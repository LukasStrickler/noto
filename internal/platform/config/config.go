package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lukasstrickler/noto/internal/core/notoerr"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Config struct {
	SchemaVersion string                  `mapstructure:"schema_version"`
	ConfigDir     string                  `mapstructure:"config_dir"`
	ArtifactRoot  string                  `mapstructure:"artifact_root"`
	RecordingsDir string                  `mapstructure:"recordings_dir"`
	Providers     ProviderConfig          `mapstructure:"providers"`
	UI            UIConfig                `mapstructure:"ui"`
	Sync          SyncConfig              `mapstructure:"sync"`
	Storage       StorageConfig           `mapstructure:"storage"`
	Backend       BackendConfig           `mapstructure:"backend"`
	Compute       ComputeConfig           `mapstructure:"compute"`
	Routing       providers.RoutingPolicy `mapstructure:"routing"`
}

// BackendConfig declares which backend this process talks to. Mode "local"
// (the default) uses the in-process/UDS ladder — capture, compute, and storage
// all run here. Mode "remote" makes this a thin client: storage and compute
// live at Remote.URL and only finished audio uploads. This is configuration,
// not a second application — the same `noto serve` binary runs either side.
type BackendConfig struct {
	Mode   string              `mapstructure:"mode"` // "local" | "remote"
	Remote RemoteBackendConfig `mapstructure:"remote"`
}

// RemoteBackendConfig points a thin client at a remote backend. TokenRef is a
// secrets-store reference (e.g. "backend:remote"), NOT the bearer token itself
// — the token follows the same Keychain/credentials.json path as provider keys
// and is never written to config.yaml.
type RemoteBackendConfig struct {
	URL      string `mapstructure:"url"`
	TokenRef string `mapstructure:"token_ref"`
}

// IsRemote reports whether this process should act as a thin client.
func (b BackendConfig) IsRemote() bool {
	return strings.EqualFold(strings.TrimSpace(b.Mode), "remote") && strings.TrimSpace(b.Remote.URL) != ""
}

// Compute location values. A route is "local" (run the capability in this
// backend process on the resolved accelerator) or "remote" (offload it to a
// noto compute endpoint over HTTP).
const (
	ComputeLocationLocal  = "local"
	ComputeLocationRemote = "remote"
)

// ComputeConfig declares WHERE each heavy compute capability runs, independent
// of WHICH provider/model performs it (that is Routing) and of the accelerator
// it runs on (that is resolved at startup). Default location is "local". A
// capability set to "remote" streams its audio to a noto compute endpoint — a
// `noto serve` GPU box or a serverless function — rather than running it here.
// The three routes resolve independently, so STT can run on a remote GPU while
// diarization and identity stay local (the recommended posture: voice prints
// are cheap to compute and the most sensitive data, so they stay on-device by
// default).
type ComputeConfig struct {
	Provider string `mapstructure:"provider"`
	// Endpoint is the default compute backend inherited by any route whose
	// location is "remote" but which sets no URL of its own — so a single
	// endpoint + per-capability toggles covers the common case.
	Endpoint ComputeEndpoint    `mapstructure:"endpoint"`
	Speech   ComputeRoute       `mapstructure:"speech"`
	Diarize  ComputeRoute       `mapstructure:"diarize"`
	Embed    ComputeRoute       `mapstructure:"embed"`
	Modal    ModalComputeConfig `mapstructure:"modal"`
	VAD      VADConfig          `mapstructure:"vad"`
	// JobConcurrency caps how many pipeline jobs (meetings) the worker pool runs
	// at once. 0 = auto: a small pool when compute is LOCAL (each worker drives
	// heavy in-process STT/diar models, so more would oversubscribe one machine),
	// and a larger pool when STT+diar are OFFLOADED to a remote GPU (workers then
	// block on a network POST, so the pool should feed the GPU at its measured
	// batch optimum — jobs=10 on L40S — instead of capping hosted throughput at
	// the local default). See JobWorkers for the resolution.
	JobConcurrency int `mapstructure:"job_concurrency"`
}

const (
	// DefaultLocalJobWorkers is the worker-pool size when compute runs in-process:
	// each worker drives heavy local STT/diar models, so the pool stays small to
	// avoid oversubscribing one machine.
	DefaultLocalJobWorkers = 4
	// DefaultOffloadJobWorkers is the pool size when STT+diar are offloaded to a
	// remote GPU. Workers then block on a network POST, so the pool should feed the
	// GPU at its measured batch optimum (jobs=10 on L40S, the anchor winner's
	// contention point) instead of capping hosted throughput at the local default.
	DefaultOffloadJobWorkers = 10
)

// JobWorkers resolves the effective pipeline worker-pool size from JobConcurrency
// and the compute posture (see the JobConcurrency field doc). An explicit
// JobConcurrency > 0 always wins; otherwise offloaded STT+diar (network-bound
// workers) gets the GPU-batch optimum and local compute gets the small pool.
func (c ComputeConfig) JobWorkers() int {
	if c.JobConcurrency > 0 {
		return c.JobConcurrency
	}
	// "Offloaded" must mean the SAME thing Resolve uses to pick the remote provider
	// (location=remote AND a URL actually resolves), or the pool size drifts from
	// reality: a remote route with NO url runs the LOCAL heavy models, so sizing for
	// offload (10) would oversubscribe the box; and a case/whitespace-variant "Remote"
	// that Resolve DOES offload would otherwise get the small local pool and starve the
	// GPU. Both bugs vanish by asking Resolve, exactly like resolveSTTAdapter does.
	_, speechRemote := c.Resolve(c.Speech)
	_, diarRemote := c.Resolve(c.Diarize)
	if speechRemote && diarRemote {
		return DefaultOffloadJobWorkers
	}
	return DefaultLocalJobWorkers
}

// VADConfig enables Silero VAD silence-trimming before diarization on the GPU
// worker. Trimming silence shrinks the diar EMBEDDING work — the dominant GPU
// cost (~90% of a hosted-batch run) — without touching the speech: the server
// remaps turn times back to the original timeline, so transcript and identity
// artifacts are unchanged. Off by default; a guardrail-checked production opt-in
// (plan §0.8/B1). Settings are bridged to the worker through the NOTO_VAD* env the
// diar server already reads (vad_trim.env_cfg), so they apply to the local
// diarizer and any `noto serve` GPU box this process spawns. For a Modal-deployed
// worker, set the same env on the Modal app.
type VADConfig struct {
	Enabled       bool    `mapstructure:"enabled"`
	PadSeconds    float64 `mapstructure:"pad_seconds"`
	MinGapSeconds float64 `mapstructure:"min_gap_seconds"`
	Threshold     float64 `mapstructure:"threshold"`
}

// VADEnvKeys lists every NOTO_VAD* variable Env can set, so a caller that toggles
// VAD off at runtime can unset exactly the keys this config owns (and nothing
// else) before re-applying — keeping the env key names defined in one place.
func VADEnvKeys() []string {
	return []string{"NOTO_VAD", "NOTO_VAD_PAD", "NOTO_VAD_MIN_GAP", "NOTO_VAD_THRESHOLD"}
}

// Env renders the VAD config as the NOTO_VAD* environment the diar server reads.
// Empty when disabled; a parameter is emitted only when explicitly set (> 0),
// leaving the server's own default in place otherwise.
func (v VADConfig) Env() []string {
	if !v.Enabled {
		return nil
	}
	env := []string{"NOTO_VAD=1"}
	if v.PadSeconds > 0 {
		env = append(env, fmt.Sprintf("NOTO_VAD_PAD=%g", v.PadSeconds))
	}
	if v.MinGapSeconds > 0 {
		env = append(env, fmt.Sprintf("NOTO_VAD_MIN_GAP=%g", v.MinGapSeconds))
	}
	if v.Threshold > 0 {
		env = append(env, fmt.Sprintf("NOTO_VAD_THRESHOLD=%g", v.Threshold))
	}
	return env
}

// ModalComputeConfig stores the user-owned Modal compute deployment metadata.
// It intentionally stores only control-plane identifiers and endpoint/token
// references. User audio defaults to request/response or ephemeral Volume
// staging; named Volume staging is an explicit opt-in that must be cleaned.
type ModalComputeConfig struct {
	AppName                string `mapstructure:"app_name"`
	Environment            string `mapstructure:"environment"`
	GPU                    string `mapstructure:"gpu"`
	EndpointURL            string `mapstructure:"endpoint_url"`
	TokenRef               string `mapstructure:"token_ref"`
	EndpointTokenRef       string `mapstructure:"endpoint_token_ref"`
	DeploymentVersion      string `mapstructure:"deployment_version"`
	ModelVolume            string `mapstructure:"model_volume"`
	BenchmarkVolume        string `mapstructure:"benchmark_volume"`
	UserTransferMode       string `mapstructure:"user_transfer_mode"`      // http | temporary | permanent
	BenchmarkTransferMode  string `mapstructure:"benchmark_transfer_mode"` // http | temporary | permanent
	TemporaryThresholdMB   int    `mapstructure:"temporary_threshold_mb"`
	ScaledownWindowSeconds int    `mapstructure:"scaledown_window_seconds"`
	MinContainers          int    `mapstructure:"min_containers"`
	ModelCache             string `mapstructure:"model_cache"` // volume | image
}

// ComputeEndpoint is a remote compute backend URL plus a secrets-store
// reference to its bearer token (never the token itself, mirroring Backend).
type ComputeEndpoint struct {
	URL      string `mapstructure:"url"`
	TokenRef string `mapstructure:"token_ref"`
}

// ComputeRoute places one capability. Location "" or "local" runs it in
// process; "remote" offloads it to URL, or to ComputeConfig.Endpoint when URL
// is empty.
type ComputeRoute struct {
	Location string `mapstructure:"location"`
	URL      string `mapstructure:"url"`
	TokenRef string `mapstructure:"token_ref"`
}

// resolve returns the effective endpoint for a route, inheriting the default
// endpoint when the route sets no URL of its own.
func (r ComputeRoute) resolve(def ComputeEndpoint) ComputeEndpoint {
	e := ComputeEndpoint{URL: strings.TrimSpace(r.URL), TokenRef: strings.TrimSpace(r.TokenRef)}
	if e.URL == "" {
		e.URL = strings.TrimSpace(def.URL)
		if e.TokenRef == "" {
			e.TokenRef = strings.TrimSpace(def.TokenRef)
		}
	}
	return e
}

// Resolve reports the effective endpoint for a route and whether that route is
// actually remote (location=remote AND a non-empty resolved URL). Callers pass
// one of the three routes, e.g. cfg.Compute.Resolve(cfg.Compute.Speech).
func (c ComputeConfig) Resolve(r ComputeRoute) (ComputeEndpoint, bool) {
	ep := r.resolve(c.Endpoint)
	remote := strings.EqualFold(strings.TrimSpace(r.Location), ComputeLocationRemote) && ep.URL != ""
	return ep, remote
}

// AnyRemote reports whether any capability is offloaded — used to label the
// topology ("compute: remote") without inspecting each route.
func (c ComputeConfig) AnyRemote() bool {
	for _, r := range []ComputeRoute{c.Speech, c.Diarize, c.Embed} {
		if _, remote := c.Resolve(r); remote {
			return true
		}
	}
	return false
}

type ProviderConfig struct {
	STT        STTConfig `mapstructure:"stt"`
	LLM        LLMConfig `mapstructure:"llm"`
	Summarizer string    `mapstructure:"summarizer"`
}

type STTConfig struct {
	Default string `mapstructure:"default"`
}

type LLMConfig struct {
	Default string `mapstructure:"default"`
}

type UIConfig struct {
	Theme        string `mapstructure:"theme"`
	SidebarWidth int    `mapstructure:"sidebar_width"`
}

type SyncConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Endpoint string `mapstructure:"endpoint"`
	Bucket   string `mapstructure:"bucket"`
}

type StorageConfig struct {
	Type   string              `mapstructure:"type"`
	Local  LocalStorageConfig  `mapstructure:"local"`
	S3     S3StorageConfig     `mapstructure:"s3"`
	Remote RemoteStorageConfig `mapstructure:"remote"`
}

// RemoteStorageConfig points the artifact store at a remote noto data plane:
// transcripts, summaries, and meeting metadata are written through to URL (the
// source of truth) over the /v1/repo/* API. This is the "compute local, store
// off-site" knob — the pipeline still runs here, but its output lands on a
// remote backend. Audio stays on this device by default (a privacy win and far
// less traffic than shipping every recording up). TokenRef is a secrets-store
// reference, never the token itself.
type RemoteStorageConfig struct {
	URL      string `mapstructure:"url"`
	TokenRef string `mapstructure:"token_ref"`
}

// IsRemote reports whether artifacts should be written through to a remote data
// plane rather than the local filesystem.
func (c StorageConfig) IsRemote() bool {
	return strings.EqualFold(strings.TrimSpace(c.Type), "remote") && strings.TrimSpace(c.Remote.URL) != ""
}

type LocalStorageConfig struct {
	Path string `mapstructure:"path"`
}

type S3StorageConfig struct {
	Bucket   string `mapstructure:"bucket"`
	Region   string `mapstructure:"region"`
	Endpoint string `mapstructure:"endpoint"`
}

type Store struct {
	dir string
}

func NewStore(dir string) Store {
	if dir == "" {
		dir = DefaultConfigDir()
	}
	return Store{dir: dir}
}

func (s Store) Dir() string {
	return s.dir
}

func (s Store) Path() string {
	return filepath.Join(s.dir, "config.yaml")
}

// Save writes the config back to s.dir. Convenience wrapper around the
// package-level Save so service code can hold a single Store value.
func (s Store) Save(cfg Config) error {
	return Save(cfg, s.dir)
}

// Load reads the config from s.dir; if the file does not exist it returns
// DefaultConfig() with ConfigDir set.
func (s Store) Load() (Config, error) {
	return Load(s.dir)
}

// envKeyReplacer maps Viper dot-separated config keys
// ("noto.providers.stt.default") to underscore env vars
// (NOTO_PROVIDERS_STT_DEFAULT).
var envKeyReplacer = strings.NewReplacer(".", "_")

func NewViper(cfgDir string) (*viper.Viper, error) {
	v := viper.New()

	v.SetConfigName(ConfigFileName())
	v.SetConfigType(ConfigFileExt())
	v.AddConfigPath(cfgDir)
	v.AddConfigPath(".")

	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(envKeyReplacer)

	v.AutomaticEnv()

	// Explicit env bindings: the convention is NOTO_<KEY_PATH> without
	// the internal "noto." namespace prefix (which is a config-file
	// nesting detail, not something callers should have to repeat).
	// AutomaticEnv handles the rest by trying NOTO_<viper_key_uppercased>.
	bindEnv := func(viperKey, envSuffix string) {
		_ = v.BindEnv(viperKey, EnvPrefix+"_"+envSuffix)
	}
	bindEnv(KeyRecordingsDir, "RECORDINGS_DIR")
	bindEnv(KeyArtifactRoot, "ARTIFACT_ROOT")
	bindEnv(KeyConfigDir, "CONFIG_DIR")
	bindEnv(KeySTTDefault, "PROVIDERS_STT_DEFAULT")
	bindEnv(KeyLLMDefault, "PROVIDERS_LLM_DEFAULT")
	bindEnv(KeyRoutingLLMModel, "ROUTING_LLM_MODEL")
	bindEnv(KeySummarizer, "PROVIDERS_SUMMARIZER")
	bindEnv(KeyUITheme, "UI_THEME")
	bindEnv(KeyStorageType, "STORAGE_TYPE")
	bindEnv(KeyBackendMode, "BACKEND_MODE")
	bindEnv(KeyBackendRemoteURL, "BACKEND_REMOTE_URL")
	bindEnv(KeyBackendRemoteTokenRef, "BACKEND_REMOTE_TOKEN_REF")
	bindEnv(KeyComputeEndpointURL, "COMPUTE_ENDPOINT_URL")
	bindEnv(KeyComputeEndpointTokenRef, "COMPUTE_ENDPOINT_TOKEN_REF")
	bindEnv(KeyComputeProvider, "COMPUTE_PROVIDER")
	bindEnv(KeyComputeSpeechLocation, "COMPUTE_SPEECH_LOCATION")
	bindEnv(KeyComputeSpeechURL, "COMPUTE_SPEECH_URL")
	bindEnv(KeyComputeDiarizeLocation, "COMPUTE_DIARIZE_LOCATION")
	bindEnv(KeyComputeEmbedLocation, "COMPUTE_EMBED_LOCATION")
	bindEnv(KeyComputeModalAppName, "MODAL_APP_NAME")
	bindEnv(KeyComputeModalEnvironment, "MODAL_ENVIRONMENT")
	bindEnv(KeyComputeModalGPU, "MODAL_GPU")
	bindEnv(KeyComputeModalEndpointURL, "MODAL_ENDPOINT_URL")
	bindEnv(KeyComputeModalTokenRef, "MODAL_TOKEN_REF")
	bindEnv(KeyComputeModalEndpointTokenRef, "MODAL_ENDPOINT_TOKEN_REF")
	bindEnv(KeyComputeModalDeploymentVersion, "MODAL_DEPLOYMENT_VERSION")
	bindEnv(KeyComputeModalModelVolume, "MODAL_MODEL_VOLUME")
	bindEnv(KeyComputeModalBenchmarkVolume, "MODAL_BENCHMARK_VOLUME")
	bindEnv(KeyComputeModalUserTransferMode, "MODAL_USER_TRANSFER_MODE")
	bindEnv(KeyComputeModalBenchmarkTransferMode, "MODAL_BENCHMARK_TRANSFER_MODE")
	bindEnv(KeyComputeModalTemporaryThresholdMB, "MODAL_TEMPORARY_THRESHOLD_MB")
	bindEnv(KeyComputeModalScaledownWindow, "MODAL_SCALEDOWN_WINDOW_SECONDS")
	bindEnv(KeyComputeModalMinContainers, "MODAL_MIN_CONTAINERS")
	bindEnv(KeyComputeModalModelCache, "MODAL_MODEL_CACHE")
	bindEnv(KeyStorageRemoteURL, "STORAGE_REMOTE_URL")
	bindEnv(KeyStorageRemoteTokenRef, "STORAGE_REMOTE_TOKEN_REF")

	for key, val := range AllDefaults() {
		v.SetDefault(key, val)
	}

	return v, nil
}

func Load(cfgDir string) (Config, error) {
	if cfgDir == "" {
		cfgDir = DefaultConfigDir()
	}

	v, err := NewViper(cfgDir)
	if err != nil {
		return Config{}, notoerr.Wrap("viper_init_failed", "Failed to initialize Viper", err)
	}

	cfg := Config{}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			cfg = DefaultConfig()
		} else {
			return Config{}, notoerr.Wrap("config_read_failed", "Failed to read config file", err)
		}
	}

	if err := unmarshalNoto(v, &cfg); err != nil {
		return Config{}, notoerr.Wrap("config_unmarshal_failed", "Failed to unmarshal config", err)
	}

	cfg.ConfigDir = cfgDir

	return cfg, nil
}

// unmarshalNoto unmarshals the "noto.*" subtree into the Config struct.
// All keys are stored under the top-level "noto" namespace, so we wrap
// the destination in a struct that mirrors that nesting. After the
// bulk unmarshal we re-pull a few well-known keys via v.GetString so
// env-bound and pflag-bound values that AllSettings doesn't surface
// still win.
func unmarshalNoto(v *viper.Viper, cfg *Config) error {
	var wrap struct {
		Noto Config `mapstructure:"noto"`
	}
	if err := v.Unmarshal(&wrap); err != nil {
		return err
	}
	*cfg = wrap.Noto

	// viper.BindEnv / BindPFlag bindings don't appear in AllSettings(),
	// so Unmarshal misses them. Pull the ones we care about back in:
	if s := v.GetString(KeyRecordingsDir); s != "" {
		cfg.RecordingsDir = s
	}
	if s := v.GetString(KeyArtifactRoot); s != "" {
		cfg.ArtifactRoot = s
	}
	if s := v.GetString(KeyConfigDir); s != "" {
		cfg.ConfigDir = s
	}
	if s := v.GetString(KeySTTDefault); s != "" {
		cfg.Providers.STT.Default = s
	}
	if s := v.GetString(KeyLLMDefault); s != "" {
		cfg.Providers.LLM.Default = s
	}
	if s := v.GetString(KeyRoutingLLMModel); s != "" {
		cfg.Routing.LLMModel = s
	}
	if s := v.GetString(KeyRoutingSpeechProvider); s != "" {
		cfg.Routing.SpeechProvider = s
	}
	// Privacy bools default ON; IsSet keeps a file/env value (including an
	// explicit false) from being masked, while an unset key leaves the
	// mapstructure-unmarshalled value in place.
	if v.IsSet(KeyRoutingPrivacyZDR) {
		cfg.Routing.LLMPrivacy.ZDR = v.GetBool(KeyRoutingPrivacyZDR)
	}
	if v.IsSet(KeyRoutingPrivacyDenyDataColl) {
		cfg.Routing.LLMPrivacy.DenyDataCollection = v.GetBool(KeyRoutingPrivacyDenyDataColl)
	}
	if v.IsSet(KeyRoutingPrivacyRequireParameters) {
		cfg.Routing.LLMPrivacy.RequireParameters = v.GetBool(KeyRoutingPrivacyRequireParameters)
	}
	if s := v.GetString(KeyUITheme); s != "" {
		cfg.UI.Theme = s
	}
	if n := v.GetInt(KeyUISidebarWidth); n > 0 {
		cfg.UI.SidebarWidth = n
	}
	if s := v.GetString(KeySummarizer); s != "" {
		cfg.Providers.Summarizer = s
	}
	// Sync + storage keys are pflag-bound (and storage.type is also
	// env-bound), so AllSettings misses them when supplied via flag/env.
	// Re-pull each one or a `--sync-endpoint`/`--storage-local-path` flag
	// would be silently dropped. IsSet keeps the bool flag from clobbering
	// a config-file value with its zero default.
	if v.IsSet(KeySyncEnabled) {
		cfg.Sync.Enabled = v.GetBool(KeySyncEnabled)
	}
	if s := v.GetString(KeySyncEndpoint); s != "" {
		cfg.Sync.Endpoint = s
	}
	if s := v.GetString(KeySyncBucket); s != "" {
		cfg.Sync.Bucket = s
	}
	if s := v.GetString(KeyStorageType); s != "" {
		cfg.Storage.Type = s
	}
	if s := v.GetString(KeyStorageLocalPath); s != "" {
		cfg.Storage.Local.Path = s
	}
	if s := v.GetString(KeyStorageS3Bucket); s != "" {
		cfg.Storage.S3.Bucket = s
	}
	if s := v.GetString(KeyStorageS3Region); s != "" {
		cfg.Storage.S3.Region = s
	}
	if s := v.GetString(KeyStorageS3Endpoint); s != "" {
		cfg.Storage.S3.Endpoint = s
	}
	if s := v.GetString(KeyStorageRemoteURL); s != "" {
		cfg.Storage.Remote.URL = s
	}
	if s := v.GetString(KeyStorageRemoteTokenRef); s != "" {
		cfg.Storage.Remote.TokenRef = s
	}
	// Backend keys are env-bound, so AllSettings misses them when supplied via env.
	if s := v.GetString(KeyBackendMode); s != "" {
		cfg.Backend.Mode = s
	}
	if s := v.GetString(KeyBackendRemoteURL); s != "" {
		cfg.Backend.Remote.URL = s
	}
	if s := v.GetString(KeyBackendRemoteTokenRef); s != "" {
		cfg.Backend.Remote.TokenRef = s
	}
	// Compute keys are env-bound, so AllSettings misses them when supplied via env.
	if s := v.GetString(KeyComputeProvider); s != "" {
		cfg.Compute.Provider = s
	}
	if s := v.GetString(KeyComputeEndpointURL); s != "" {
		cfg.Compute.Endpoint.URL = s
	}
	if s := v.GetString(KeyComputeEndpointTokenRef); s != "" {
		cfg.Compute.Endpoint.TokenRef = s
	}
	if s := v.GetString(KeyComputeSpeechLocation); s != "" {
		cfg.Compute.Speech.Location = s
	}
	if s := v.GetString(KeyComputeSpeechURL); s != "" {
		cfg.Compute.Speech.URL = s
	}
	if s := v.GetString(KeyComputeDiarizeLocation); s != "" {
		cfg.Compute.Diarize.Location = s
	}
	if s := v.GetString(KeyComputeEmbedLocation); s != "" {
		cfg.Compute.Embed.Location = s
	}
	if s := v.GetString(KeyComputeModalAppName); s != "" {
		cfg.Compute.Modal.AppName = s
	}
	if s := v.GetString(KeyComputeModalEnvironment); s != "" {
		cfg.Compute.Modal.Environment = s
	}
	if s := v.GetString(KeyComputeModalGPU); s != "" {
		cfg.Compute.Modal.GPU = s
	}
	if s := v.GetString(KeyComputeModalEndpointURL); s != "" {
		cfg.Compute.Modal.EndpointURL = s
	}
	if s := v.GetString(KeyComputeModalTokenRef); s != "" {
		cfg.Compute.Modal.TokenRef = s
	}
	if s := v.GetString(KeyComputeModalEndpointTokenRef); s != "" {
		cfg.Compute.Modal.EndpointTokenRef = s
	}
	if s := v.GetString(KeyComputeModalDeploymentVersion); s != "" {
		cfg.Compute.Modal.DeploymentVersion = s
	}
	if s := v.GetString(KeyComputeModalModelVolume); s != "" {
		cfg.Compute.Modal.ModelVolume = s
	}
	if s := v.GetString(KeyComputeModalBenchmarkVolume); s != "" {
		cfg.Compute.Modal.BenchmarkVolume = s
	}
	if s := v.GetString(KeyComputeModalUserTransferMode); s != "" {
		cfg.Compute.Modal.UserTransferMode = s
	}
	if s := v.GetString(KeyComputeModalBenchmarkTransferMode); s != "" {
		cfg.Compute.Modal.BenchmarkTransferMode = s
	}
	if n := v.GetInt(KeyComputeModalTemporaryThresholdMB); n > 0 {
		cfg.Compute.Modal.TemporaryThresholdMB = n
	}
	if n := v.GetInt(KeyComputeModalScaledownWindow); n >= 0 {
		cfg.Compute.Modal.ScaledownWindowSeconds = n
	}
	if n := v.GetInt(KeyComputeModalMinContainers); n >= 0 {
		cfg.Compute.Modal.MinContainers = n
	}
	if s := v.GetString(KeyComputeModalModelCache); s != "" {
		cfg.Compute.Modal.ModelCache = s
	}
	cfg.Compute.VAD.Enabled = v.GetBool(KeyComputeVADEnabled)
	if f := v.GetFloat64(KeyComputeVADPad); f > 0 {
		cfg.Compute.VAD.PadSeconds = f
	}
	if f := v.GetFloat64(KeyComputeVADMinGap); f > 0 {
		cfg.Compute.VAD.MinGapSeconds = f
	}
	if f := v.GetFloat64(KeyComputeVADThreshold); f > 0 {
		cfg.Compute.VAD.Threshold = f
	}
	return nil
}

func LoadWithFlags(cfgDir string, flags *pflag.FlagSet) (Config, error) {
	if cfgDir == "" {
		cfgDir = DefaultConfigDir()
	}

	v, err := NewViper(cfgDir)
	if err != nil {
		return Config{}, notoerr.Wrap("viper_init_failed", "Failed to initialize Viper", err)
	}

	BindFlags(flags, v)

	cfg := DefaultConfig()

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return Config{}, notoerr.Wrap("config_read_failed", "Failed to read config file", err)
		}
	}

	if err := unmarshalNoto(v, &cfg); err != nil {
		return Config{}, notoerr.Wrap("config_unmarshal_failed", "Failed to unmarshal config", err)
	}

	cfg.ConfigDir = cfgDir

	return cfg, nil
}

func Save(cfg Config, dir string) error {
	if dir == "" {
		dir = DefaultConfigDir()
	}

	if err := os.MkdirAll(dir, ConfigDirMode); err != nil {
		return notoerr.Wrap("config_dir_create_failed", "Could not create config directory", err)
	}
	if err := os.Chmod(dir, ConfigDirMode); err != nil {
		return notoerr.Wrap("config_dir_perm_failed", "Could not set config directory permissions", err)
	}

	v := viper.New()
	v.SetConfigName(ConfigFileName())
	v.SetConfigType(ConfigFileExt())
	v.AddConfigPath(dir)
	v.AddConfigPath(".")

	cfg.ConfigDir = dir
	cfg.SchemaVersion = "config.v1"

	for key, val := range AllDefaults() {
		v.SetDefault(key, val)
	}

	v.Set(KeySchemaVersion, cfg.SchemaVersion)
	v.Set(KeyConfigDir, cfg.ConfigDir)
	v.Set(KeyArtifactRoot, cfg.ArtifactRoot)
	v.Set(KeyRecordingsDir, cfg.RecordingsDir)
	v.Set(KeySTTDefault, cfg.Providers.STT.Default)
	v.Set(KeyLLMDefault, cfg.Providers.LLM.Default)
	v.Set(KeySummarizer, cfg.Providers.Summarizer)
	v.Set(KeyUITheme, cfg.UI.Theme)
	v.Set(KeyUISidebarWidth, cfg.UI.SidebarWidth)
	v.Set(KeySyncEnabled, cfg.Sync.Enabled)
	v.Set(KeySyncEndpoint, cfg.Sync.Endpoint)
	v.Set(KeySyncBucket, cfg.Sync.Bucket)
	v.Set(KeyStorageType, cfg.Storage.Type)
	v.Set(KeyStorageLocalPath, cfg.Storage.Local.Path)
	v.Set(KeyStorageS3Bucket, cfg.Storage.S3.Bucket)
	v.Set(KeyStorageS3Region, cfg.Storage.S3.Region)
	v.Set(KeyStorageS3Endpoint, cfg.Storage.S3.Endpoint)
	v.Set(KeyStorageRemoteURL, cfg.Storage.Remote.URL)
	v.Set(KeyStorageRemoteTokenRef, cfg.Storage.Remote.TokenRef)
	v.Set(KeyBackendMode, cfg.Backend.Mode)
	v.Set(KeyBackendRemoteURL, cfg.Backend.Remote.URL)
	v.Set(KeyBackendRemoteTokenRef, cfg.Backend.Remote.TokenRef)
	v.Set(KeyComputeProvider, cfg.Compute.Provider)
	v.Set(KeyComputeEndpointURL, cfg.Compute.Endpoint.URL)
	v.Set(KeyComputeEndpointTokenRef, cfg.Compute.Endpoint.TokenRef)
	v.Set(KeyComputeSpeechLocation, cfg.Compute.Speech.Location)
	v.Set(KeyComputeSpeechURL, cfg.Compute.Speech.URL)
	v.Set(KeyComputeSpeechTokenRef, cfg.Compute.Speech.TokenRef)
	v.Set(KeyComputeDiarizeLocation, cfg.Compute.Diarize.Location)
	v.Set(KeyComputeDiarizeURL, cfg.Compute.Diarize.URL)
	v.Set(KeyComputeDiarizeTokenRef, cfg.Compute.Diarize.TokenRef)
	v.Set(KeyComputeEmbedLocation, cfg.Compute.Embed.Location)
	v.Set(KeyComputeEmbedURL, cfg.Compute.Embed.URL)
	v.Set(KeyComputeEmbedTokenRef, cfg.Compute.Embed.TokenRef)
	v.Set(KeyComputeModalAppName, cfg.Compute.Modal.AppName)
	v.Set(KeyComputeModalEnvironment, cfg.Compute.Modal.Environment)
	v.Set(KeyComputeModalGPU, cfg.Compute.Modal.GPU)
	v.Set(KeyComputeModalEndpointURL, cfg.Compute.Modal.EndpointURL)
	v.Set(KeyComputeModalTokenRef, cfg.Compute.Modal.TokenRef)
	v.Set(KeyComputeModalEndpointTokenRef, cfg.Compute.Modal.EndpointTokenRef)
	v.Set(KeyComputeModalDeploymentVersion, cfg.Compute.Modal.DeploymentVersion)
	v.Set(KeyComputeModalModelVolume, cfg.Compute.Modal.ModelVolume)
	v.Set(KeyComputeModalBenchmarkVolume, cfg.Compute.Modal.BenchmarkVolume)
	v.Set(KeyComputeModalUserTransferMode, cfg.Compute.Modal.UserTransferMode)
	v.Set(KeyComputeModalBenchmarkTransferMode, cfg.Compute.Modal.BenchmarkTransferMode)
	v.Set(KeyComputeModalTemporaryThresholdMB, cfg.Compute.Modal.TemporaryThresholdMB)
	v.Set(KeyComputeModalScaledownWindow, cfg.Compute.Modal.ScaledownWindowSeconds)
	v.Set(KeyComputeModalMinContainers, cfg.Compute.Modal.MinContainers)
	v.Set(KeyComputeModalModelCache, cfg.Compute.Modal.ModelCache)
	v.Set(KeyComputeVADEnabled, cfg.Compute.VAD.Enabled)
	v.Set(KeyComputeVADPad, cfg.Compute.VAD.PadSeconds)
	v.Set(KeyComputeVADMinGap, cfg.Compute.VAD.MinGapSeconds)
	v.Set(KeyComputeVADThreshold, cfg.Compute.VAD.Threshold)
	v.Set(KeyRoutingLLMProvider, cfg.Routing.LLMProvider)
	v.Set(KeyRoutingLLMModel, cfg.Routing.LLMModel)
	v.Set(KeyRoutingSpeechProvider, cfg.Routing.SpeechProvider)
	v.Set(KeyRoutingProfile, string(cfg.Routing.Profile))
	v.Set(KeyRoutingPrivacyZDR, cfg.Routing.LLMPrivacy.ZDR)
	v.Set(KeyRoutingPrivacyDenyDataColl, cfg.Routing.LLMPrivacy.DenyDataCollection)
	v.Set(KeyRoutingPrivacyRequireParameters, cfg.Routing.LLMPrivacy.RequireParameters)

	// Write atomically: a crash or disk-full mid-write must never leave a
	// truncated config.yaml. viper.WriteConfigAs infers the marshaler from
	// the file extension, so the temp file keeps a .yaml suffix; the final
	// os.Rename is atomic on POSIX.
	final := filepath.Join(dir, "config.yaml")
	tmp, err := os.CreateTemp(dir, "config-*.yaml")
	if err != nil {
		return notoerr.Wrap("config_write_failed", "Failed to create temp config file", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close() // WriteConfigAs reopens and rewrites the file.
	defer os.Remove(tmpPath)

	if err := v.WriteConfigAs(tmpPath); err != nil {
		return notoerr.Wrap("config_write_failed", "Failed to write config file", err)
	}
	if err := os.Chmod(tmpPath, ConfigFileMode); err != nil {
		return notoerr.Wrap("config_file_perm_failed", "Could not set config file permissions", err)
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return notoerr.Wrap("config_write_failed", "Failed to finalize config file", err)
	}

	return nil
}

func DefaultConfig() Config {
	cfgDir := DefaultConfigDir()
	artifactRoot := DefaultArtifactRoot()
	routing := providers.DefaultRoutingPolicy()

	return Config{
		SchemaVersion: "config.v1",
		ConfigDir:     cfgDir,
		ArtifactRoot:  artifactRoot,
		RecordingsDir: DefaultRecordingsDir(),
		Providers: ProviderConfig{
			STT: STTConfig{
				Default: DefaultSTTProvider,
			},
			LLM: LLMConfig{
				Default: DefaultLLMProvider,
			},
			Summarizer: DefaultSummarizer,
		},
		UI: UIConfig{
			Theme: DefaultUITheme,
		},
		Sync: SyncConfig{
			Enabled:  DefaultSyncEnabled,
			Endpoint: "",
			Bucket:   "",
		},
		Storage: StorageConfig{
			Type: DefaultStorageType,
			Local: LocalStorageConfig{
				Path: artifactRoot,
			},
			S3: S3StorageConfig{
				Bucket:   "",
				Region:   "",
				Endpoint: "",
			},
		},
		Backend: BackendConfig{
			Mode: DefaultBackendMode,
		},
		Compute: ComputeConfig{
			Provider: "",
			Speech:   ComputeRoute{Location: DefaultComputeLocation},
			Diarize:  ComputeRoute{Location: DefaultComputeLocation},
			Embed:    ComputeRoute{Location: DefaultComputeLocation},
			Modal: ModalComputeConfig{
				AppName:                DefaultModalAppName,
				Environment:            DefaultModalEnvironment,
				GPU:                    DefaultModalGPU,
				TokenRef:               ModalTokenRef,
				EndpointTokenRef:       ModalEndpointTokenRef,
				ModelVolume:            DefaultModalModelVolume,
				BenchmarkVolume:        DefaultModalBenchmarkVolume,
				UserTransferMode:       DefaultModalUserTransferMode,
				BenchmarkTransferMode:  DefaultModalBenchmarkTransferMode,
				TemporaryThresholdMB:   DefaultModalTemporaryThresholdMB,
				ScaledownWindowSeconds: DefaultModalScaledownWindow,
				MinContainers:          DefaultModalMinContainers,
				ModelCache:             DefaultModalModelCache,
			},
		},
		Routing: routing,
	}
}

func (c Config) GetProviderConfig(provider string) ProviderSettings {
	return ProviderSettings{
		Provider:  provider,
		APIKeyRef: fmt.Sprintf("provider:%s", provider),
	}
}

type ProviderSettings struct {
	Provider  string
	APIKeyRef string
	Endpoint  string
	Model     string
}

func (c Config) GetStorageBackend() string {
	return c.Storage.Type
}

func (c Config) GetSyncGateway() string {
	if c.Sync.Enabled {
		return c.Sync.Endpoint
	}
	return ""
}

func (c Config) GetRecordingsDir() string {
	if c.RecordingsDir != "" {
		return c.RecordingsDir
	}
	return filepath.Join(c.ArtifactRoot, DefaultRecordingsDirName)
}

func (c Config) GetArtifactRoot() string {
	return c.ArtifactRoot
}

func (c Config) GetConfigDir() string {
	return c.ConfigDir
}
