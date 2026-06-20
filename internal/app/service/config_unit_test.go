package service_test

import (
	"context"
	"os"
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/testutil"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// guardVADEnv snapshots and clears the NOTO_VAD* env, restoring it on cleanup, so
// a test that exercises the live env bridge can't leak into sibling tests.
func guardVADEnv(t *testing.T) {
	t.Helper()
	saved := map[string]*string{}
	for _, k := range config.VADEnvKeys() {
		if v, ok := os.LookupEnv(k); ok {
			vv := v
			saved[k] = &vv
		}
		_ = os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range config.VADEnvKeys() {
			if v := saved[k]; v != nil {
				_ = os.Setenv(k, *v)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	})
}

// A sidebar-width-only patch must leave the theme untouched: the two UI
// settings travel on the same struct, and an unset field (zero value) must not
// be written over the stored one. Guards the field-guards in PatchConfig.
func TestPatchConfig_SidebarWidthDoesNotClearTheme(t *testing.T) {
	svc := newTestSvc(t, testutil.NewFakeRepo())
	ctx := context.Background()

	// Seed a theme first.
	if _, err := svc.PatchConfig(ctx, notoapi.ConfigPatch{
		UI: &notoapi.ConfigUI{Theme: "light"},
	}); err != nil {
		t.Fatalf("seed theme: %v", err)
	}

	// Patch ONLY the sidebar width.
	cfg, err := svc.PatchConfig(ctx, notoapi.ConfigPatch{
		UI: &notoapi.ConfigUI{SidebarWidth: 42},
	})
	if err != nil {
		t.Fatalf("patch sidebar width: %v", err)
	}

	if cfg.UI.SidebarWidth != 42 {
		t.Errorf("sidebar width = %d; want 42", cfg.UI.SidebarWidth)
	}
	if cfg.UI.Theme != "light" {
		t.Errorf("theme = %q; sidebar-only patch must not clear it (want \"light\")", cfg.UI.Theme)
	}
}

// The config DTO always carries a VAD block (so a client can render the toggle),
// defaulting to disabled with no NOTO_VAD env leaking before anyone opts in.
func TestGetConfig_ExposesVADDefaultOff(t *testing.T) {
	guardVADEnv(t)
	svc := newTestSvc(t, testutil.NewFakeRepo())

	cfg, err := svc.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.Compute.VAD == nil {
		t.Fatal("Compute.VAD is nil; the DTO must always expose the toggle state")
	}
	if cfg.Compute.VAD.Enabled {
		t.Errorf("VAD enabled by default; want off")
	}
	if v, ok := os.LookupEnv("NOTO_VAD"); ok {
		t.Errorf("NOTO_VAD set to %q with VAD off; want unset", v)
	}
}

// Toggling VAD on then off must persist the posture, bridge it to the live
// NOTO_VAD env both ways (an explicit toggle is authoritative — off unsets it),
// and never clobber a sibling Compute field set by an earlier patch.
func TestPatchConfig_VADTogglesEnvAndPreservesSiblings(t *testing.T) {
	guardVADEnv(t)
	svc := newTestSvc(t, testutil.NewFakeRepo())
	ctx := context.Background()

	// Seed an unrelated Compute field so we can prove the VAD patch is surgical.
	if _, err := svc.PatchConfig(ctx, notoapi.ConfigPatch{
		Compute: &notoapi.ConfigCompute{Provider: "modal"},
	}); err != nil {
		t.Fatalf("seed provider: %v", err)
	}

	// Enable VAD.
	cfg, err := svc.PatchConfig(ctx, notoapi.ConfigPatch{
		Compute: &notoapi.ConfigCompute{VAD: &notoapi.ConfigVAD{Enabled: true}},
	})
	if err != nil {
		t.Fatalf("enable VAD: %v", err)
	}
	if cfg.Compute.VAD == nil || !cfg.Compute.VAD.Enabled {
		t.Errorf("VAD not enabled after patch: %+v", cfg.Compute.VAD)
	}
	if cfg.Compute.Provider != "modal" {
		t.Errorf("provider = %q; VAD patch must not clobber it (want \"modal\")", cfg.Compute.Provider)
	}
	if os.Getenv("NOTO_VAD") != "1" {
		t.Errorf("NOTO_VAD = %q after enable; want \"1\"", os.Getenv("NOTO_VAD"))
	}

	// Disable VAD — the authoritative toggle must clear the env, not just leave
	// the stale enable behind.
	cfg, err = svc.PatchConfig(ctx, notoapi.ConfigPatch{
		Compute: &notoapi.ConfigCompute{VAD: &notoapi.ConfigVAD{Enabled: false}},
	})
	if err != nil {
		t.Fatalf("disable VAD: %v", err)
	}
	if cfg.Compute.VAD == nil || cfg.Compute.VAD.Enabled {
		t.Errorf("VAD still enabled after disable patch: %+v", cfg.Compute.VAD)
	}
	if v, ok := os.LookupEnv("NOTO_VAD"); ok {
		t.Errorf("NOTO_VAD still set to %q after disable; want unset", v)
	}
}
