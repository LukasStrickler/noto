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
