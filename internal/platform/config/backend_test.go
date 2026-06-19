package config

import "testing"

func TestBackendDefaultsToLocal(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Backend.Mode != "local" {
		t.Errorf("default backend mode = %q, want local", cfg.Backend.Mode)
	}
	if cfg.Backend.IsRemote() {
		t.Error("default config must not be remote")
	}
}

func TestBackendRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Backend.Mode = "remote"
	cfg.Backend.Remote.URL = "https://noto.example.com:8731"
	cfg.Backend.Remote.TokenRef = "backend:remote"

	if err := Save(cfg, dir); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Backend.Mode != "remote" {
		t.Errorf("mode = %q, want remote", loaded.Backend.Mode)
	}
	if loaded.Backend.Remote.URL != cfg.Backend.Remote.URL {
		t.Errorf("url = %q, want %q", loaded.Backend.Remote.URL, cfg.Backend.Remote.URL)
	}
	if loaded.Backend.Remote.TokenRef != "backend:remote" {
		t.Errorf("token_ref = %q, want backend:remote", loaded.Backend.Remote.TokenRef)
	}
	if !loaded.Backend.IsRemote() {
		t.Error("loaded config should be remote")
	}
}

func TestBackendEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NOTO_BACKEND_MODE", "remote")
	t.Setenv("NOTO_BACKEND_REMOTE_URL", "http://10.0.0.5:8731")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Backend.IsRemote() {
		t.Fatal("env-configured remote backend should be remote")
	}
	if cfg.Backend.Remote.URL != "http://10.0.0.5:8731" {
		t.Errorf("url = %q", cfg.Backend.Remote.URL)
	}
}

func TestIsRemoteRequiresURL(t *testing.T) {
	// mode=remote but no URL is not actionable → not remote.
	b := BackendConfig{Mode: "remote"}
	if b.IsRemote() {
		t.Error("remote mode without a URL must not be treated as remote")
	}
}
