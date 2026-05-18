// Package service holds the transport-agnostic business logic for noto.
// Every HTTP handler and every direct in-process client funnels through
// Service. Wrap, don't rewrite: existing internal packages (storage,
// search, providers, appsocket, artifacts) are composed in here.
package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/lukasstrickler/noto/internal/appsocket"
	"github.com/lukasstrickler/noto/internal/config"
	"github.com/lukasstrickler/noto/internal/db"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/providers"
	"github.com/lukasstrickler/noto/internal/search"
	"github.com/lukasstrickler/noto/internal/secrets"
)

// Service is the core. It is safe for concurrent use by HTTP handlers and
// the in-process direct client.
type Service struct {
	cfg           config.Config
	cfgStore      config.Store
	recordingsDir string
	sqlitePath    string
	jobsDBPath    string

	secrets  secrets.Store
	registry providers.Registry
	search   *search.SearchIndex
	jobsDB   *db.DB

	ipc *appsocket.IPCClient

	events *eventHub

	// recorder state
	recMu     sync.RWMutex
	recording notoapi.RecordingState
	recStopMeters chan struct{}

	// jobs runtime
	jobsMu     sync.Mutex
	jobCancels map[string]context.CancelFunc

	started time.Time
	version string
}

// Deps is what callers must supply to build a Service.
type Deps struct {
	Config        config.Config
	ConfigStore   config.Store
	Secrets       secrets.Store
	Registry      providers.Registry
	Search        *search.SearchIndex
	JobsDB        *db.DB
	IPC           *appsocket.IPCClient
	Version       string
}

// New wires a Service from the given deps. None may be nil except IPC
// (which is platform-specific; nil means recording is unavailable).
func New(d Deps) *Service {
	s := &Service{
		cfg:           d.Config,
		cfgStore:      d.ConfigStore,
		recordingsDir: d.Config.GetRecordingsDir(),
		sqlitePath:    filepath.Join(d.Config.ConfigDir, "noto.sqlite"),
		jobsDBPath:    filepath.Join(d.Config.ConfigDir, "noto-jobs.sqlite"),
		secrets:       d.Secrets,
		registry:      d.Registry,
		search:        d.Search,
		jobsDB:        d.JobsDB,
		ipc:           d.IPC,
		events:        newEventHub(),
		jobCancels:    map[string]context.CancelFunc{},
		started:       time.Now(),
		version:       d.Version,
	}
	if s.version == "" {
		s.version = "0.0.0-dev"
	}
	return s
}

// Start kicks off background workers (job loop, status-bar broadcast).
// It is safe to call once; subsequent calls are no-ops. ctx controls the
// lifetime of background goroutines: cancel it to stop them cleanly.
func (s *Service) Start(ctx context.Context) error {
	if s.jobsDB == nil {
		return fmt.Errorf("service: JobsDB is required")
	}
	// Mark any in-flight jobs from a previous run as interrupted so the
	// user can retry deliberately rather than silently restarting STT.
	if err := s.markInterruptedJobs(ctx); err != nil {
		return fmt.Errorf("service: recover jobs: %w", err)
	}
	s.startWorkers(ctx)
	go s.statusBarLoop(ctx)
	return nil
}

// Close shuts down the service: closes the events hub, the search index,
// and any IPC connection. The shared *sql.DBs are closed by the caller.
func (s *Service) Close() error {
	s.events.close()
	s.recMu.Lock()
	if s.recStopMeters != nil {
		close(s.recStopMeters)
		s.recStopMeters = nil
	}
	s.recMu.Unlock()
	if s.ipc != nil {
		_ = s.ipc.Close()
	}
	return nil
}

// Events returns the in-process event hub for HTTP/SSE handlers and the
// direct client to subscribe to.
func (s *Service) Events() *eventHub {
	return s.events
}

// Health returns the standard health payload.
func (s *Service) Health(_ context.Context) (notoapi.Health, error) {
	return notoapi.Health{
		OK:              true,
		Version:         s.version,
		StartedAt:       s.started,
		UptimeSec:       int(time.Since(s.started).Seconds()),
		PID:             pid(),
		RecordingActive: s.recordingActive(),
	}, nil
}

func (s *Service) recordingActive() bool {
	s.recMu.RLock()
	defer s.recMu.RUnlock()
	return s.recording.Active
}
