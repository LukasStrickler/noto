package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.SchemaVersion != "config.v1" {
		t.Errorf("SchemaVersion = %q, want config.v1", cfg.SchemaVersion)
	}
	if cfg.Providers.STT.Default != DefaultSTTProvider {
		t.Errorf("Providers.STT.Default = %q, want %q", cfg.Providers.STT.Default, DefaultSTTProvider)
	}
	if cfg.Providers.LLM.Default != DefaultLLMProvider {
		t.Errorf("Providers.LLM.Default = %q, want %q", cfg.Providers.LLM.Default, DefaultLLMProvider)
	}
	if cfg.UI.Theme != DefaultUITheme {
		t.Errorf("UI.Theme = %q, want %q", cfg.UI.Theme, DefaultUITheme)
	}
	if cfg.Sync.Enabled != DefaultSyncEnabled {
		t.Errorf("Sync.Enabled = %v, want %v", cfg.Sync.Enabled, DefaultSyncEnabled)
	}
	if cfg.Storage.Type != DefaultStorageType {
		t.Errorf("Storage.Type = %q, want %q", cfg.Storage.Type, DefaultStorageType)
	}
}

func TestStoreSavesYAMLConfig(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	cfg := DefaultConfig()

	if err := Save(cfg, dir); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	path := filepath.Join(dir, "config.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	text := string(b)

	if text == "" {
		t.Fatal("config file is empty")
	}
	if !contains(text, "schema_version") {
		t.Fatalf("config missing schema_version")
	}
}

func TestStoreSavesMode0600(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := Save(DefaultConfig(), dir); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if got := info.Mode().Perm(); got != ConfigFileMode {
		t.Fatalf("config mode = %v, want %o", got, ConfigFileMode)
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Providers.STT.Default = "assemblyai"
	cfg.UI.Theme = "light"

	if err := Save(cfg, dir); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if loaded.Providers.STT.Default != "assemblyai" {
		t.Errorf("Providers.STT.Default = %q, want assemblyai", loaded.Providers.STT.Default)
	}
	if loaded.UI.Theme != "light" {
		t.Errorf("UI.Theme = %q, want light", loaded.UI.Theme)
	}
}

func TestLoadConfigCreatesDefaults(t *testing.T) {
	dir := t.TempDir()

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if loaded.SchemaVersion != "config.v1" {
		t.Errorf("SchemaVersion = %q, want config.v1", loaded.SchemaVersion)
	}
	if loaded.Providers.STT.Default != DefaultSTTProvider {
		t.Errorf("Providers.STT.Default = %q, want %q", loaded.Providers.STT.Default, DefaultSTTProvider)
	}
}

func TestEnvVarOverrides(t *testing.T) {
	dir := t.TempDir()

	t.Setenv("NOTO_RECORDINGS_DIR", "/custom/recordings")
	t.Setenv("NOTO_PROVIDERS_STT_DEFAULT", "assemblyai")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.RecordingsDir != "/custom/recordings" {
		t.Errorf("RecordingsDir = %q, want /custom/recordings", cfg.RecordingsDir)
	}
	if cfg.Providers.STT.Default != "assemblyai" {
		t.Errorf("Providers.STT.Default = %q, want assemblyai", cfg.Providers.STT.Default)
	}
}

func TestConfigGetRecordingsDir(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ArtifactRoot = "/noto"
	cfg.RecordingsDir = "/noto/recordings"

	dir := cfg.GetRecordingsDir()
	if dir != "/noto/recordings" {
		t.Errorf("GetRecordingsDir() = %q, want /noto/recordings", dir)
	}
}

func TestConfigGetStorageBackend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Storage.Type = "s3"

	backend := cfg.GetStorageBackend()
	if backend != "s3" {
		t.Errorf("GetStorageBackend() = %q, want s3", backend)
	}
}

func TestConfigGetSyncGateway(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sync.Enabled = true
	cfg.Sync.Endpoint = "https://sync.example.com"

	gateway := cfg.GetSyncGateway()
	if gateway != "https://sync.example.com" {
		t.Errorf("GetSyncGateway() = %q, want https://sync.example.com", gateway)
	}
}

func TestConfigGetSyncGatewayDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sync.Enabled = false

	gateway := cfg.GetSyncGateway()
	if gateway != "" {
		t.Errorf("GetSyncGateway() = %q, want empty string when disabled", gateway)
	}
}

func TestNewStore(t *testing.T) {
	store := NewStore("")
	if store.Dir() == "" {
		t.Error("NewStore should set a default dir")
	}

	store = NewStore("/custom/path")
	if store.Dir() != "/custom/path" {
		t.Errorf("NewStore.Dir() = %q, want /custom/path", store.Dir())
	}
}

func TestStorePath(t *testing.T) {
	store := NewStore("/config")
	expected := "/config/config.yaml"
	if store.Path() != expected {
		t.Errorf("Store.Path() = %q, want %q", store.Path(), expected)
	}
}

func TestBindFlags(t *testing.T) {
	fs := NewFlagSet()
	v, _ := NewViper(t.TempDir())

	BindFlags(fs, v)

	if fs.Lookup(FlagRecordingsDir) == nil {
		t.Error("Flag recordings-dir not found")
	}
	if fs.Lookup(FlagSTTProvider) == nil {
		t.Error("Flag provider-stt not found")
	}
	if fs.Lookup(FlagUITheme) == nil {
		t.Error("Flag ui-theme not found")
	}
}

func TestLoadWithFlags(t *testing.T) {
	dir := t.TempDir()
	fs := NewFlagSet()

	fs.String(FlagSTTProvider, "assemblyai", "STT provider")

	cfg, err := LoadWithFlags(dir, fs)
	if err != nil {
		t.Fatalf("LoadWithFlags returned error: %v", err)
	}

	if cfg.Providers.STT.Default != "assemblyai" {
		t.Errorf("Providers.STT.Default = %q, want assemblyai", cfg.Providers.STT.Default)
	}
}

func TestSaveAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Providers.STT.Default = "elevenlabs"

	if err := Save(cfg, dir); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if loaded.Providers.STT.Default != "elevenlabs" {
		t.Errorf("Providers.STT.Default = %q, want elevenlabs", loaded.Providers.STT.Default)
	}

	tmpPath := filepath.Join(dir, "config.yaml.tmp")
	if _, err := os.Stat(tmpPath); err == nil {
		t.Error("temp file should not exist after commit")
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
