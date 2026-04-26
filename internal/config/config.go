package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/lukasstrickler/noto/internal/notoerr"
	"github.com/lukasstrickler/noto/internal/providers"
)

const (
	EnvConfigDir    = "NOTO_CONFIG_DIR"
	EnvArtifactRoot = "NOTO_ARTIFACT_ROOT"
)

type Config struct {
	SchemaVersion string                  `json:"schema_version"`
	ConfigDir     string                  `json:"config_dir"`
	ArtifactRoot  string                  `json:"artifact_root"`
	Routing       providers.RoutingPolicy `json:"routing"`
	Credentials   map[string]string       `json:"credentials"`
}

func Default() Config {
	configDir := DefaultConfigDir()
	artifactRoot := DefaultArtifactRoot()
	return Config{
		SchemaVersion: "config.v1",
		ConfigDir:     configDir,
		ArtifactRoot:  artifactRoot,
		Routing:       providers.DefaultRoutingPolicy(),
		Credentials: map[string]string{
			"mistral":    "provider:mistral",
			"assemblyai": "provider:assemblyai",
			"elevenlabs": "provider:elevenlabs",
			"openrouter": "provider:openrouter",
		},
	}
}

func DefaultConfigDir() string {
	if override := os.Getenv(EnvConfigDir); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".noto"
	}
	return filepath.Join(home, "Library", "Application Support", "Noto")
}

func DefaultArtifactRoot() string {
	if override := os.Getenv(EnvArtifactRoot); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "Noto"
	}
	return filepath.Join(home, "Noto")
}

type Store struct {
	Dir string
}

func NewStore(dir string) Store {
	if dir == "" {
		dir = DefaultConfigDir()
	}
	return Store{Dir: dir}
}

func (s Store) Path() string {
	return filepath.Join(s.Dir, "config.json")
}

func (s Store) Load() (Config, error) {
	cfg := Default()
	cfg.ConfigDir = s.Dir
	b, err := os.ReadFile(s.Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, notoerr.Wrap("config_read_failed", "Could not read Noto config.", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, notoerr.Wrap("config_parse_failed", "Could not parse Noto config.", err)
	}
	if cfg.SchemaVersion == "" {
		cfg.SchemaVersion = "config.v1"
	}
	if cfg.ConfigDir == "" {
		cfg.ConfigDir = s.Dir
	}
	if cfg.ArtifactRoot == "" {
		cfg.ArtifactRoot = DefaultArtifactRoot()
	}
	if cfg.Credentials == nil {
		cfg.Credentials = Default().Credentials
	}
	if cfg.Routing.LLMProvider == "" {
		cfg.Routing.LLMProvider = "openrouter"
	}
	if cfg.Routing.LLMModel == "" {
		cfg.Routing.LLMModel = providers.DefaultRoutingPolicy().LLMModel
	}
	if cfg.Routing.SpeechProvider == "" {
		cfg.Routing.SpeechProvider = providers.DefaultRoutingPolicy().SpeechProvider
	}
	if cfg.Routing.Profile == "" {
		cfg.Routing.Profile = providers.RoutingProfileManual
	}
	return cfg, nil
}

func (s Store) Save(cfg Config) error {
	if cfg.SchemaVersion == "" {
		cfg.SchemaVersion = "config.v1"
	}
	cfg.ConfigDir = s.Dir
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return notoerr.Wrap("config_dir_create_failed", "Could not create Noto config directory.", err)
	}
	if err := os.Chmod(s.Dir, 0o700); err != nil {
		return notoerr.Wrap("config_dir_permission_failed", "Could not secure Noto config directory.", err)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return notoerr.Wrap("config_encode_failed", "Could not encode Noto config.", err)
	}
	tmp := s.Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return notoerr.Wrap("config_write_failed", "Could not write Noto config.", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return notoerr.Wrap("config_permission_failed", "Could not secure Noto config.", err)
	}
	if err := os.Rename(tmp, s.Path()); err != nil {
		return notoerr.Wrap("config_commit_failed", "Could not commit Noto config.", err)
	}
	return nil
}
