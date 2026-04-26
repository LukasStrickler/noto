package config

import (
	"os"
	"path/filepath"

	"github.com/lukasstrickler/noto/internal/providers"
)

// Default configuration values using Viper-compatible dot notation keys.
const (
	// Config key prefixes
	KeyNoto            = "noto"
	KeyRecordingsDir   = "noto.recordings_dir"
	KeyArtifactRoot    = "noto.artifact_root"
	KeyConfigDir       = "noto.config_dir"
	KeySchemaVersion   = "noto.schema_version"

	// Provider keys
	KeyProviders     = "noto.providers"
	KeySTTDefault    = "noto.providers.stt.default"
	KeyLLMDefault    = "noto.providers.llm.default"
	KeySummarizer    = "noto.providers.summarizer"

	// UI keys
	KeyUITheme = "noto.ui.theme"

	// Sync keys
	KeySyncEnabled  = "noto.sync.enabled"
	KeySyncEndpoint = "noto.sync.endpoint"
	KeySyncBucket   = "noto.sync.bucket"

	// Storage keys
	KeyStorageType       = "noto.storage.type"
	KeyStorageLocalPath  = "noto.storage.local.path"
	KeyStorageS3Bucket    = "noto.storage.s3.bucket"
	KeyStorageS3Region    = "noto.storage.s3.region"
	KeyStorageS3Endpoint  = "noto.storage.s3.endpoint"

	// Routing keys (from existing providers.RoutingPolicy)
	KeyRoutingLLMProvider    = "noto.routing.llm_provider"
	KeyRoutingLLMModel       = "noto.routing.llm_model"
	KeyRoutingSpeechProvider = "noto.routing.speech_provider"
	KeyRoutingProfile        = "noto.routing.profile"
)

// EnvConfigDir is the environment variable for overriding config directory.
const EnvConfigDir = "NOTO_CONFIG_DIR"

// EnvArtifactRoot is the environment variable for overriding artifact root.
const EnvArtifactRoot = "NOTO_ARTIFACT_ROOT"

// EnvPrefix is the environment variable prefix for Viper.
const EnvPrefix = "NOTO"

// Default values
const (
	DefaultRecordingsDir    = "recordings"
	DefaultArtifactRootName = "Noto"
	DefaultConfigDirName    = ".noto"
	DefaultSTTProvider      = "mistral"
	DefaultLLMProvider      = "openrouter"
	DefaultLLMModel        = "openai/gpt-4.1-mini"
	DefaultSummarizer      = "openrouter"
	DefaultUITheme         = "dark"
	DefaultSyncEnabled     = false
	DefaultStorageType     = "local"
)

// Provider env var references (maps config ref to env var).
var ProviderEnvRefs = map[string]string{
	"provider:mistral":    "NOTO_API_KEY_MISTRAL",
	"provider:assemblyai": "NOTO_API_KEY_ASSEMBLYAI",
	"provider:elevenlabs": "NOTO_API_KEY_ELEVENLABS",
	"provider:openrouter": "NOTO_API_KEY_OPENROUTER",
}

// DefaultConfigDir returns the default config directory path.
func DefaultConfigDir() string {
	if override := os.Getenv(EnvConfigDir); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return DefaultConfigDirName
	}
	return filepath.Join(home, "Library", "Application Support", "Noto")
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
	return filepath.Join(DefaultArtifactRoot(), DefaultRecordingsDir)
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
		"mistral":    "provider:mistral",
		"assemblyai": "provider:assemblyai",
		"elevenlabs": "provider:elevenlabs",
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
		"theme": DefaultUITheme,
	}
}

// SyncDefaults returns the default sync configuration.
func SyncDefaults() map[string]interface{} {
	return map[string]interface{}{
		"enabled":  DefaultSyncEnabled,
		"endpoint": "",
		"bucket":    "",
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
	}
}

// RoutingDefaults returns the default routing configuration.
func RoutingDefaults() providers.RoutingPolicy {
	return providers.DefaultRoutingPolicy()
}

// AllDefaults returns a map of all default values for Viper initialization.
func AllDefaults() map[string]interface{} {
	return map[string]interface{}{
		KeyNoto: map[string]interface{}{
			"schema_version":   "config.v1",
			"config_dir":       DefaultConfigDir(),
			"artifact_root":    DefaultArtifactRoot(),
			"recordings_dir":   DefaultRecordingsDir(),
			"providers":       ProviderDefaults(),
			"ui":               UIDefaults(),
			"sync":             SyncDefaults(),
			"storage":          StorageDefaults(),
			"routing": map[string]interface{}{
				"llm_provider":     DefaultLLMProvider,
				"llm_model":        DefaultLLMModel,
				"speech_provider":   DefaultSTTProvider,
				"profile":          string(providers.RoutingProfileManual),
			},
		},
	}
}
