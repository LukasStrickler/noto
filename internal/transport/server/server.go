// Package server exposes the noto Service over HTTP/JSON + SSE.
// Two transports: HTTP/JSON over a Unix domain socket (local default)
// and TCP (`noto serve --listen tcp:host:port`). The wire format is
// stable: see internal/notoapi for every DTO.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lukasstrickler/noto/internal/app/service"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// Options configure how the server listens.
type Options struct {
	// Network is "unix" or "tcp".
	Network string
	// Address is the socket path (unix) or host:port (tcp).
	Address string
	// Token is required for TCP listeners. Empty = bind only on UDS.
	Token string
	// Logger is optional; defaults to log.Default().
	Logger *log.Logger
}

// Server wraps an *http.Server plus its listener.
type Server struct {
	opts    Options
	svc     *service.Service
	logger  *log.Logger
	httpSrv *http.Server
	ln      net.Listener

	mu      sync.Mutex
	closed  bool
	closeFn func()
}

// New constructs but does not start a Server.
func New(svc *service.Service, opts Options) (*Server, error) {
	if svc == nil {
		return nil, errors.New("server: svc is required")
	}
	if opts.Network == "" {
		opts.Network = "unix"
	}
	if opts.Address == "" {
		return nil, errors.New("server: Address is required")
	}
	if opts.Network == "tcp" && opts.Token == "" {
		return nil, errors.New("server: a token is required for TCP listeners")
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	return &Server{opts: opts, svc: svc, logger: opts.Logger}, nil
}

// Start binds the listener and serves in the background. Returns once
// the listener is ready (so callers can probe it). Stop with Close.
func (s *Server) Start(ctx context.Context) error {
	ln, err := bindListener(s.opts.Network, s.opts.Address)
	if err != nil {
		return err
	}
	s.ln = ln

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	handler := s.middleware(mux)

	s.httpSrv = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		// Long reads ok: SSE streams stay open indefinitely.
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	srvCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.closeFn = cancel
	s.mu.Unlock()

	go func() {
		<-srvCtx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = s.httpSrv.Shutdown(shutdownCtx)
		_ = ln.Close()
		if s.opts.Network == "unix" {
			_ = os.Remove(s.opts.Address)
		}
	}()

	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Printf("noto server: %v", err)
		}
	}()

	return nil
}

// Close stops the server gracefully. Idempotent.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.closeFn != nil {
		s.closeFn()
	}
	return nil
}

// Addr returns the bound address (e.g. for tests).
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// bindListener opens the appropriate listener. For unix it deletes any
// stale socket file first.
func bindListener(network, address string) (net.Listener, error) {
	if network == "unix" {
		if err := os.MkdirAll(filepath.Dir(address), 0o700); err != nil {
			return nil, fmt.Errorf("server: prepare socket dir: %w", err)
		}
		_ = os.Remove(address)
		ln, err := net.Listen("unix", address)
		if err != nil {
			return nil, fmt.Errorf("server: listen unix: %w", err)
		}
		_ = os.Chmod(address, 0o600)
		return ln, nil
	}
	return net.Listen("tcp", address)
}

// writeJSON serializes v as JSON with the given status. err is logged.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encodeJSON(w, v)
}

// writeError converts an error to the standard envelope.
func writeError(w http.ResponseWriter, err error) {
	if apiErr, ok := notoapi.As(err); ok {
		status := statusForCode(apiErr.Code)
		writeJSON(w, status, notoapi.ErrorEnvelope{Error: apiErr})
		return
	}
	writeJSON(w, http.StatusInternalServerError, notoapi.ErrorEnvelope{
		Error: &notoapi.Error{Code: notoapi.CodeInternal, Message: err.Error()},
	})
}

func statusForCode(code string) int {
	switch code {
	case notoapi.CodeNotFound, notoapi.CodeJobNotFound:
		return http.StatusNotFound
	case notoapi.CodeInvalidRequest, notoapi.CodeSchemaValidationFailed:
		return http.StatusBadRequest
	case notoapi.CodeConflict, notoapi.CodeRecordingActive, notoapi.CodeRecordingInactive, notoapi.CodeArtifactConflict:
		return http.StatusConflict
	case notoapi.CodeUnauthorized:
		return http.StatusUnauthorized
	case notoapi.CodePermissionDenied:
		return http.StatusForbidden
	case notoapi.CodeUnsupportedCapability:
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}
