package providers

import (
	"sort"
	"testing"
)

// TestDefaultRegistry_ReturnsExpectedProviders verifies that DefaultRegistry
// returns the expected provider IDs in sorted order. This characterizes
// the baseline provider list before any changes to the registry.
func TestDefaultRegistry_ReturnsExpectedProviders(t *testing.T) {
	reg := DefaultRegistry()
	suites := reg.List()

	var ids []string
	for _, s := range suites {
		ids = append(ids, s.ID)
	}

	sort.Strings(ids)

	expected := []string{
		"assemblyai",
		"fake-llm",
		"fake-stt",
		"openrouter",
	}

	if len(ids) != len(expected) {
		t.Fatalf("DefaultRegistry returned %d providers; want %d. IDs: %v", len(ids), len(expected), ids)
	}

	for i, e := range expected {
		if ids[i] != e {
			t.Errorf("provider[%d] = %q; want %q", i, ids[i], e)
		}
	}
}

// TestDefaultRegistry_ContainsSpeechProviders verifies the expected speech
// (STT) providers are present. fake-stt is ProviderKindFake, not speech.
func TestDefaultRegistry_ContainsSpeechProviders(t *testing.T) {
	reg := DefaultRegistry()

	speechIDs := reg.SpeechProviderIDs()
	sort.Strings(speechIDs)

	// fake-stt has ProviderKindFake, not speech, so it's excluded.
	expectedSpeech := []string{
		"assemblyai",
	}

	if len(speechIDs) != len(expectedSpeech) {
		t.Fatalf("SpeechProviderIDs returned %d providers; want %d. IDs: %v", len(speechIDs), len(expectedSpeech), speechIDs)
	}

	for i, e := range expectedSpeech {
		if speechIDs[i] != e {
			t.Errorf("speech[%d] = %q; want %q", i, speechIDs[i], e)
		}
	}
}

// TestDefaultRegistry_ContainsExpectedLLMProviders verifies the expected
// LLM providers are present. fake-llm has ProviderKindFake.
func TestDefaultRegistry_ContainsExpectedLLMProviders(t *testing.T) {
	reg := DefaultRegistry()

	all := reg.List()
	var llmIDs []string
	for _, p := range all {
		if p.Kind == ProviderKindLLM {
			llmIDs = append(llmIDs, p.ID)
		}
	}

	sort.Strings(llmIDs)
	// fake-llm has ProviderKindFake, not LLM.
	expectedLLM := []string{"openrouter"}

	if len(llmIDs) != len(expectedLLM) {
		t.Fatalf("LLM provider IDs = %v; want %v", llmIDs, expectedLLM)
	}

	for i, e := range expectedLLM {
		if llmIDs[i] != e {
			t.Errorf("llm[%d] = %q; want %q", i, llmIDs[i], e)
		}
	}
}

// TestDefaultRegistry_GetExistingProvider verifies Get returns the correct provider.
func TestDefaultRegistry_GetExistingProvider(t *testing.T) {
	reg := DefaultRegistry()

	providers := []string{"assemblyai", "openrouter", "fake-stt"}
	for _, id := range providers {
		suite, ok := reg.Get(id)
		if !ok {
			t.Errorf("Get(%q) = false; want true", id)
			continue
		}
		if suite.ID != id {
			t.Errorf("suite.ID = %q; want %q", suite.ID, id)
		}
	}
}

// TestDefaultRegistry_GetMissingProvider verifies Get returns false for unknown providers.
func TestDefaultRegistry_GetMissingProvider(t *testing.T) {
	reg := DefaultRegistry()

	_, ok := reg.Get("nonexistent-provider")
	if ok {
		t.Error("Get(nonexistent-provider) = true; want false")
	}
}

// TestDefaultRegistry_MustGetExistingProvider verifies MustGet doesn't panic for known providers.
func TestDefaultRegistry_MustGetExistingProvider(t *testing.T) {
	reg := DefaultRegistry()

	for _, id := range []string{"assemblyai", "openrouter"} {
		suite, err := reg.MustGet(id)
		if err != nil {
			t.Errorf("MustGet(%q) returned error: %v", id, err)
			continue
		}
		if suite.ID != id {
			t.Errorf("suite.ID = %q; want %q", suite.ID, id)
		}
	}
}
