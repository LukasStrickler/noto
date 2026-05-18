package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lukasstrickler/noto/internal/notoerr"
	"github.com/lukasstrickler/noto/internal/providers"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Config struct {
	SchemaVersion  string                  `mapstructure:"schema_version"`
	ConfigDir      string                  `mapstructure:"config_dir"`
	ArtifactRoot   string                  `mapstructure:"artifact_root"`
	RecordingsDir  string                  `mapstructure:"recordings_dir"`
	Providers      ProviderConfig          `mapstructure:"providers"`
	UI             UIConfig               `mapstructure:"ui"`
	Sync           SyncConfig             `mapstructure:"sync"`
	Storage        StorageConfig          `mapstructure:"storage"`
	Routing        providers.RoutingPolicy `mapstructure:"routing"`
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
	Theme string `mapstructure:"theme"`
}

type SyncConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Endpoint string `mapstructure:"endpoint"`
	Bucket   string `mapstructure:"bucket"`
}

type StorageConfig struct {
	Type  string           `mapstructure:"type"`
	Local LocalStorageConfig `mapstructure:"local"`
	S3    S3StorageConfig  `mapstructure:"s3"`
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

// LoadOrDefault reads the config from s.dir; if the file does not exist
// it returns DefaultConfig() with ConfigDir set.
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
	if s := v.GetString(KeyUITheme); s != "" {
		cfg.UI.Theme = s
	}
	if s := v.GetString(KeyStorageType); s != "" {
		cfg.Storage.Type = s
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
	v.Set(KeySyncEnabled, cfg.Sync.Enabled)
	v.Set(KeySyncEndpoint, cfg.Sync.Endpoint)
	v.Set(KeySyncBucket, cfg.Sync.Bucket)
	v.Set(KeyStorageType, cfg.Storage.Type)
	v.Set(KeyStorageLocalPath, cfg.Storage.Local.Path)
	v.Set(KeyStorageS3Bucket, cfg.Storage.S3.Bucket)
	v.Set(KeyStorageS3Region, cfg.Storage.S3.Region)
	v.Set(KeyStorageS3Endpoint, cfg.Storage.S3.Endpoint)
	v.Set(KeyRoutingLLMProvider, cfg.Routing.LLMProvider)
	v.Set(KeyRoutingLLMModel, cfg.Routing.LLMModel)
	v.Set(KeyRoutingSpeechProvider, cfg.Routing.SpeechProvider)
	v.Set(KeyRoutingProfile, string(cfg.Routing.Profile))

	// viper.WriteConfigAs infers format from extension; the .tmp suffix
	// confuses it. Write to a normal .yaml first then rename.
	final := filepath.Join(dir, "config.yaml")
	if err := v.WriteConfigAs(final); err != nil {
		return notoerr.Wrap("config_write_failed", "Failed to write config file", err)
	}
	if err := os.Chmod(final, ConfigFileMode); err != nil {
		return notoerr.Wrap("config_file_perm_failed", "Could not set config file permissions", err)
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
		Routing: routing,
	}
}

func (c Config) GetProviderConfig(provider string) ProviderSettings {
	return ProviderSettings{
		Provider: provider,
		APIKeyRef: fmt.Sprintf("provider:%s", provider),
	}
}

type ProviderSettings struct {
	Provider string
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
