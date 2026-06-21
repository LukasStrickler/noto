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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukasstrickler/noto/internal/platform/compute"
	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/db"
	"github.com/lukasstrickler/noto/internal/platform/models"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/speaker"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt/parakeet"
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
	speakerMu         sync.Mutex // guards lazy resolution of speakerEmbedder
	speakerEmbedder   SpeakerEmbedder
	diarizer          diarize.Diarizer // standalone speaker-turn seam; nil disables diarization
	sttAdapterFactory func(providerID string) (stt.STTProvider, error)

	// compute is the accelerator plan resolved once at backend startup and
	// injected into the local model-runtime constructors. Surfaced via
	// /v1/system so a client can show "backend: Mac M1, CoreML".
	compute compute.Plan

	// pool owns the lifetime of loaded local models (lazy load + idle evict),
	// so multi-GB STT/diarizer weights don't all sit pinned in RAM. ECAPA is
	// tiny and pinned; heavy models acquire/release through it.
	pool *modelPool

	// newModelManager builds the model manager for a data dir. Overridable in
	// tests; defaults to models.New (the pinned default manifest).
	newModelManager func(dataDir string) *models.Manager

	ipc *appsocket.IPCClient

	events *eventHub

	recMu         sync.RWMutex
	recording     notoapi.RecordingState
	recPending    bool // held while IPC Start is in progress; prevents concurrent starts
	recStopMeters chan struct{}

	jobsMu     sync.Mutex
	jobCancels map[string]context.CancelCauseFunc
	workerWake chan struct{}

	// bgCancel stops the goroutines Start launched (the worker pool, status-bar
	// and evictor loops); bgWG tracks them so Close can WAIT for them to drain
	// before tearing down the resources they use (the jobs DB, the event hub).
	// Without this, a worker could write the jobs DB or publish to a closed hub
	// after Close returned — a shutdown race (and the source of a TempDir-cleanup
	// flake in the host conformance test).
	bgCancel context.CancelFunc
	bgWG     sync.WaitGroup

	started time.Time
	version string
}

type Deps struct {
	Config          config.Config
	ConfigStore     config.Store
	Secrets         secrets.Store
	Registry        providers.Registry
	Search          *search.SearchIndex
	JobsDB          *db.DB
	IPC             *appsocket.IPCClient
	Version         string
	SpeakerProfiles speakerstore.SpeakerProfileRepository
	MeetingMappings speakerstore.MeetingSpeakerMappingRepository
	SpeakerEmbedder SpeakerEmbedder
	// Diarizer is the standalone speaker-turn provider. If nil, diarization is
	// disabled and the STT provider's own speaker labels (if any) stand.
	Diarizer          diarize.Diarizer
	STTAdapterFactory func(providerID string) (stt.STTProvider, error)
	// Repo is the artifact storage backend. If nil, a LocalArtifactRepository
	// backed by Config.GetRecordingsDir() is created automatically.
	Repo repo.ArtifactRepository
	// Compute is the accelerator plan resolved at host startup. The zero value
	// (CPU, unverified) is a safe default for tests and CPU-only hosts.
	Compute compute.Plan
	// ModelManager builds the model manager for a data dir. If nil, models.New
	// (the pinned default manifest) is used. Tests inject a manifest here.
	ModelManager func(dataDir string) *models.Manager
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
		jobCancels:        map[string]context.CancelCauseFunc{},
		workerWake:        make(chan struct{}, 1),
		started:           time.Now(),
		version:           d.Version,
		speakerProfiles:   d.SpeakerProfiles,
		meetingMappings:   d.MeetingMappings,
		speakerEmbedder:   d.SpeakerEmbedder,
		diarizer:          d.Diarizer,
		sttAdapterFactory: d.STTAdapterFactory,
		compute:           d.Compute,
		newModelManager:   d.ModelManager,
	}
	if s.newModelManager == nil {
		s.newModelManager = models.New
	}
	// Idle eviction frees RAM ~90s after a batch transcribe finishes. An
	// always-hot server (hosted GPU box) sets NOTO_MODEL_KEEP_WARM to keep
	// models resident and avoid per-request reload latency.
	idleTTL := 90 * time.Second
	if os.Getenv("NOTO_MODEL_KEEP_WARM") != "" {
		idleTTL = 0
	}
	s.pool = newModelPool(idleTTL, parseMaxResidentBytes(os.Getenv("NOTO_MODEL_MAX_RESIDENT_MB")))
	if s.speakerEmbedder == nil {
		// A remote embedding endpoint is fixed for the process lifetime, so wire
		// it eagerly. The in-process ECAPA provider is resolved lazily instead
		// (see activeSpeakerEmbedder) so a model installed via
		// `noto speaker-model download` is picked up on the next transcribe
		// without restarting a long-running server.
		if endpoint := os.Getenv("NOTO_SPEAKER_EMBEDDING_URL"); endpoint != "" {
			s.speakerEmbedder = NewHTTPSpeakerEmbedder(endpoint)
		}
	}
	if s.version == "" {
		s.version = "0.0.0-dev"
	}

	return s
}

// activeSpeakerEmbedder returns the embedder to use for the next transcribe,
// resolving the in-process ECAPA provider on demand. Returning nil simply
// disables speaker matching. The local provider is opened the first time the
// model is present in the data dir and then cached, so installing it via
// `noto speaker-model download` takes effect without restarting the server.
func (s *Service) activeSpeakerEmbedder() SpeakerEmbedder {
	s.speakerMu.Lock()
	defer s.speakerMu.Unlock()
	if s.speakerEmbedder != nil {
		return s.speakerEmbedder
	}
	cfg := s.currentCfg()
	// Config-declared remote embed endpoint (identity offload). The default is
	// deliberately LOCAL — voice prints are cheap to compute and the most
	// sensitive data class, so they stay on-device unless explicitly offloaded.
	// The endpoint speaks the documented speaker-embedding-service contract
	// (POST /v1/speaker-embeddings), so point compute.embed.url at such a
	// service, not at the transcribe/diarize compute box.
	if ep, remote := cfg.Compute.Resolve(cfg.Compute.Embed); remote {
		s.speakerEmbedder = NewHTTPSpeakerEmbedder(ep.URL)
		return s.speakerEmbedder
	}
	dir := cfg.ConfigDir
	if speaker.Available(dir) {
		// Best-effort: a load failure leaves matching disabled (and we retry on
		// the next call) rather than wedging the embedder. The pool pins ECAPA
		// (it's tiny) and owns its lifetime, so a single ORT session is shared
		// across transcribes and torn down once on shutdown.
		m, err := s.pool.pin("embed:ecapa", func() (loadedModel, error) {
			return speaker.Open(dir)
		})
		if err == nil {
			if emb, ok := m.(SpeakerEmbedder); ok {
				s.speakerEmbedder = emb
			}
		}
	}
	return s.speakerEmbedder
}

// parseMaxResidentBytes converts a "MB" env value into a byte budget for the
// model pool. Empty/invalid → 0 (unlimited; idle eviction still applies).
func parseMaxResidentBytes(mb string) int64 {
	mb = strings.TrimSpace(mb)
	if mb == "" {
		return 0
	}
	n, err := strconv.ParseInt(mb, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n << 20
}

// activeDiarizer returns the diarizer for the next transcribe, or nil to leave
// diarization disabled (the local STT provider emits no speaker labels, so with
// no diarizer every participants-channel word stays a single anonymous voice).
// An injected diarizer wins; otherwise this is where a local sherpa-backed
// diarizer will be lazily resolved once its engine is wired (mirrors
// activeSpeakerEmbedder).
func (s *Service) activeDiarizer() diarize.Diarizer {
	s.speakerMu.Lock()
	defer s.speakerMu.Unlock()
	return s.diarizer
}

// applyVADEnv bridges the Compute.VAD config to the NOTO_VAD* environment the
// diar server reads, so an enabled VAD trims silence (cutting the dominant diar
// embedding cost) on every local / `noto serve` diarization this process spawns —
// the production opt-in for plan §0.8/B1. It only SETS env when VAD is enabled (it
// never unsets), so a direct NOTO_VAD env override is still honored. No-op when
// disabled.
func (s *Service) applyVADEnv() {
	for _, kv := range s.currentCfg().Compute.VAD.Env() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			_ = os.Setenv(k, v)
		}
	}
}

// reapplyVADEnv makes the live NOTO_VAD* env match the persisted config exactly,
// for an explicit runtime toggle (the Config screen). Unlike applyVADEnv it first
// clears every key the config owns, so turning VAD OFF actually removes the env a
// previous enable set; a deliberate UI toggle is authoritative (a startup manual
// override is intentionally not preserved here).
//
// Scope of "live": a diar server reads NOTO_VAD from os.Environ at exec, so the new
// posture reaches the NEXT diar-server incarnation. A server not yet started this
// session picks it up on first start (the common first-run case). An already-WARM
// local pyannote server is a long-lived singleton that captured os.Environ once at
// exec, so it keeps its old posture until it next restarts (crash or app restart) —
// we deliberately don't kill a warm server with diarizations possibly in flight.
// (A Modal/remote worker reads VAD from its own deploy env, not this process.)
func (s *Service) reapplyVADEnv() {
	for _, k := range config.VADEnvKeys() {
		_ = os.Unsetenv(k)
	}
	s.applyVADEnv()
}

// ComputePlan returns the accelerator plan resolved at startup. Used by the
// /v1/system endpoint so a client can display the backend's capabilities.
func (s *Service) ComputePlan() compute.Plan { return s.compute }

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
	s.applyVADEnv()
	if err := s.recoverInterruptedJobs(ctx); err != nil {
		return fmt.Errorf("service: recover jobs: %w", err)
	}
	// Own the background goroutines' lifetime so Close can stop AND join them.
	ctx, s.bgCancel = context.WithCancel(ctx)
	s.startWorkers(ctx)
	s.goBG(func() { s.statusBarLoop(ctx) })
	s.goBG(func() { s.pool.runEvictor(ctx) })
	return nil
}

// goBG runs fn as a tracked background goroutine so Close can wait for it.
func (s *Service) goBG(fn func()) {
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		fn()
	}()
}

func (s *Service) Close() error {
	// Stop the background goroutines and WAIT for them to drain BEFORE closing
	// the resources they touch (event hub, model pool) — a worker mid-job writes
	// the jobs DB and publishes job events, so tearing those down first would
	// race it. Canceling propagates into any in-flight job's context, so a worker
	// aborts promptly rather than running the whole job to completion.
	if s.bgCancel != nil {
		s.bgCancel()
	}
	s.bgWG.Wait()

	s.events.close()
	s.recMu.Lock()
	if s.recStopMeters != nil {
		close(s.recStopMeters)
		s.recStopMeters = nil
	}
	s.recMu.Unlock()
	if s.pool != nil {
		s.pool.closeAll()
	}
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
	case parakeet.ProviderID, "":
		// Local-first: on-device STT is the only transcription path. The model
		// manager + accelerator plan are injected so the provider can fetch
		// weights and pick the fastest engine present.
		mgr := s.newModelManager(s.currentCfg().ConfigDir)
		return parakeet.New(mgr, s.compute), nil
	default:
		return nil, fmt.Errorf("unknown STT provider: %s (noto transcribes locally; only %q is supported)", providerID, parakeet.ProviderID)
	}
}
