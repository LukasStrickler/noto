package providers

import "testing"

func TestLLMCapabilitiesRouteOnlyToOpenRouter(t *testing.T) {
	reg := DefaultRegistry()
	llmCaps := []Capability{
		CapabilitySummarize,
		CapabilityExtractActions,
		CapabilityExtractDecisions,
		CapabilityExtractRisks,
		CapabilityClassify,
		CapabilityChat,
	}

	for _, cap := range llmCaps {
		router := CapabilityRouter{
			Registry: reg,
			Policy: RoutingPolicy{
				SpeechProvider: "elevenlabs",
				LLMProvider:    "openrouter",
				LLMModel:       "anthropic/claude-3.5-sonnet",
				Profile:        RoutingProfileManual,
			},
		}
		got, err := router.Resolve(cap)
		if err != nil {
			t.Fatalf("Resolve(%s) returned error: %v", cap, err)
		}
		if got.ID != "openrouter" {
			t.Fatalf("Resolve(%s) = %s, want openrouter", cap, got.ID)
		}
	}
}

func TestLLMCapabilitiesRejectNonOpenRouterPolicy(t *testing.T) {
	router := CapabilityRouter{
		Registry: DefaultRegistry(),
		Policy: RoutingPolicy{
			SpeechProvider: "mistral",
			LLMProvider:    "mistral",
			LLMModel:       "mistral-large-latest",
			Profile:        RoutingProfileManual,
		},
	}

	_, err := router.Resolve(CapabilitySummarize)
	if err == nil {
		t.Fatal("Resolve(summarize) succeeded with non-OpenRouter LLM provider")
	}
}

func TestSpeechCapabilitiesRouteToConfiguredSpeechProvider(t *testing.T) {
	router := CapabilityRouter{
		Registry: DefaultRegistry(),
		Policy: RoutingPolicy{
			SpeechProvider: "assemblyai",
			LLMProvider:    "openrouter",
			LLMModel:       "openai/gpt-4.1-mini",
			Profile:        RoutingProfileManual,
		},
	}

	got, err := router.Resolve(CapabilityTranscribe)
	if err != nil {
		t.Fatalf("Resolve(transcribe) returned error: %v", err)
	}
	if got.ID != "assemblyai" {
		t.Fatalf("Resolve(transcribe) = %s, want assemblyai", got.ID)
	}
}

func TestOfflineOnlyUsesFakeProviders(t *testing.T) {
	router := CapabilityRouter{
		Registry: DefaultRegistry(),
		Policy:   RoutingPolicy{Profile: RoutingProfileOfflineOnly},
	}

	stt, err := router.Resolve(CapabilityTranscribe)
	if err != nil {
		t.Fatalf("Resolve(transcribe) returned error: %v", err)
	}
	if stt.ID != "fake-stt" {
		t.Fatalf("Resolve(transcribe) = %s, want fake-stt", stt.ID)
	}

	llm, err := router.Resolve(CapabilitySummarize)
	if err != nil {
		t.Fatalf("Resolve(summarize) returned error: %v", err)
	}
	if llm.ID != "fake-llm" {
		t.Fatalf("Resolve(summarize) = %s, want fake-llm", llm.ID)
	}
}
