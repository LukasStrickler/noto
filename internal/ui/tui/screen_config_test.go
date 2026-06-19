package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

func configWithBrokenRoutes() *configScreen {
	c := newConfigScreen().(*configScreen)
	c.cfgLoad, c.provLoad, c.storageLoad = false, false, false
	// A synthetic two-broken-route scenario (cloud providers needing keys),
	// plus the local default which needs none — to prove the local provider is
	// never flagged "add key".
	c.cfg.Routing.SpeechProvider = "cloud-asr"
	c.cfg.Routing.LLMProvider = "openrouter"
	c.provs = []notoapi.ProviderInfo{
		{ID: "cloud-asr", Kind: "speech", IsActiveSpeech: true, RequiresKey: true, HasKey: false},
		{ID: "openrouter", Kind: "llm", IsActiveLLM: true, RequiresKey: true, HasKey: false},
		{ID: "whisper-cloud", Kind: "speech", RequiresKey: true, HasKey: false}, // keyed but NOT routed → △ missing
		{ID: "parakeet-local", Kind: "speech", RequiresKey: false},              // local default → needs no key
		{ID: "local", Kind: "fake"}, // fake → needs no key
	}
	return c
}

// providerNeedsKey is the single predicate behind every config flag: it must
// fire only for the ACTIVE route missing a key, so the flag count matches the
// nav pill's ConfigIssues and never nags about a provider you aren't using.
func TestConfigProviderNeedsKey(t *testing.T) {
	c := configWithBrokenRoutes()
	by := map[string]notoapi.ProviderInfo{}
	for _, p := range c.provs {
		by[p.ID] = p
	}
	for id, want := range map[string]bool{"cloud-asr": true, "openrouter": true, "whisper-cloud": false, "parakeet-local": false, "local": false} {
		if got := c.providerNeedsKey(by[id]); got != want {
			t.Errorf("providerNeedsKey(%s) = %v, want %v", id, got, want)
		}
	}
	if got := c.keyIssueCount(); got != 2 {
		t.Errorf("keyIssueCount() = %d, want 2", got)
	}
	if got := c.sectionAttention(secAPIKeys); got != 2 {
		t.Errorf("sectionAttention(API keys) = %d, want 2", got)
	}
	if got := c.sectionAttention(secActive); got != 0 {
		t.Errorf("sectionAttention(Active routes) = %d, want 0", got)
	}
}

// The flag must reach the exact broken thing, not just "something in config":
// the API-keys section nav is badged, the landing (Active routes) marks each
// broken route, and only the ACTIVE keyless providers read "⚑ add key" — the
// unused keyless one stays a quiet "△ missing" so the flag never cries wolf.
func TestConfigAttentionTrail(t *testing.T) {
	c := configWithBrokenRoutes()
	ctx := screenCtx{ctx: context.Background(), keys: keys.New(), styles: theme.NewStyles(), width: 120, height: 24}

	got := ansi.Strip(c.view(ctx)) // lands on Active routes
	if !strings.Contains(got, "API keys") || !strings.Contains(got, "⚑2") {
		t.Errorf("section nav must badge 'API keys ⚑2'; got:\n%s", got)
	}
	if n := strings.Count(got, "⚑ no key"); n != 2 {
		t.Errorf("both broken routes must show '⚑ no key', found %d; got:\n%s", n, got)
	}

	c.section = secAPIKeys
	c.rightFocus = true
	got = ansi.Strip(c.view(ctx))
	if n := strings.Count(got, "⚑ add key"); n != 2 {
		t.Errorf("both active-route providers must read '⚑ add key', found %d; got:\n%s", n, got)
	}
	if !strings.Contains(got, "△ missing") {
		t.Errorf("the unused keyless provider must stay a quiet '△ missing'; got:\n%s", got)
	}
}

// The Deployment section renders the three-plane topology diagram derived from
// /v1/system, so a user sees where compute and data run.
func TestConfigDeploymentSectionRendersTopology(t *testing.T) {
	c := newConfigScreen().(*configScreen)
	c.cfgLoad, c.provLoad, c.storageLoad, c.sysLoad = false, false, false, false
	// A full-split topology so the diagram shows all three planes.
	c.sys = notoapi.System{
		Compute: notoapi.ComputeTopology{
			Speech:  notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.modal.run", Trust: notoapi.TrustCloud},
			Diarize: notoapi.ComputePlacement{Location: "remote", Endpoint: "gpu.modal.run", Trust: notoapi.TrustCloud},
			Embed:   notoapi.ComputePlacement{Location: "local"},
		},
		DataPlane: notoapi.DataPlane{Location: "remote", Endpoint: "srv:8731", Storage: "remote", Trust: notoapi.TrustOwned},
	}
	for i, sec := range c.sections() {
		if sec.id == secDeployment {
			c.section = i
		}
	}
	// Open on the shape this (full-split) install runs, so the diagram below the
	// list shows all three planes.
	c.cursors[secDeployment] = c.livePreviewIndex()
	ctx := screenCtx{ctx: context.Background(), keys: keys.New(), styles: theme.NewStyles(), width: 170, height: 40}
	got := ansi.Strip(c.view(ctx))
	// The selectable preview list (all four shapes, ordered) plus the diagram for
	// the selected shape rendered below it.
	for _, want := range []string{"Deployment shape", "This device", "Compute", "Data + API",
		"Local", "Offload compute", "Remote backend", "API server"} {
		if !strings.Contains(got, want) {
			t.Errorf("deployment section must render %q; got:\n%s", want, got)
		}
	}
	// The shapes must read simplest → most involved, with Remote backend BEFORE
	// the most complex API server.
	order := []string{"Local", "Offload compute", "Remote backend", "API server"}
	last := -1
	for _, label := range order {
		idx := strings.Index(got, label)
		if idx < 0 {
			t.Fatalf("missing shape %q", label)
		}
		if idx <= last {
			t.Errorf("shape %q is out of order; want Local · Offload compute · Remote backend · API server", label)
		}
		last = idx
	}
}

// On a plain local install the live diagram is one static box, but the user can
// still cycle the preview to a multi-machine shape and see it — and the diagram
// animates only for those multi-node previews.
func TestConfigDeploymentPreviewCyclesToMultiNode(t *testing.T) {
	c := newConfigScreen().(*configScreen)
	c.cfgLoad, c.provLoad, c.storageLoad, c.sysLoad = false, false, false, false
	c.sys = notoapi.System{Hostname: "this-mac", Compute: localCompute(),
		DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"}}
	for i, sec := range c.sections() {
		if sec.id == secDeployment {
			c.section = i
		}
	}
	c.rightFocus = true

	if c.animates() {
		t.Fatal("live local preview is single-node and must not animate")
	}

	apiIdx := -1
	for i, p := range c.topoPreviews() {
		if p.label == "API server" {
			apiIdx = i
		}
	}
	if apiIdx < 0 {
		t.Fatal("expected an 'API server' preview (local TUI + Modal GPU + server data/API)")
	}
	c.cursors[secDeployment] = apiIdx

	if !c.animates() {
		t.Error("the API server preview is multi-node and must animate")
	}
	sys, _ := c.previewSystem()
	if sys.DataPlane.Location != "remote" || sys.DataPlane.Trust != notoapi.TrustOwned {
		t.Errorf("API server preview should have an owned remote data plane; got %+v", sys.DataPlane)
	}
	if sys.Compute.Speech.Trust != notoapi.TrustCloud {
		t.Errorf("API server preview should put speech on a third-party GPU; got %+v", sys.Compute.Speech)
	}

	ctx := screenCtx{ctx: context.Background(), keys: keys.New(), styles: theme.NewStyles(), width: 170, height: 40}
	got := ansi.Strip(c.view(ctx))
	for _, want := range []string{"noto--gpu.modal.run", "Data + API", "Compute", "third-party"} {
		if !strings.Contains(got, want) {
			t.Errorf("api-server preview missing %q:\n%s", want, got)
		}
	}
}

// Cursoring down onto a multi-node preview must kick the animation tick (return a
// command) and mark the chain alive, so the picture comes to life on selection.
func TestConfigDeploymentDownStartsAnimation(t *testing.T) {
	c := newConfigScreen().(*configScreen)
	c.cfgLoad, c.provLoad, c.storageLoad, c.sysLoad = false, false, false, false
	c.sys = notoapi.System{Hostname: "this-mac", Compute: localCompute(),
		DataPlane: notoapi.DataPlane{Location: "local", Storage: "local"}}
	for i, sec := range c.sections() {
		if sec.id == secDeployment {
			c.section = i
		}
	}
	c.rightFocus = true
	ctx := screenCtx{ctx: context.Background(), keys: keys.New(), styles: theme.NewStyles(), width: 170, height: 40}

	// Cursor starts on Local (index 0): single-node, nothing to animate. Pressing
	// Up clamps to Local, so it must not start the tick.
	if _, cmd := c.updateKey(ctx, tea.KeyPressMsg{Code: tea.KeyUp}); cmd != nil {
		t.Error("staying on the single-node Local preview must not start the tick")
	}
	// Local(0) → Offload compute(1): multi-node, tick starts.
	_, cmd := c.updateKey(ctx, tea.KeyPressMsg{Code: tea.KeyDown})
	if cmd == nil {
		t.Error("cursoring onto a multi-node preview should start the animation tick")
	}
	if !c.topoAnim {
		t.Error("topoAnim should be set once the animation starts")
	}
}
