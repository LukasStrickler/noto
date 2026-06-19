package config

import (
	"strings"
	"testing"
)

func TestVADConfig_Env_DisabledIsEmpty(t *testing.T) {
	if env := (VADConfig{Enabled: false, PadSeconds: 0.4}).Env(); env != nil {
		t.Errorf("disabled VAD must emit no env, got %v", env)
	}
}

func TestVADConfig_Env_EnabledEmitsNotoVad(t *testing.T) {
	env := strings.Join((VADConfig{
		Enabled: true, PadSeconds: 0.4, MinGapSeconds: 2, Threshold: 0.35,
	}).Env(), " ")
	for _, want := range []string{"NOTO_VAD=1", "NOTO_VAD_PAD=0.4", "NOTO_VAD_MIN_GAP=2", "NOTO_VAD_THRESHOLD=0.35"} {
		if !strings.Contains(env, want) {
			t.Errorf("env %q missing %q", env, want)
		}
	}
}

func TestVADConfig_Env_OmitsUnsetParams(t *testing.T) {
	// Enabled with no params → just NOTO_VAD=1, so the server uses its own
	// pad/min_gap/threshold defaults.
	env := (VADConfig{Enabled: true}).Env()
	if len(env) != 1 || env[0] != "NOTO_VAD=1" {
		t.Errorf("enabled-with-no-params should emit only NOTO_VAD=1, got %v", env)
	}
}

func TestVADConfigRoundTripsThroughSave(t *testing.T) {
	// The production toggle: an operator sets compute.vad in the config file and
	// it must survive Save→Load so a restart picks it up.
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Compute.VAD = VADConfig{Enabled: true, PadSeconds: 0.4, MinGapSeconds: 2.5, Threshold: 0.35}
	if err := Save(cfg, dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.Compute.VAD.Enabled {
		t.Error("VAD enabled did not persist through Save→Load")
	}
	if loaded.Compute.VAD.PadSeconds != 0.4 || loaded.Compute.VAD.MinGapSeconds != 2.5 || loaded.Compute.VAD.Threshold != 0.35 {
		t.Errorf("VAD params not loaded: %+v", loaded.Compute.VAD)
	}
}
