package providers

import "github.com/lukasstrickler/noto/internal/notoerr"

type RoutingProfile string

const (
	RoutingProfileManual            RoutingProfile = "manual"
	RoutingProfileBenchmarkSelected RoutingProfile = "benchmark-selected"
	RoutingProfileOfflineOnly       RoutingProfile = "offline_only"
)

type RoutingPolicy struct {
	SpeechProvider string         `json:"speech_provider"`
	LLMProvider    string         `json:"llm_provider"`
	LLMModel       string         `json:"llm_model"`
	Profile        RoutingProfile `json:"profile"`
}

func DefaultRoutingPolicy() RoutingPolicy {
	return RoutingPolicy{
		SpeechProvider: "mistral",
		LLMProvider:    "openrouter",
		LLMModel:       "openai/gpt-4.1-mini",
		Profile:        RoutingProfileManual,
	}
}

type CapabilityRouter struct {
	Registry Registry
	Policy   RoutingPolicy
}

func (r CapabilityRouter) Resolve(cap Capability) (ProviderSuite, error) {
	if r.Policy.Profile == RoutingProfileOfflineOnly {
		if LLMCapabilities[cap] {
			return r.Registry.MustGet("fake-llm")
		}
		return r.Registry.MustGet("fake-stt")
	}

	if LLMCapabilities[cap] {
		if r.Policy.LLMProvider != "openrouter" {
			return ProviderSuite{}, notoerr.New("invalid_provider_route", "Real LLM capabilities must route through OpenRouter.", map[string]any{
				"capability": cap,
				"provider":   r.Policy.LLMProvider,
			})
		}
		return providerWithCapability(r.Registry, "openrouter", cap)
	}

	if r.Policy.SpeechProvider == RoutingProfileBenchmarkSelected.String() {
		return ProviderSuite{}, notoerr.New("benchmark_selection_missing", "No benchmark-selected speech provider has been written to config yet.", nil)
	}
	return providerWithCapability(r.Registry, r.Policy.SpeechProvider, cap)
}

func providerWithCapability(reg Registry, id string, cap Capability) (ProviderSuite, error) {
	s, err := reg.MustGet(id)
	if err != nil {
		return ProviderSuite{}, err
	}
	if !s.HasCapability(cap) {
		return ProviderSuite{}, notoerr.New("unsupported_capability", "Selected provider does not support the requested capability.", map[string]any{
			"provider":   id,
			"capability": cap,
		})
	}
	return s, nil
}

func (p RoutingProfile) String() string {
	return string(p)
}
