package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// fakeVADClient embeds the full Client interface (left nil) and implements only
// PatchConfig — the one method the Accuracy toggle calls — capturing the patch
// it was sent and echoing the new posture back like the real service.
type fakeVADClient struct {
	notoapi.Client
	gotPatch notoapi.ConfigPatch
	cfg      notoapi.Config
}

func (f *fakeVADClient) PatchConfig(_ context.Context, p notoapi.ConfigPatch) (notoapi.Config, error) {
	f.gotPatch = p
	if p.Compute != nil && p.Compute.VAD != nil {
		v := *p.Compute.VAD
		f.cfg.Compute.VAD = &v
	}
	return f.cfg, nil
}

// The Accuracy section must render the live VAD posture: a fresh root per case
// (the root caches the last frame, so one root reused across a silent mutation
// would serve a stale frame).
func TestConfigAccuracyReflectsVADState(t *testing.T) {
	for _, tc := range []struct {
		on   bool
		want string
	}{
		{false, "○ off"},
		{true, "● on"},
	} {
		r, c := configRoot(t, secAccuracy)
		c.cfg.Compute.VAD = &notoapi.ConfigVAD{Enabled: tc.on}
		got := ansi.Strip(r.View().Content)
		if !strings.Contains(got, tc.want) {
			t.Errorf("VAD on=%v: Accuracy section missing %q\n%s", tc.on, tc.want, got)
		}
	}
}

// Enter on the focused VAD row sends a patch that flips Enabled and carries the
// existing tuning floats through (read-modify-write — a toggle must not blank
// the pad/threshold the user may have tuned).
func TestConfigAccuracyEnterTogglesVAD(t *testing.T) {
	c := newConfigScreen().(*configScreen)
	c.cfgLoad, c.provLoad, c.storageLoad = false, false, false
	c.cfg.Compute.VAD = &notoapi.ConfigVAD{Enabled: false, PadSeconds: 0.25}
	c.section = secAccuracy
	c.rightFocus = true

	fake := &fakeVADClient{}
	ctx := testScreenCtx()
	ctx.client = fake

	_, cmd := c.update(ctx, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("Enter on the VAD row produced no command")
	}
	drain(cmd) // executes the closure → fake.PatchConfig

	if fake.gotPatch.Compute == nil || fake.gotPatch.Compute.VAD == nil {
		t.Fatal("Enter on the VAD row sent no VAD patch")
	}
	if !fake.gotPatch.Compute.VAD.Enabled {
		t.Errorf("toggling from off must request Enabled=true")
	}
	if fake.gotPatch.Compute.VAD.PadSeconds != 0.25 {
		t.Errorf("toggle dropped existing tuning: PadSeconds = %v; want 0.25",
			fake.gotPatch.Compute.VAD.PadSeconds)
	}
}
