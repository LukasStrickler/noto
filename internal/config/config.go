package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

type dotReplacer struct{}

func (r *dotReplacer) Replace(s string) string {
	return s
}

func NewViper(cfgDir string) (*viper.Viper, error) {
	v := viper.New()

	v.SetConfigName(ConfigFileName())
	v.SetConfigType(ConfigFileExt())
	v.AddConfigPath(cfgDir)
	v.AddConfigPath(".")

	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(&dotReplacer{})

	v.AutomaticEnv()

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

	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, notoerr.Wrap("config_unmarshal_failed", "Failed to unmarshal config", err)
	}

	cfg.ConfigDir = cfgDir

	return cfg, nil
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

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
		} else {
			return Config{}, notoerr.Wrap("config_read_failed", "Failed to read config file", err)
		}
	}

	cfg := Config{}
	if err := v.Unmarshal(&cfg); err != nil {
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

	tmp := filepath.Join(dir, "config.yaml.tmp")
	if err := v.WriteConfigAs(tmp); err != nil {
		return notoerr.Wrap("config_write_failed", "Failed to write config file", err)
	}
	if err := os.Chmod(tmp, ConfigFileMode); err != nil {
		return notoerr.Wrap("config_file_perm_failed", "Could not set config file permissions", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "config.yaml")); err != nil {
		return notoerr.Wrap("config_commit_failed", "Failed to commit config file", err)
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
	return filepath.Join(c.ArtifactRoot, DefaultRecordingsDir)
}

func (c Config) GetArtifactRoot() string {
	return c.ArtifactRoot
}

func (c Config) GetConfigDir() string {
	return c.ConfigDir
}
