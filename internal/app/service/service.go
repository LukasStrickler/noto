// Package service holds the transport-agnostic business logic for noto.
// Every HTTP handler and every direct in-process client funnels through
// Service. Wrap, don't rewrite: existing internal packages (storage,
// search, providers, appsocket, artifacts) are composed in here.
package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/db"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/platform/search"
	"github.com/lukasstrickler/noto/internal/platform/secrets"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
	"github.com/lukasstrickler/noto/internal/transport/appsocket"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

type Service struct {
	// cfgMu guards cfg. Config values are always replaced wholesale (the
	// setters assign fresh scalar fields, never mutate slices/maps in
	// place), so a plain RWMutex around the field is sufficient. Read with
	// currentCfg(); mutate+persist with updateCfg().
	cfgMu         sync.RWMutex
	cfg           config.Config
	cfgStore      config.Store
	recordingsDir string
	sqlitePath    string
	jobsDBPath    string

	repo     repo.ArtifactRepository
	secrets  secrets.Store
	registry providers.Registry
	search   *search.SearchIndex
	jobsDB   *db.DB

	speakerProfiles   speakerstore.SpeakerProfileRepository
	meetingMappings   speakerstore.MeetingSpeakerMappingRepository
	speakerEmbedder   SpeakerEmbedder
	sttAdapterFactory func(providerID string) (stt.STTProvider, error)

	ipc *appsocket.IPCClient

	events *eventHub

	recMu         sync.RWMutex
	recording     notoapi.RecordingState
	recPending    bool // held while IPC Start is in progress; prevents concurrent starts
	recStopMeters chan struct{}

	jobsMu     sync.Mutex
	jobCancels map[string]context.CancelFunc
	workerWake chan struct{}

	started time.Time
	version string
}

type Deps struct {
	Config            config.Config
	ConfigStore       config.Store
	Secrets           secrets.Store
	Registry          providers.Registry
	Search            *search.SearchIndex
	JobsDB            *db.DB
	IPC               *appsocket.IPCClient
	Version           string
	SpeakerProfiles   speakerstore.SpeakerProfileRepository
	MeetingMappings   speakerstore.MeetingSpeakerMappingRepository
	SpeakerEmbedder   SpeakerEmbedder
	STTAdapterFactory func(providerID string) (stt.STTProvider, error)
	// Repo is the artifact storage backend. If nil, a LocalArtifactRepository
	// backed by Config.GetRecordingsDir() is created automatically.
	Repo repo.ArtifactRepository
}

func New(d Deps) *Service {
	recordingsDir := d.Config.GetRecordingsDir()
	artifactRepo := d.Repo
	if artifactRepo == nil {
		artifactRepo = repo.NewLocal(recordingsDir)
	}
	s := &Service{
		cfg:               d.Config,
		cfgStore:          d.ConfigStore,
		recordingsDir:     recordingsDir,
		sqlitePath:        filepath.Join(d.Config.ConfigDir, "noto.sqlite"),
		jobsDBPath:        filepath.Join(d.Config.ConfigDir, "noto-jobs.sqlite"),
		repo:              artifactRepo,
		secrets:           d.Secrets,
		registry:          d.Registry,
		search:            d.Search,
		jobsDB:            d.JobsDB,
		ipc:               d.IPC,
		events:            newEventHub(),
		jobCancels:        map[string]context.CancelFunc{},
		workerWake:        make(chan struct{}, 1),
		started:           time.Now(),
		version:           d.Version,
		speakerProfiles:   d.SpeakerProfiles,
		meetingMappings:   d.MeetingMappings,
		speakerEmbedder:   d.SpeakerEmbedder,
		sttAdapterFactory: d.STTAdapterFactory,
	}
	if s.speakerEmbedder == nil {
		if endpoint := os.Getenv("NOTO_SPEAKER_EMBEDDING_URL"); endpoint != "" {
			s.speakerEmbedder = NewHTTPSpeakerEmbedder(endpoint)
		}
	}
	if s.version == "" {
		s.version = "0.0.0-dev"
	}

	return s
}

// currentCfg returns a snapshot copy of the live config under read lock.
// Callers may read the returned value freely; the setters never mutate the
// underlying config in place, so the snapshot stays consistent.
func (s *Service) currentCfg() config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// updateCfg applies mutate to a copy of the live config, persists it, and
// swaps it in only on a successful Save. If Save fails the in-memory config
// is left untouched, so a failed write never leaves the two out of sync.
func (s *Service) updateCfg(mutate func(*config.Config)) error {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	next := s.cfg
	mutate(&next)
	if err := s.cfgStore.Save(next); err != nil {
		return err
	}
	s.cfg = next
	return nil
}

func (s *Service) Start(ctx context.Context) error {
	if s.jobsDB == nil {
		return fmt.Errorf("service: JobsDB is required")
	}
	if err := s.markInterruptedJobs(ctx); err != nil {
		return fmt.Errorf("service: recover jobs: %w", err)
	}
	s.startWorkers(ctx)
	go s.statusBarLoop(ctx)
	return nil
}

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

func (s *Service) Events() *eventHub {
	return s.events
}

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

func (s *Service) newSTTAdapter(providerID string) (stt.STTProvider, error) {
	if s.sttAdapterFactory != nil {
		return s.sttAdapterFactory(providerID)
	}
	switch providerID {
	case "assemblyai":
		key, _ := s.secrets.Get(context.Background(), "provider:assemblyai")
		return &stt.AssemblyAIAdapter{APIKey: key}, nil
	default:
		return nil, fmt.Errorf("unknown STT provider: %s (production STT is AssemblyAI-only)", providerID)
	}
}
