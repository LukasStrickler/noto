package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreSavesConfigWithoutRawSecrets(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	cfg := Default()
	cfg.Credentials["openrouter"] = "provider:openrouter"

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	text := string(b)
	if strings.Contains(text, "sk-or-") || strings.Contains(text, "secret") {
		t.Fatalf("config leaked secret-like text: %s", text)
	}
	if !strings.Contains(text, "provider:openrouter") {
		t.Fatalf("config missing credential ref: %s", text)
	}
}

func TestStoreSavesMode0600(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Save(Default()); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %v, want 0600", got)
	}
}
