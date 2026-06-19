package config

import "testing"

func TestLLMDefaults_ModelAndPrivacy(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Routing.LLMModel != "google/gemini-3.1-flash-preview" {
		t.Errorf("default LLM model = %q, want google/gemini-3.1-flash-preview", cfg.Routing.LLMModel)
	}
	p := cfg.Routing.LLMPrivacy
	if !p.ZDR || !p.DenyDataCollection || !p.RequireParameters {
		t.Errorf("privacy guards must default ON, got %+v", p)
	}
}

// Flipping one guard off must persist (including the explicit-false case, which
// is the tricky part of the viper round-trip) while the others stay on.
func TestPrivacyRoundTrip_ExplicitFalsePersists(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Routing.LLMPrivacy.ZDR = false

	if err := Save(cfg, dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Routing.LLMPrivacy.ZDR {
		t.Error("ZDR=false did not persist (got true after reload)")
	}
	if !got.Routing.LLMPrivacy.DenyDataCollection || !got.Routing.LLMPrivacy.RequireParameters {
		t.Errorf("other guards should remain on, got %+v", got.Routing.LLMPrivacy)
	}
}
