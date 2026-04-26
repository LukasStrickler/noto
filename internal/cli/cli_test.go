package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/internal/config"
	"github.com/lukasstrickler/noto/internal/providers"
	"github.com/lukasstrickler/noto/internal/secrets"
	"github.com/lukasstrickler/noto/internal/tui"
)

func testApp(t *testing.T) (app, *bytes.Buffer, *bytes.Buffer, *secrets.MemoryStore) {
	t.Helper()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	mem := secrets.NewMemoryStore()
	return app{
		in:      strings.NewReader(""),
		out:     out,
		errOut:  errOut,
		config:  config.NewStore(t.TempDir()),
		secrets: secrets.EnvFallbackStore{Primary: mem},
		reg:     providers.DefaultRegistry(),
	}, out, errOut, mem
}

func TestProvidersSetSpeechRejectsOpenRouter(t *testing.T) {
	a, _, _, _ := testApp(t)
	err := a.setSpeech("openrouter")
	if err == nil {
		t.Fatal("setSpeech(openrouter) succeeded")
	}
}

func TestProvidersSetLLMModelPinsOpenRouter(t *testing.T) {
	a, out, _, _ := testApp(t)
	if err := a.setLLMModel("anthropic/claude-3.5-sonnet"); err != nil {
		t.Fatalf("setLLMModel returned error: %v", err)
	}
	if !strings.Contains(out.String(), `"llm_provider": "openrouter"`) {
		t.Fatalf("response did not pin openrouter: %s", out.String())
	}
	cfg, err := a.config.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Routing.LLMProvider != "openrouter" {
		t.Fatalf("LLMProvider = %s, want openrouter", cfg.Routing.LLMProvider)
	}
}

func TestAuthSetDoesNotPrintSecret(t *testing.T) {
	a, out, _, mem := testApp(t)
	a.in = strings.NewReader("sk-or-secret\n")
	if err := a.providerAuth(context.Background(), []string{"set", "openrouter"}); err != nil {
		t.Fatalf("providerAuth set returned error: %v", err)
	}
	if strings.Contains(out.String(), "sk-or-secret") {
		t.Fatalf("auth response leaked secret: %s", out.String())
	}
	if got := mem.Values["provider:openrouter"]; got != "sk-or-secret" {
		t.Fatalf("stored credential = %q, want secret", got)
	}
}

func TestProvidersListIncludesRequiredProviders(t *testing.T) {
	a, out, _, _ := testApp(t)
	if err := a.providersList(context.Background()); err != nil {
		t.Fatalf("providersList returned error: %v", err)
	}
	text := out.String()
	for _, want := range []string{"mistral", "assemblyai", "elevenlabs", "openrouter", "fake-stt", "fake-llm"} {
		if !strings.Contains(text, want) {
			t.Fatalf("providersList missing %s in %s", want, text)
		}
	}
}

func TestHelpPrintsWithoutStartingTUI(t *testing.T) {
	a, out, _, _ := testApp(t)
	if err := a.run(context.Background(), []string{"help"}); err != nil {
		t.Fatalf("help returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Usage:") || !strings.Contains(out.String(), "noto                         Open the terminal UI") {
		t.Fatalf("help output missing usage:\n%s", out.String())
	}
}

func TestStatusJSONReportsIdleState(t *testing.T) {
	a, out, _, _ := testApp(t)
	if err := a.run(context.Background(), []string{"status", "--json"}); err != nil {
		t.Fatalf("status returned error: %v", err)
	}
	if !strings.Contains(out.String(), `"recording_state": "idle"`) {
		t.Fatalf("status output missing idle state:\n%s", out.String())
	}
}

func TestVerifyJSONRejectsBadLLMRoute(t *testing.T) {
	a, out, _, _ := testApp(t)
	cfg, err := a.config.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	cfg.Routing.LLMProvider = "mistral"
	if err := a.config.Save(cfg); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if err := a.run(context.Background(), []string{"verify", "--json"}); err != nil {
		t.Fatalf("verify returned error: %v", err)
	}
	if !strings.Contains(out.String(), `"ok": false`) || !strings.Contains(out.String(), "llm_route_invalid") {
		t.Fatalf("verify output did not show invalid route:\n%s", out.String())
	}
}

func TestDocumentedButUnimplementedCommandsReturnStableError(t *testing.T) {
	a, _, _, _ := testApp(t)
	err := a.run(context.Background(), []string{"record"})
	if err == nil || !strings.Contains(err.Error(), "documented V1 surface") {
		t.Fatalf("record returned %v, want not implemented error", err)
	}
}

func TestParseTUIScreen(t *testing.T) {
	if got := parseTUIScreen("providers"); got != tui.ScreenProviders {
		t.Fatalf("parseTUIScreen providers = %s", got)
	}
	if got := parseTUIScreen("unknown"); got != tui.ScreenDashboard {
		t.Fatalf("parseTUIScreen unknown = %s", got)
	}
}
