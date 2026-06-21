package service

import (
	"context"
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/providers"
)

func TestGetSystemReportsLocalTopology(t *testing.T) {
	dir := t.TempDir()
	svc := New(Deps{
		Config:   config.Config{ConfigDir: dir, ArtifactRoot: dir},
		Registry: providers.DefaultRegistry(),
		Version:  "test",
	})
	sys, err := svc.GetSystem(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sys.Compute.Speech.Location != "local" || sys.Compute.Embed.Location != "local" {
		t.Errorf("default compute should be local, got %+v", sys.Compute)
	}
	if sys.DataPlane.Location != "local" || sys.DataPlane.Storage != "local" {
		t.Errorf("default data plane should be local, got %+v", sys.DataPlane)
	}
}

func TestGetSystemReportsOffloadedCompute(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		ConfigDir:    dir,
		ArtifactRoot: dir,
		Compute: config.ComputeConfig{
			Endpoint: config.ComputeEndpoint{URL: "https://gpu.box:8731"},
			Speech:   config.ComputeRoute{Location: config.ComputeLocationRemote},
			Diarize:  config.ComputeRoute{Location: config.ComputeLocationLocal},
			Embed:    config.ComputeRoute{Location: config.ComputeLocationLocal},
		},
	}
	svc := New(Deps{Config: cfg, Registry: providers.DefaultRegistry(), Version: "test"})
	sys, err := svc.GetSystem(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sys.Compute.Speech.Location != "remote" || sys.Compute.Speech.Endpoint != "gpu.box:8731" {
		t.Errorf("speech should be remote at gpu.box:8731, got %+v", sys.Compute.Speech)
	}
	if sys.Compute.Diarize.Location != "local" {
		t.Errorf("diarize should be local, got %+v", sys.Compute.Diarize)
	}
	// Endpoint must be a host hint only — no scheme, no token.
	if got := sys.Compute.Speech.Endpoint; got != "gpu.box:8731" {
		t.Errorf("endpoint should be a bare host:port, got %q", got)
	}
}

// TestGetSystemModeReflectsEffectiveBackend pins System.Mode to the SAME predicate
// that drives thin-client behavior (Backend.IsRemote → host.go) and that the sibling
// DataPlane.Location uses (Storage.IsRemote), rather than a raw mode==\"remote\" string
// match. Two divergences this catches: a mode=\"remote\" with NO url runs in-process
// (must label \"local\"), and a case/space-variant \"Remote\" WITH a url is a real thin
// client (must label \"remote\").
func TestGetSystemModeReflectsEffectiveBackend(t *testing.T) {
	cases := []struct {
		name string
		mode string
		url  string
		want string
	}{
		{"default local", "local", "", "local"},
		{"remote with url", "remote", "https://backend.example:8731", "remote"},
		{"remote location but NO url → runs in-process → local", "remote", "", "local"},
		{"case/space-variant Remote with url → thin client → remote", " Remote ", "https://backend.example:8731", "remote"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := config.Config{
				ConfigDir:    dir,
				ArtifactRoot: dir,
				Backend: config.BackendConfig{
					Mode:   c.mode,
					Remote: config.RemoteBackendConfig{URL: c.url},
				},
			}
			svc := New(Deps{Config: cfg, Registry: providers.DefaultRegistry(), Version: "test"})
			sys, err := svc.GetSystem(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if sys.Mode != c.want {
				t.Errorf("System.Mode = %q; want %q (Backend.IsRemote=%v)", sys.Mode, c.want, cfg.Backend.IsRemote())
			}
		})
	}
}

func TestGetSystemReportsRemoteStorage(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		ConfigDir:    dir,
		ArtifactRoot: dir,
		Storage: config.StorageConfig{
			Type:   "remote",
			Remote: config.RemoteStorageConfig{URL: "https://data.example.com:8731"},
		},
	}
	svc := New(Deps{Config: cfg, Registry: providers.DefaultRegistry(), Version: "test"})
	sys, err := svc.GetSystem(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sys.DataPlane.Location != "remote" || sys.DataPlane.Storage != "remote" {
		t.Errorf("data plane should be remote, got %+v", sys.DataPlane)
	}
	if sys.DataPlane.Endpoint != "data.example.com:8731" {
		t.Errorf("data plane endpoint = %q, want host:port", sys.DataPlane.Endpoint)
	}
}
