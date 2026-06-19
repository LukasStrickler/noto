package providers

import (
	"sort"

	"github.com/lukasstrickler/noto/internal/core/notoerr"
)

type Registry struct {
	suites map[string]ProviderSuite
}

func NewRegistry(suites []ProviderSuite) Registry {
	m := make(map[string]ProviderSuite, len(suites))
	for _, suite := range suites {
		m[suite.ID] = suite
	}
	return Registry{suites: m}
}

func DefaultRegistry() Registry {
	return NewRegistry([]ProviderSuite{
		{
			ID:                     "parakeet-local",
			DisplayName:            "Parakeet TDT 0.6b v3 (local)",
			Kind:                   ProviderKindSpeech,
			Capabilities:           []Capability{CapabilityTranscribe, CapabilityWordTimestamps}, // NOT diarize — that's the diar seam
			CredentialRef:          "",
			RequiresNetwork:        false,
			SendsRawAudioOffDevice: false,
			PricingHint:            "Local compute only — $0 marginal cost, fully offline after first model fetch.",
			Notes:                  "Default local STT. Diarization is a separate stage. Fetch weights with `noto models download parakeet-tdt-0.6b-v3`.",
			Models: []Model{{
				ID:           "parakeet-tdt-0.6b-v3",
				DisplayName:  "Parakeet TDT 0.6b v3 (EN + 25 EU)",
				Capabilities: []Capability{CapabilityTranscribe, CapabilityWordTimestamps},
			}},
		},
		{
			ID:                     "openrouter",
			DisplayName:            "OpenRouter",
			Kind:                   ProviderKindLLM,
			Capabilities:           []Capability{CapabilitySummarize, CapabilityExtractActions, CapabilityExtractDecisions, CapabilityExtractRisks, CapabilityClassify, CapabilityChat},
			CredentialRef:          "provider:openrouter",
			RequiresNetwork:        true,
			SendsRawAudioOffDevice: false,
			PricingHint:            "Depends on selected OpenRouter model.",
			Notes:                  "Only real LLM provider allowed for production LLM work.",
			Models: []Model{{
				ID:           "openai/gpt-4.1-mini",
				DisplayName:  "OpenAI GPT-4.1 Mini via OpenRouter",
				Capabilities: []Capability{CapabilitySummarize, CapabilityExtractActions, CapabilityExtractDecisions, CapabilityExtractRisks, CapabilityClassify, CapabilityChat},
			}},
		},
		{
			ID:                     "fake-stt",
			DisplayName:            "Fake STT",
			Kind:                   ProviderKindFake,
			Capabilities:           []Capability{CapabilityTranscribe, CapabilityDiarize, CapabilityWordTimestamps, CapabilityAudioTags, CapabilityContextBiasing},
			CredentialRef:          "",
			RequiresNetwork:        false,
			SendsRawAudioOffDevice: false,
			PricingHint:            "No cost. Deterministic tests only.",
			Notes:                  "Offline fixture provider.",
		},
		{
			ID:                     "fake-llm",
			DisplayName:            "Fake LLM",
			Kind:                   ProviderKindFake,
			Capabilities:           []Capability{CapabilitySummarize, CapabilityExtractActions, CapabilityExtractDecisions, CapabilityExtractRisks, CapabilityClassify, CapabilityChat},
			CredentialRef:          "",
			RequiresNetwork:        false,
			SendsRawAudioOffDevice: false,
			PricingHint:            "No cost. Deterministic tests only.",
			Notes:                  "Offline fixture provider.",
		},
	})
}

func (r Registry) Get(id string) (ProviderSuite, bool) {
	s, ok := r.suites[id]
	return s, ok
}

func (r Registry) MustGet(id string) (ProviderSuite, error) {
	s, ok := r.Get(id)
	if !ok {
		return ProviderSuite{}, notoerr.New("unknown_provider", "Provider is not registered.", map[string]any{"provider": id})
	}
	return s, nil
}

func (r Registry) List() []ProviderSuite {
	out := make([]ProviderSuite, 0, len(r.suites))
	for _, s := range r.suites {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r Registry) SpeechProviderIDs() []string {
	var ids []string
	for _, s := range r.List() {
		if s.Kind == ProviderKindSpeech {
			ids = append(ids, s.ID)
		}
	}
	return ids
}
