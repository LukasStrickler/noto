// Package notohost owns the noto server process lifecycle on the local
// machine. It is the seam between "I want to talk to noto" and "is
// there a server running on this socket".
//
// Two roles:
//
//   - In TUI/CLI mode the host starts a server in-process on a UDS,
//     hands the caller a notoapi.Client, and tears it all down on
//     Close. The TUI is the lifeline: when it exits the server dies.
//
//   - In `noto serve` mode the host is the server, period. It runs
//     until SIGINT/SIGTERM and exposes a UDS plus, optionally, a TCP
//     listener with bearer-token auth for remote clients.
package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/app/service"
	"github.com/lukasstrickler/noto/internal/platform/compute"
	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/db"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/platform/search"
	"github.com/lukasstrickler/noto/internal/platform/secrets"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
	"github.com/lukasstrickler/noto/internal/transport/apiclient"
	"github.com/lukasstrickler/noto/internal/transport/appsocket"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/transport/server"
)

// Options control how the host wires itself.
type Options struct {
	// Network: "unix" or "tcp". Empty defaults to "unix".
	Network string
	// Address: socket path or host:port. Empty means UDSDefault.
	Address string
	// Token: required for TCP. Auto-generated if empty.
	Token string
	// ServerOnly: don't return a client; just stand up the server.
	ServerOnly bool
	// Version is reported in the health endpoint.
	Version string
	// Logger is optional.
	Logger *log.Logger
}

// Host is a running noto backend + (optionally) a local client to it.
type Host struct {
	svc    *service.Service
	srv    *server.Server
	client notoapi.Client
	deps   ownedDeps
	logger *log.Logger
	addr   string
	token  string

	cancel context.CancelFunc
}

type ownedDeps struct {
	search          *search.SearchIndex
	jobsDB          *db.DB
	ipc             *appsocket.IPCClient
	cfg             config.Store
	speakerProfiles speakerstore.SpeakerProfileRepository
	meetingMappings speakerstore.MeetingSpeakerMappingRepository
	// appDB is the single noto.sqlite handle shared by the search index and
	// the speaker store; the host owns it and closes it once.
	appDB *db.DB
}

// Start prepares the deps, starts the service workers, and binds the
// HTTP listener. It returns once the listener is ready.
func Start(ctx context.Context, opts Options) (*Host, error) {
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}

	cfg, cfgStore, err := loadConfig()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(cfg.ConfigDir, 0o700); err != nil {
		return nil, fmt.Errorf("notohost: prepare config dir: %w", err)
	}
	recordingsDir := cfg.GetRecordingsDir()
	if err := os.MkdirAll(recordingsDir, 0o755); err != nil {
		return nil, fmt.Errorf("notohost: prepare recordings dir: %w", err)
	}

	// One handle to noto.sqlite, shared by the search index and the speaker
	// store (these used to open the same file twice, with mismatched pragmas).
	// The jobs queue lives in a separate file so its write traffic never
	// contends with the index.
	sqlitePath := filepath.Join(cfg.ConfigDir, "noto.sqlite")
	appDB, err := db.Open(sqlitePath)
	if err != nil {
		return nil, fmt.Errorf("notohost: open app db: %w", err)
	}
	idx, err := search.NewSearchIndexConn(appDB.DB)
	if err != nil {
		_ = appDB.Close()
		return nil, fmt.Errorf("notohost: init search index: %w", err)
	}
	if err := speakerstore.Migrate(appDB); err != nil {
		_ = appDB.Close()
		return nil, fmt.Errorf("notohost: migrate speaker store: %w", err)
	}
	speakerProfiles := speakerstore.NewSQLiteSpeakerProfileRepository(appDB)
	meetingMappings := speakerstore.NewSQLiteMeetingSpeakerMappingRepository(appDB)

	jobsDBPath := filepath.Join(cfg.ConfigDir, "noto-jobs.sqlite")
	jobsDB, err := db.Open(jobsDBPath)
	if err != nil {
		_ = appDB.Close()
		return nil, fmt.Errorf("notohost: open jobs db: %w", err)
	}
	if err := jobsDB.Migrate(db.JobsSchema); err != nil {
		_ = appDB.Close()
		_ = jobsDB.Close()
		return nil, fmt.Errorf("notohost: migrate jobs db: %w", err)
	}

	// IPC client is best-effort; nil if no Swift helper exists on this
	// machine (Linux/Windows or macOS without xcode). When nil, the
	// service falls through to dry-run recordings so the TUI flow
	// stays usable for development.
	var ipc *appsocket.IPCClient
	if helperExistsOnSystem() {
		ipc, _ = appsocket.NewIPCClient()
	}

	registry := providers.DefaultRegistry()
	// macOS uses the system Keychain. Everywhere else falls back to
	// a file under ~/.noto/credentials.json with 0600 perms — better
	// than refusing to persist keys, and the TUI surfaces the source
	// so users see exactly which backend protects their speakerstore.
	var primary secrets.Store
	if runtime.GOOS == "darwin" {
		primary = secrets.KeychainStore{}
	} else {
		primary = secrets.NewFileStore(filepath.Join(cfg.ConfigDir, "credentials.json"))
	}
	store := secrets.EnvFallbackStore{Primary: primary}

	// Resolve the accelerator plan once, here, on the machine that runs the
	// backend (the Mac in local mode, the Linux server in remote mode). The
	// Verify hook is nil for now — the candidate ladder is trusted and the
	// real safety net is each local provider's fall-back-to-CPU at Open time.
	// NOTO_COMPUTE / NOTO_MODEL_TIER override the choice.
	computePlan := compute.DetectFromEnv(nil, func(format string, args ...any) {
		logger.Printf(format, args...)
	})

	// Select the artifact store. Default is local (SQLite + filesystem). With
	// storage.type=remote the pipeline still runs here, but its output is written
	// through to a remote data plane (the "compute local, store off-site" mode);
	// audio stays in the local recordings dir as a staging area.
	artifactRepo := selectRepo(ctx, cfg, store, recordingsDir, logger)

	svc := service.New(service.Deps{
		Config:          cfg,
		ConfigStore:     cfgStore,
		Secrets:         store,
		Registry:        registry,
		Search:          idx,
		JobsDB:          jobsDB,
		IPC:             ipc,
		Version:         opts.Version,
		SpeakerProfiles: speakerProfiles,
		MeetingMappings: meetingMappings,
		Repo:            artifactRepo,
		Compute:         computePlan,
	})

	serviceCtx, cancel := context.WithCancel(ctx)
	if err := svc.Start(serviceCtx); err != nil {
		cancel()
		_ = appDB.Close()
		_ = jobsDB.Close()
		return nil, err
	}

	// Pick listener parameters.
	network := opts.Network
	if network == "" {
		network = "unix"
	}
	address := opts.Address
	if network == "unix" && address == "" {
		address = DefaultSocketPath(cfg.ConfigDir)
	}
	if network == "tcp" && address == "" {
		address = "127.0.0.1:0"
	}
	token := opts.Token
	if network == "tcp" && token == "" {
		token = generateToken()
	}

	srv, err := server.New(svc, server.Options{
		Network: network,
		Address: address,
		Token:   token,
		Logger:  logger,
	})
	if err != nil {
		cancel()
		_ = svc.Close()
		_ = appDB.Close()
		_ = jobsDB.Close()
		return nil, err
	}
	if err := srv.Start(serviceCtx); err != nil {
		cancel()
		_ = svc.Close()
		_ = appDB.Close()
		_ = jobsDB.Close()
		return nil, err
	}

	// Capture the actual bound address (in case opts.Address was
	// "127.0.0.1:0" or similar, the listener picked a real port).
	boundAddr := srv.Addr()
	if boundAddr == "" {
		boundAddr = address
	}

	h := &Host{
		svc:    svc,
		srv:    srv,
		deps:   ownedDeps{search: idx, jobsDB: jobsDB, ipc: ipc, cfg: cfgStore, speakerProfiles: speakerProfiles, meetingMappings: meetingMappings, appDB: appDB},
		logger: logger,
		addr:   boundAddr,
		token:  token,
		cancel: cancel,
	}

	if !opts.ServerOnly {
		// Always prefer the direct in-process client locally: it has
		// zero round-trip overhead and works regardless of socket
		// permissions.
		h.client = apiclient.NewDirect(svc)
	}

	if network == "unix" {
		if err := h.waitReady(); err != nil {
			_ = h.Close()
			return nil, err
		}
	}
	return h, nil
}

// Client returns the in-process notoapi.Client.
func (h *Host) Client() notoapi.Client {
	return h.client
}

// SeedDev populates the local store with the bundled fixture meetings plus a
// People directory + speaker mappings. Dev-only: not exposed over the wire.
func (h *Host) SeedDev(ctx context.Context) (service.SeedResult, error) {
	if h.svc == nil {
		return service.SeedResult{}, errors.New("notohost: service not initialized")
	}
	return h.svc.SeedDev(ctx)
}

// PurgeAll wipes all local meetings + speaker profiles back to empty.
// Dev-only: not exposed over the wire.
func (h *Host) PurgeAll(ctx context.Context) (service.PurgeResult, error) {
	if h.svc == nil {
		return service.PurgeResult{}, errors.New("notohost: service not initialized")
	}
	return h.svc.PurgeAll(ctx)
}

// Addr is the listener address (socket path or host:port).
func (h *Host) Addr() string { return h.addr }

// Token is the bearer token used for TCP listeners (empty for UDS).
func (h *Host) Token() string { return h.token }

// SocketPath returns the UDS path if network is unix; empty otherwise.
func (h *Host) SocketPath() string {
	if strings.HasPrefix(h.addr, "/") || strings.HasSuffix(h.addr, ".sock") {
		return h.addr
	}
	return ""
}

// Close stops the server and closes deps. Idempotent.
func (h *Host) Close() error {
	if h.cancel != nil {
		h.cancel()
	}
	if h.srv != nil {
		_ = h.srv.Close()
	}
	if h.svc != nil {
		_ = h.svc.Close()
	}
	if h.deps.search != nil {
		_ = h.deps.search.Close() // no-op: borrows the shared appDB handle
	}
	if h.deps.jobsDB != nil {
		_ = h.deps.jobsDB.Close()
	}
	if h.deps.ipc != nil {
		_ = h.deps.ipc.Close()
	}
	if h.deps.appDB != nil {
		_ = h.deps.appDB.Close()
	}
	return nil
}

// waitReady probes /v1/healthz until it returns OK or 2s elapse, so
// callers don't hit a race on first use of an in-process server.
func (h *Host) waitReady() error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err := h.svc.Health(ctx)
		cancel()
		if err == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("notohost: server did not become ready in time")
}

// DefaultSocketPath returns the canonical local UDS path inside cfgDir.
func DefaultSocketPath(cfgDir string) string {
	return filepath.Join(cfgDir, "api.sock")
}

// loadConfig reads the noto config from disk (defaults if missing).
func loadConfig() (config.Config, config.Store, error) {
	cfgDir := config.DefaultConfigDir()
	store := config.NewStore(cfgDir)
	cfg, err := store.Load()
	if err != nil {
		return config.Config{}, config.Store{}, fmt.Errorf("notohost: load config: %w", err)
	}
	if cfg.ConfigDir == "" {
		cfg.ConfigDir = cfgDir
	}
	if cfg.ArtifactRoot == "" {
		cfg.ArtifactRoot = config.DefaultArtifactRoot()
	}
	if cfg.SchemaVersion == "" {
		cfg.SchemaVersion = "config.v1"
	}
	if cfg.UI.Theme == "" {
		cfg.UI.Theme = config.DefaultUITheme
	}
	if cfg.Routing.SpeechProvider == "" {
		cfg.Routing.SpeechProvider = config.DefaultSTTProvider
	}
	if cfg.Routing.LLMProvider == "" {
		cfg.Routing.LLMProvider = config.DefaultLLMProvider
	}
	if cfg.Routing.LLMModel == "" {
		cfg.Routing.LLMModel = config.DefaultLLMModel
	}
	return cfg, store, nil
}

// Connect returns a client for an *existing* running server, or starts
// one in-process if no server is reachable.
//
// Discovery rule:
//  1. If NOTO_API_URL is set, use it (with optional NOTO_API_TOKEN).
//  2. If the default UDS exists and responds to /v1/healthz, use it.
//  3. Otherwise spawn an in-process Host owned by the caller.
//
// The returned closeFn is non-nil only when the caller owns the Host
// (case 3). It must be invoked to free resources.
func Connect(ctx context.Context, opts Options) (notoapi.Client, func(), error) {
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	forceInProcess := truthyEnv("NOTO_FORCE_IN_PROCESS")
	if remote := os.Getenv("NOTO_API_URL"); remote != "" {
		if forceInProcess {
			logger.Printf("notohost: ignoring NOTO_API_URL because NOTO_FORCE_IN_PROCESS is set")
		} else {
			token := os.Getenv("NOTO_API_TOKEN")
			client := apiclient.NewHTTP(apiclient.HTTPOptions{
				BaseURL: remote,
				Token:   token,
			})
			if _, err := client.Health(ctx); err != nil {
				return nil, nil, fmt.Errorf("notohost: cannot reach %s: %w", remote, err)
			}
			return maybeWrapEdgeCapture(ctx, client, logger), nil, nil
		}
	}
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, nil, err
	}
	// Config-declared remote backend (the persistent "thin client" setup). The
	// bearer token lives in the secrets store, never config.yaml.
	if cfg.Backend.IsRemote() && !forceInProcess {
		client := apiclient.NewHTTP(apiclient.HTTPOptions{
			BaseURL: cfg.Backend.Remote.URL,
			Token:   resolveBackendToken(cfg),
		})
		if _, err := client.Health(ctx); err != nil {
			return nil, nil, fmt.Errorf("notohost: cannot reach remote backend %s: %w", cfg.Backend.Remote.URL, err)
		}
		return maybeWrapEdgeCapture(ctx, client, logger), nil, nil
	}
	sock := DefaultSocketPath(cfg.ConfigDir)
	if !forceInProcess && probeSocket(ctx, sock) {
		client := apiclient.NewHTTP(apiclient.HTTPOptions{SocketPath: sock})
		return client, nil, nil
	}
	host, err := Start(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	return host.Client(), func() { _ = host.Close() }, nil
}

// maybeWrapEdgeCapture upgrades a remote client to capture audio locally when
// the connected backend is headless. A Mac TUI pointed at a Linux data plane
// reports CaptureAvailable=false; if this machine has a capture helper, we wrap
// the client so recording runs here and uploads to the backend on stop. When the
// backend can capture (or we have no helper), the client is returned unchanged —
// the common path is untouched and the CLI/TUI need no edge-capture awareness.
func maybeWrapEdgeCapture(ctx context.Context, client notoapi.Client, logger *log.Logger) notoapi.Client {
	sys, err := client.GetSystem(ctx)
	if err != nil || sys.CaptureAvailable {
		return client // backend captures (or unknown) — leave it
	}
	if !helperExistsOnSystem() {
		logger.Printf("notohost: backend is headless and no local capture helper found — recording disabled")
		return client
	}
	ipc, err := appsocket.NewIPCClient()
	if err != nil {
		return client
	}
	logger.Printf("notohost: backend cannot capture — recording locally and uploading on stop (edge capture)")
	return apiclient.NewEdgeCapture(client, ipcCapturer{ipc: ipc}, logger.Printf)
}

// ipcCapturer adapts the macOS Swift helper (appsocket.IPCClient) to the
// apiclient.LocalCapturer the edge recorder drives.
type ipcCapturer struct{ ipc *appsocket.IPCClient }

func (c ipcCapturer) Start(ctx context.Context, sources []string) error {
	if err := c.ipc.Connect(ctx); err != nil {
		return err
	}
	_, err := c.ipc.Start(ctx, sources, 48000)
	return err
}

func (c ipcCapturer) Stop(ctx context.Context) (string, int, error) {
	res, err := c.ipc.Stop(ctx)
	if err != nil {
		return "", 0, err
	}
	return res.OutputPath, int(res.DurationSecs), nil
}

func (c ipcCapturer) Pause(ctx context.Context) error  { return c.ipc.Pause(ctx) }
func (c ipcCapturer) Resume(ctx context.Context) error { return c.ipc.Resume(ctx) }

func (c ipcCapturer) Level(ctx context.Context) (int, int, error) {
	lvl, err := c.ipc.GetAudioLevel(ctx)
	if err != nil {
		return 0, 0, err
	}
	return int(lvl.Left), int(lvl.Right), nil
}

func (c ipcCapturer) Close() error { return c.ipc.Close() }

// selectRepo chooses the artifact store backend from config. With
// storage.type=remote it writes artifacts through to a remote data plane (audio
// stays in the local recordings dir as staging); otherwise it stays local.
func selectRepo(ctx context.Context, cfg config.Config, store secrets.Store, recordingsDir string, logger *log.Logger) repo.ArtifactRepository {
	if cfg.Storage.IsRemote() {
		token := ""
		if ref := strings.TrimSpace(cfg.Storage.Remote.TokenRef); ref != "" {
			token, _ = store.Get(ctx, ref)
		}
		logger.Printf("storage: remote data plane at %s (audio stays local)", cfg.Storage.Remote.URL)
		return repo.NewRemote(cfg.Storage.Remote.URL, token, recordingsDir)
	}
	return repo.NewLocal(recordingsDir)
}

// resolveBackendToken loads the remote backend's bearer token from the secrets
// store (Keychain on macOS, credentials.json elsewhere), mirroring how provider
// keys are protected. Returns "" if no ref is configured.
func resolveBackendToken(cfg config.Config) string {
	ref := strings.TrimSpace(cfg.Backend.Remote.TokenRef)
	if ref == "" {
		return ""
	}
	var primary secrets.Store
	if runtime.GOOS == "darwin" {
		primary = secrets.KeychainStore{}
	} else {
		primary = secrets.NewFileStore(filepath.Join(cfg.ConfigDir, "credentials.json"))
	}
	store := secrets.EnvFallbackStore{Primary: primary}
	tok, _ := store.Get(context.Background(), ref)
	return tok
}

// probeSocket returns true if a noto server is reachable at sock.
func probeSocket(ctx context.Context, sock string) bool {
	if _, err := os.Stat(sock); err != nil {
		return false
	}
	c := &http.Client{
		Timeout: 200 * time.Millisecond,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/healthz", nil)
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func truthyEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// generateToken returns a 32-char hex token.
func generateToken() string {
	return NewToken()
}

// NewToken is the exported alias used by the CLI's `serve --token-file`.
func NewToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// touch satisfies the import for notoapi when only types are used.
var _ = notoapi.Health{}

// helperExistsOnSystem returns true if any of the discovery candidates
// for the macOS Swift helper resolve. We don't *run* it here; just
// check existence so the IPC client doesn't sit waiting on a missing
// binary the first time someone hits Start.
func helperExistsOnSystem() bool {
	exe, err := os.Executable()
	if err == nil {
		candidates := []string{
			filepath.Join(filepath.Dir(exe), "capture-helper"),
			filepath.Join(filepath.Dir(exe), "..", "share", "noto", "capture-helper"),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return true
			}
		}
	}
	// Fall back to swift in PATH (macOS dev workflow).
	if _, err := exec.LookPath("swift"); err == nil {
		return true
	}
	return false
}
