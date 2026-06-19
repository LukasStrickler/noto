package config

import "testing"

func TestPresetLocalIsAllLocal(t *testing.T) {
	c := DefaultConfig()
	c.Compute.Speech.Location = ComputeLocationRemote
	c.Compute.Endpoint.URL = "https://gpu.box"
	if err := c.ApplyPreset(PresetLocal, PresetOptions{}); err != nil {
		t.Fatal(err)
	}
	if c.DetectPreset() != PresetLocal {
		t.Errorf("after PresetLocal, DetectPreset = %q", c.DetectPreset())
	}
	if _, remote := c.Compute.Resolve(c.Compute.Speech); remote {
		t.Error("PresetLocal must clear remote compute")
	}
}

func TestPresetOffloadCompute(t *testing.T) {
	c := DefaultConfig()
	if err := c.ApplyPreset(PresetOffloadCompute, PresetOptions{ComputeURL: "https://gpu.box:8731"}); err != nil {
		t.Fatal(err)
	}
	if _, remote := c.Compute.Resolve(c.Compute.Speech); !remote {
		t.Error("speech should be remote")
	}
	if _, remote := c.Compute.Resolve(c.Compute.Diarize); !remote {
		t.Error("diarize should be remote")
	}
	if _, remote := c.Compute.Resolve(c.Compute.Embed); remote {
		t.Error("embed (identity) must stay local under offload-compute")
	}
	if c.DetectPreset() != PresetOffloadCompute {
		t.Errorf("DetectPreset = %q, want offload-compute", c.DetectPreset())
	}
}

func TestPresetOffloadComputeRequiresEndpoint(t *testing.T) {
	c := DefaultConfig()
	if err := c.ApplyPreset(PresetOffloadCompute, PresetOptions{}); err == nil {
		t.Error("offload-compute without an endpoint should error")
	}
}

func TestPresetRemoteBackend(t *testing.T) {
	c := DefaultConfig()
	if err := c.ApplyPreset(PresetRemoteBackend, PresetOptions{BackendURL: "https://noto.example.com:8731"}); err != nil {
		t.Fatal(err)
	}
	if !c.Backend.IsRemote() {
		t.Error("backend should be remote")
	}
	if c.DetectPreset() != PresetRemoteBackend {
		t.Errorf("DetectPreset = %q, want remote-backend", c.DetectPreset())
	}
}

// The api-server preset is the full split: local TUI + Modal GPU compute +
// server storage/API. Compute and storage are both remote; identity stays local.
func TestPresetAPIServer(t *testing.T) {
	c := DefaultConfig()
	err := c.ApplyPreset(PresetAPIServer, PresetOptions{
		ComputeURL: "https://noto--gpu.modal.run",
		DataURL:    "https://srv.example.com:8731",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, remote := c.Compute.Resolve(c.Compute.Speech); !remote {
		t.Error("speech should be on the GPU endpoint")
	}
	if _, remote := c.Compute.Resolve(c.Compute.Embed); remote {
		t.Error("identity must stay on-device under api-server")
	}
	if !c.Storage.IsRemote() || c.Storage.Remote.URL != "https://srv.example.com:8731" {
		t.Errorf("storage should be the remote server, got %+v", c.Storage)
	}
	if c.Backend.IsRemote() {
		t.Error("this node orchestrates locally; backend must stay local")
	}
	if c.DetectPreset() != PresetAPIServer {
		t.Errorf("DetectPreset = %q, want api-server", c.DetectPreset())
	}
}

func TestPresetAPIServerRequiresBothURLs(t *testing.T) {
	c := DefaultConfig()
	if err := c.ApplyPreset(PresetAPIServer, PresetOptions{ComputeURL: "https://gpu"}); err == nil {
		t.Error("api-server without a data URL should error")
	}
	if err := c.ApplyPreset(PresetAPIServer, PresetOptions{DataURL: "https://srv"}); err == nil {
		t.Error("api-server without a compute URL should error")
	}
}

func TestDefaultConfigDetectsLocal(t *testing.T) {
	if got := DefaultConfig().DetectPreset(); got != PresetLocal {
		t.Errorf("default config preset = %q, want local", got)
	}
}
