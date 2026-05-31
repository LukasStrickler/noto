package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/notoapi"
)

// middleware adds auth (bearer on TCP, peer-cred on UDS), proxy-buffering
// headers for the SSE streams, and a request log line.
func (s *Server) middleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// UDS peer credentials are enforced by socket file permissions
		// (mode 0600 + owner-only dir). We don't add a Go-level check
		// here because it requires cgo on macOS; the filesystem ACL is
		// sufficient for V1.

		if s.opts.Network == "tcp" {
			auth := r.Header.Get("Authorization")
			expected := "Bearer " + s.opts.Token
			if auth != expected {
				w.Header().Set("WWW-Authenticate", "Bearer realm=\"noto\"")
				writeError(w, notoapi.NewError(notoapi.CodeUnauthorized, "authentication required", nil))
				return
			}
		}

		// SSE streams need these flags to survive reverse proxies.
		if strings.HasPrefix(r.URL.Path, "/v1/jobs/events") ||
			strings.HasPrefix(r.URL.Path, "/v1/recording/meters") {
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
		}

		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(ww, r)
		if r.URL.Path != "/v1/healthz" {
			s.logger.Printf("noto %s %s %d %s", r.Method, r.URL.Path, ww.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(c int) {
	s.status = c
	s.ResponseWriter.WriteHeader(c)
}

// Flush passes through to the underlying writer if it supports it,
// required for SSE.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// encodeJSON is a tiny wrapper to keep the import surface in one place.
func encodeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
