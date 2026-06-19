package config

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/lukasstrickler/noto/internal/platform/providers"
)

// Default configuration values using Viper-compatible dot notation keys.
const (
	// Config key prefixes
	KeyNoto          = "noto"
	KeyRecordingsDir = "noto.recordings_dir"
	KeyArtifactRoot  = "noto.artifact_root"
	KeyConfigDir     = "noto.config_dir"
	KeySchemaVersion = "noto.schema_version"

	// Provider keys
	KeyProviders  = "noto.providers"
	KeySTTDefault = "noto.providers.stt.default"
	KeyLLMDefault = "noto.providers.llm.default"
	KeySummarizer = "noto.providers.summarizer"

	// UI keys
	KeyUITheme        = "noto.ui.theme"
	KeyUISidebarWidth = "noto.ui.sidebar_width"

	// Sync keys
	KeySyncEnabled  = "noto.sync.enabled"
	KeySyncEndpoint = "noto.sync.endpoint"
	KeySyncBucket   = "noto.sync.bucket"

	// Backend keys — selects which backend this process talks to.
	// "local" = in-process/UDS ladder (capture+compute+storage here);
	// "remote" = thin client; storage+compute live at backend.remote.url.
	KeyBackendMode           = "noto.backend.mode"
	KeyBackendRemoteURL      = "noto.backend.remote.url"
	KeyBackendRemoteTokenRef = "noto.backend.remote.token_ref"

	// Compute keys — WHERE each heavy capability runs (local|remote). A single
	// endpoint plus per-capability location toggles keeps the surface small.
	KeyComputeEndpointURL                = "noto.compute.endpoint.url"
	KeyComputeEndpointTokenRef           = "noto.compute.endpoint.token_ref"
	KeyComputeSpeechLocation             = "noto.compute.speech.location"
	KeyComputeSpeechURL                  = "noto.compute.speech.url"
	KeyComputeSpeechTokenRef             = "noto.compute.speech.token_ref"
	KeyComputeDiarizeLocation            = "noto.compute.diarize.location"
	KeyComputeDiarizeURL                 = "noto.compute.diarize.url"
	KeyComputeDiarizeTokenRef            = "noto.compute.diarize.token_ref"
	KeyComputeEmbedLocation              = "noto.compute.embed.location"
	KeyComputeEmbedURL                   = "noto.compute.embed.url"
	KeyComputeEmbedTokenRef              = "noto.compute.embed.token_ref"
	KeyComputeProvider                   = "noto.compute.provider"
	KeyComputeModalAppName               = "noto.compute.modal.app_name"
	KeyComputeModalEnvironment           = "noto.compute.modal.environment"
	KeyComputeModalGPU                   = "noto.compute.modal.gpu"
	KeyComputeModalEndpointURL           = "noto.compute.modal.endpoint_url"
	KeyComputeModalTokenRef              = "noto.compute.modal.token_ref"
	KeyComputeModalEndpointTokenRef      = "noto.compute.modal.endpoint_token_ref"
	KeyComputeModalDeploymentVersion     = "noto.compute.modal.deployment_version"
	KeyComputeModalModelVolume           = "noto.compute.modal.model_volume"
	KeyComputeModalBenchmarkVolume       = "noto.compute.modal.benchmark_volume"
	KeyComputeModalUserTransferMode      = "noto.compute.modal.user_transfer_mode"
	KeyComputeModalBenchmarkTransferMode = "noto.compute.modal.benchmark_transfer_mode"
	KeyComputeModalTemporaryThresholdMB  = "noto.compute.modal.temporary_threshold_mb"
	KeyComputeModalScaledownWindow       = "noto.compute.modal.scaledown_window_seconds"
	KeyComputeModalMinContainers         = "noto.compute.modal.min_containers"
	KeyComputeModalModelCache            = "noto.compute.modal.model_cache"

	// Compute VAD keys — silence-trim before diarization (plan §0.8/B1).
	KeyComputeVADEnabled   = "noto.compute.vad.enabled"
	KeyComputeVADPad       = "noto.compute.vad.pad_seconds"
	KeyComputeVADMinGap    = "noto.compute.vad.min_gap_seconds"
	KeyComputeVADThreshold = "noto.compute.vad.threshold"

	// Storage keys
	KeyStorageType           = "noto.storage.type"
	KeyStorageLocalPath      = "noto.storage.local.path"
	KeyStorageS3Bucket       = "noto.storage.s3.bucket"
	KeyStorageS3Region       = "noto.storage.s3.region"
	KeyStorageS3Endpoint     = "noto.storage.s3.endpoint"
	KeyStorageRemoteURL      = "noto.storage.remote.url"
	KeyStorageRemoteTokenRef = "noto.storage.remote.token_ref"

	// Routing keys (from existing providers.RoutingPolicy)
	KeyRoutingLLMProvider    = "noto.routing.llm_provider"
	KeyRoutingLLMModel       = "noto.routing.llm_model"
	KeyRoutingSpeechProvider = "noto.routing.speech_provider"
	KeyRoutingProfile        = "noto.routing.profile"

	// LLM privacy (OpenRouter provider-routing) keys.
	KeyRoutingPrivacyZDR               = "noto.routing.llm_privacy.zdr"
	KeyRoutingPrivacyDenyDataColl      = "noto.routing.llm_privacy.deny_data_collection"
	KeyRoutingPrivacyRequireParameters = "noto.routing.llm_privacy.require_parameters"
)

// EnvConfigDir is the environment variable for overriding config directory.
const EnvConfigDir = "NOTO_CONFIG_DIR"

// EnvArtifactRoot is the environment variable for overriding artifact root.
const EnvArtifactRoot = "NOTO_ARTIFACT_ROOT"

// EnvPrefix is the environment variable prefix for Viper.
const EnvPrefix = "NOTO"

// Default values
const (
	DefaultRecordingsDirName          = "recordings"
	DefaultArtifactRootName           = "Noto"
	DefaultConfigDirName              = ".noto"
	DefaultSTTProvider                = "parakeet-local"
	DefaultLLMProvider                = "openrouter"
	DefaultLLMModel                   = "google/gemini-3.1-flash-preview"
	DefaultSummarizer                 = "openrouter"
	DefaultUITheme                    = "dark"
	DefaultUISidebarWidth             = 52
	DefaultSyncEnabled                = false
	DefaultStorageType                = "local"
	DefaultBackendMode                = "local"
	DefaultComputeLocation            = ComputeLocationLocal
	DefaultComputeProvider            = ""
	DefaultModalAppName               = "noto-compute"
	DefaultModalEnvironment           = "main"
	DefaultModalGPU                   = "L40S"
	DefaultModalModelVolume           = "noto-model-cache"
	DefaultModalBenchmarkVolume       = "noto-benchmark-cache"
	DefaultModalUserTransferMode      = "http"
	DefaultModalBenchmarkTransferMode = "permanent"
	DefaultModalTemporaryThresholdMB  = 128
	DefaultModalScaledownWindow       = 300
	DefaultModalMinContainers         = 0
	DefaultModalModelCache            = "volume"
)

const (
	ModalTokenRef         = "compute:modal"
	ModalEndpointTokenRef = "compute:modal-endpoint"
)

// ProviderEnvRefs maps a config provider ref to its env var.
var ProviderEnvRefs = map[string]string{
	"provider:openrouter": "NOTO_API_KEY_OPENROUTER",
}

// DefaultConfigDir returns the default config directory path. macOS uses the
// platform Application Support location; everywhere else (the remote Linux
// `noto serve` host, dev boxes) uses ~/.noto so a server doesn't write into a
// Mac-style path.
func DefaultConfigDir() string {
	if override := os.Getenv(EnvConfigDir); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return DefaultConfigDirName
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Noto")
	}
	return filepath.Join(home, DefaultConfigDirName)
}

// DefaultArtifactRoot returns the default artifact root path.
func DefaultArtifactRoot() string {
	if override := os.Getenv(EnvArtifactRoot); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return DefaultArtifactRootName
	}
	return filepath.Join(home, DefaultArtifactRootName)
}

// DefaultRecordingsDir returns the default recordings directory path.
func DefaultRecordingsDir() string {
	return filepath.Join(DefaultArtifactRoot(), DefaultRecordingsDirName)
}

// AppName returns the application name used by Viper.
func AppName() string {
	return "noto"
}

// ConfigFileName returns the config file name without extension.
func ConfigFileName() string {
	return "config"
}

// ConfigFileExt returns the config file extension.
func ConfigFileExt() string {
	return "yaml"
}

// ConfigDirMode returns the directory permission mode.
const ConfigDirMode = 0o700

// ConfigFileMode returns the file permission mode.
const ConfigFileMode = 0o600

// ProviderCredentialRefs returns the credential reference map.
func ProviderCredentialRefs() map[string]string {
	return map[string]string{
		"openrouter": "provider:openrouter",
	}
}

// ProviderDefaults returns the default provider configuration.
func ProviderDefaults() map[string]interface{} {
	return map[string]interface{}{
		"stt": map[string]string{
			"default": DefaultSTTProvider,
		},
		"llm": map[string]string{
			"default": DefaultLLMProvider,
		},
		"summarizer": DefaultSummarizer,
	}
}

// UIDefaults returns the default UI configuration.
func UIDefaults() map[string]interface{} {
	return map[string]interface{}{
		"theme":         DefaultUITheme,
		"sidebar_width": DefaultUISidebarWidth,
	}
}

// SyncDefaults returns the default sync configuration.
func SyncDefaults() map[string]interface{} {
	return map[string]interface{}{
		"enabled":  DefaultSyncEnabled,
		"endpoint": "",
		"bucket":   "",
	}
}

// StorageDefaults returns the default storage configuration.
func StorageDefaults() map[string]interface{} {
	return map[string]interface{}{
		"type": DefaultStorageType,
		"local": map[string]string{
			"path": DefaultArtifactRoot(),
		},
		"s3": map[string]string{
			"bucket":   "",
			"region":   "",
			"endpoint": "",
		},
		"remote": map[string]string{
			"url":       "",
			"token_ref": "",
		},
	}
}

// RoutingDefaults returns the default routing configuration.
func RoutingDefaults() providers.RoutingPolicy {
	return providers.DefaultRoutingPolicy()
}

// ComputeDefaults returns the default compute placement: every capability local,
// no remote endpoint configured.
func ComputeDefaults() map[string]interface{} {
	route := func() map[string]string {
		return map[string]string{"location": DefaultComputeLocation, "url": "", "token_ref": ""}
	}
	return map[string]interface{}{
		"provider": "",
		"endpoint": map[string]string{"url": "", "token_ref": ""},
		"speech":   route(),
		"diarize":  route(),
		"embed":    route(),
		"modal": map[string]interface{}{
			"app_name":                 DefaultModalAppName,
			"environment":              DefaultModalEnvironment,
			"gpu":                      DefaultModalGPU,
			"endpoint_url":             "",
			"token_ref":                ModalTokenRef,
			"endpoint_token_ref":       ModalEndpointTokenRef,
			"deployment_version":       "",
			"model_volume":             DefaultModalModelVolume,
			"benchmark_volume":         DefaultModalBenchmarkVolume,
			"user_transfer_mode":       DefaultModalUserTransferMode,
			"benchmark_transfer_mode":  DefaultModalBenchmarkTransferMode,
			"temporary_threshold_mb":   DefaultModalTemporaryThresholdMB,
			"scaledown_window_seconds": DefaultModalScaledownWindow,
			"min_containers":           DefaultModalMinContainers,
			"model_cache":              DefaultModalModelCache,
		},
	}
}

// AllDefaults returns a map of all default values for Viper initialization.
func AllDefaults() map[string]interface{} {
	return map[string]interface{}{
		KeyNoto: map[string]interface{}{
			"schema_version": "config.v1",
			"config_dir":     DefaultConfigDir(),
			"artifact_root":  DefaultArtifactRoot(),
			"recordings_dir": DefaultRecordingsDir(),
			"providers":      ProviderDefaults(),
			"ui":             UIDefaults(),
			"sync":           SyncDefaults(),
			"storage":        StorageDefaults(),
			"backend": map[string]interface{}{
				"mode": DefaultBackendMode,
				"remote": map[string]string{
					"url":       "",
					"token_ref": "",
				},
			},
			"compute": ComputeDefaults(),
			"routing": map[string]interface{}{
				"llm_provider":    DefaultLLMProvider,
				"llm_model":       DefaultLLMModel,
				"speech_provider": DefaultSTTProvider,
				"profile":         string(providers.RoutingProfileManual),
				"llm_privacy": map[string]interface{}{
					"zdr":                  providers.DefaultLLMPrivacy().ZDR,
					"deny_data_collection": providers.DefaultLLMPrivacy().DenyDataCollection,
					"require_parameters":   providers.DefaultLLMPrivacy().RequireParameters,
				},
			},
		},
	}
}
