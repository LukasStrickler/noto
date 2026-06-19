package service_test

import (
	"context"
	"testing"

	"github.com/lukasstrickler/noto/internal/testutil"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

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
