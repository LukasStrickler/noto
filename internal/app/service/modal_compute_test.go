package service_test

import (
	"context"
	"testing"

	"github.com/lukasstrickler/noto/internal/app/service"
	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/lukasstrickler/noto/internal/platform/secrets"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

func newModalTestSvc(t *testing.T, cfg config.Config, store secrets.Store) *service.Service {
	t.Helper()
	cfg.ConfigDir = t.TempDir()
	cfg.ArtifactRoot = cfg.ConfigDir
	cfg.RecordingsDir = cfg.ConfigDir
	return service.New(service.Deps{
		Config:      cfg,
		ConfigStore: config.NewStore(cfg.ConfigDir),
		Secrets:     store,
		Registry:    providers.DefaultRegistry(),
		Version:     "unit-test",
	})
}

func TestSetupModalComputeStoresCredentialAndRoutesCompute(t *testing.T) {
	ctx := context.Background()
	secretStore := secrets.NewMemoryStore()
	svc := newModalTestSvc(t, config.DefaultConfig(), secretStore)

	status, err := svc.SetupModalCompute(ctx, notoapi.ModalSetupRequest{
		TokenID:                "id",
		TokenSecret:            "secret",
		EndpointURL:            "https://noto--compute.modal.run",
		AppName:                "noto-compute-dev",
		Environment:            "dev",
		GPU:                    "L4",
		UserTransferMode:       "temporary",
		BenchmarkTransferMode:  "permanent",
		TemporaryThresholdMB:   256,
		ScaledownWindowSeconds: 120,
		MinContainers:          1,
		ModelCache:             "volume",
		ConfigureRoutes:        true,
	})
	if err != nil {
		t.Fatalf("SetupModalCompute: %v", err)
	}
	if !status.Configured || !status.Ready {
		t.Fatalf("status = %+v, want configured and ready", status)
	}
	if status.EndpointURL != "https://noto--compute.modal.run" {
		t.Errorf("endpoint = %q", status.EndpointURL)
	}
	if status.UserTransferMode != "temporary" || status.BenchmarkTransferMode != "permanent" {
		t.Errorf("transfer modes = user %q benchmark %q", status.UserTransferMode, status.BenchmarkTransferMode)
	}
	if status.ScaledownWindowSeconds != 120 || status.MinContainers != 1 || status.ModelCache != "volume" {
		t.Errorf("runtime knobs = scaledown %d min %d cache %q", status.ScaledownWindowSeconds, status.MinContainers, status.ModelCache)
	}
	if got, err := secretStore.Get(ctx, config.ModalTokenRef); err != nil || got == "" {
		t.Fatalf("modal credential not stored: got=%q err=%v", got, err)
	}

	cfg, err := svc.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.Compute.Provider != "modal" {
		t.Errorf("compute provider = %q", cfg.Compute.Provider)
	}
	if cfg.Compute.SpeechLocation != "remote" || cfg.Compute.DiarizeLocation != "remote" {
		t.Errorf("compute routes = %+v, want speech+diarize remote", cfg.Compute)
	}

	sys, err := svc.GetSystem(ctx)
	if err != nil {
		t.Fatalf("GetSystem: %v", err)
	}
	if sys.Compute.Speech.Endpoint != "noto--compute.modal.run" {
		t.Errorf("system speech endpoint = %q", sys.Compute.Speech.Endpoint)
	}
}

func TestModalComputeStatusReportsUpdateNeeded(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Compute.Modal.EndpointURL = "https://noto--old.modal.run"
	cfg.Compute.Modal.DeploymentVersion = "old-version"
	cfg.Compute.Modal.TokenRef = config.ModalTokenRef
	svc := newModalTestSvc(t, cfg, secrets.NewMemoryStore())

	status, err := svc.GetModalComputeStatus(context.Background())
	if err != nil {
		t.Fatalf("GetModalComputeStatus: %v", err)
	}
	if !status.NeedsUpdate {
		t.Fatalf("NeedsUpdate = false, want true for old deployment: %+v", status)
	}
	if status.DesiredVersion == "" {
		t.Fatalf("DesiredVersion is empty")
	}
}
