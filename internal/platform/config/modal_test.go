package config

import "testing"

func TestModalComputeConfigRoundTripsThroughSave(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Compute.Modal = ModalComputeConfig{
		AppName:                "noto-compute",
		Environment:            "dev",
		GPU:                    "L4",
		EndpointURL:            "https://noto--compute.modal.run",
		TokenRef:               "compute:modal",
		EndpointTokenRef:       "compute:modal-endpoint",
		DeploymentVersion:      "noto-compute.v1",
		ModelVolume:            "noto-models",
		BenchmarkVolume:        "noto-bench",
		UserTransferMode:       "temporary",
		BenchmarkTransferMode:  "permanent",
		TemporaryThresholdMB:   256,
		ScaledownWindowSeconds: 120,
		MinContainers:          1,
		ModelCache:             "volume",
	}
	if err := Save(cfg, dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Compute.Modal.AppName != "noto-compute" {
		t.Errorf("app name = %q", loaded.Compute.Modal.AppName)
	}
	if loaded.Compute.Modal.EndpointURL != "https://noto--compute.modal.run" {
		t.Errorf("endpoint = %q", loaded.Compute.Modal.EndpointURL)
	}
	if loaded.Compute.Modal.DeploymentVersion != "noto-compute.v1" {
		t.Errorf("deployment version = %q", loaded.Compute.Modal.DeploymentVersion)
	}
	if loaded.Compute.Modal.BenchmarkVolume != "noto-bench" {
		t.Errorf("benchmark volume = %q", loaded.Compute.Modal.BenchmarkVolume)
	}
	if loaded.Compute.Modal.UserTransferMode != "temporary" {
		t.Errorf("user transfer mode = %q", loaded.Compute.Modal.UserTransferMode)
	}
	if loaded.Compute.Modal.BenchmarkTransferMode != "permanent" {
		t.Errorf("benchmark transfer mode = %q", loaded.Compute.Modal.BenchmarkTransferMode)
	}
	if loaded.Compute.Modal.TemporaryThresholdMB != 256 {
		t.Errorf("temporary threshold = %d", loaded.Compute.Modal.TemporaryThresholdMB)
	}
	if loaded.Compute.Modal.ScaledownWindowSeconds != 120 {
		t.Errorf("scaledown window = %d", loaded.Compute.Modal.ScaledownWindowSeconds)
	}
	if loaded.Compute.Modal.MinContainers != 1 {
		t.Errorf("min containers = %d", loaded.Compute.Modal.MinContainers)
	}
	if loaded.Compute.Modal.ModelCache != "volume" {
		t.Errorf("model cache = %q", loaded.Compute.Modal.ModelCache)
	}
}
