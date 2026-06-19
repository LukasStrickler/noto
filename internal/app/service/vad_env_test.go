package service

import (
	"os"
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/config"
)

func TestApplyVADEnv_BridgesConfigToServerEnv(t *testing.T) {
	t.Setenv("NOTO_VAD", "")     // isolate from the developer's environment
	t.Setenv("NOTO_VAD_PAD", "") // (auto-restored after the test)

	cfg := config.DefaultConfig()
	cfg.Compute.VAD = config.VADConfig{Enabled: true, PadSeconds: 0.4}
	s := &Service{cfg: cfg}
	s.applyVADEnv()

	if got := os.Getenv("NOTO_VAD"); got != "1" {
		t.Errorf("NOTO_VAD = %q, want 1", got)
	}
	if got := os.Getenv("NOTO_VAD_PAD"); got != "0.4" {
		t.Errorf("NOTO_VAD_PAD = %q, want 0.4", got)
	}
}

func TestApplyVADEnv_NoopWhenDisabled(t *testing.T) {
	t.Setenv("NOTO_VAD", "preexisting")
	cfg := config.DefaultConfig()
	cfg.Compute.VAD = config.VADConfig{Enabled: false}
	s := &Service{cfg: cfg}
	s.applyVADEnv()

	// Disabled VAD must not clobber a direct env override.
	if got := os.Getenv("NOTO_VAD"); got != "preexisting" {
		t.Errorf("disabled VAD touched env: NOTO_VAD = %q", got)
	}
}
