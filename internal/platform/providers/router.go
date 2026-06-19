package providers

import (
	"github.com/lukasstrickler/noto/internal/core/notoerr"
)

type RoutingProfile string

const (
	RoutingProfileManual            RoutingProfile = "manual"
	RoutingProfileBenchmarkSelected RoutingProfile = "benchmark-selected"
	RoutingProfileOfflineOnly       RoutingProfile = "offline_only"
)

type RoutingPolicy struct {
	SpeechProvider  string         `json:"speech_provider"`
	SpeechProviders []string       `json:"speech_providers"`
	LLMProvider     string         `json:"llm_provider"`
	LLMProviders    []string       `json:"llm_providers"`
	LLMModel        string         `json:"llm_model"`
	LLMPrivacy      LLMPrivacy     `json:"llm_privacy" mapstructure:"llm_privacy"`
	Profile         RoutingProfile `json:"profile"`
}

// LLMPrivacy controls how OpenRouter is allowed to route a request across its
// upstream providers. These map directly onto OpenRouter's `provider` routing
// block. They default ON so meeting transcripts stay private unless the user
// deliberately relaxes them (which widens the model pool but allows providers
// that may log or train on the data).
type LLMPrivacy struct {
	// ZDR routes only to endpoints with a Zero-Data-Retention policy.
	ZDR bool `json:"zdr" mapstructure:"zdr"`
	// DenyDataCollection routes only to providers that do not collect/train on data.
	DenyDataCollection bool `json:"deny_data_collection" mapstructure:"deny_data_collection"`
	// RequireParameters routes only to providers that honor every request
	// parameter (e.g. response_format) — keeps structured output reliable.
	RequireParameters bool `json:"require_parameters" mapstructure:"require_parameters"`
}

// DefaultLLMPrivacy returns the private-by-default posture.
func DefaultLLMPrivacy() LLMPrivacy {
	return LLMPrivacy{ZDR: true, DenyDataCollection: true, RequireParameters: true}
}

func DefaultRoutingPolicy() RoutingPolicy {
	return RoutingPolicy{
		SpeechProvider:  "parakeet-local",
		SpeechProviders: []string{"parakeet-local"},
		LLMProvider:     "openrouter",
		LLMProviders:    []string{"openrouter"},
		LLMModel:        "google/gemini-3.1-flash-preview",
		LLMPrivacy:      DefaultLLMPrivacy(),
		Profile:         RoutingProfileManual,
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

	if r.Policy.Profile == RoutingProfileBenchmarkSelected {
		return ProviderSuite{}, notoerr.New("benchmark_selection_missing", "No benchmark-selected speech provider has been set in config.", nil)
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

type ProviderRouter struct {
	Registry Registry
	Policy   RoutingPolicy
}

func (r ProviderRouter) STTProvider() STTProviderSuite {
	return STTProviderSuite{
		Primary:  r.Policy.SpeechProvider,
		Fallback: r.fallbackProviders(r.Policy.SpeechProviders, r.Policy.SpeechProvider),
	}
}

func (r ProviderRouter) LLMProvider() LLMProviderSuite {
	return LLMProviderSuite{
		Primary:  r.Policy.LLMProvider,
		Fallback: r.fallbackProviders(r.Policy.LLMProviders, r.Policy.LLMProvider),
	}
}

func (r ProviderRouter) fallbackProviders(providers []string, primary string) []string {
	var fallback []string
	for _, p := range providers {
		if p != primary {
			fallback = append(fallback, p)
		}
	}
	return fallback
}

type STTProviderSuite struct {
	Primary  string
	Fallback []string
}

type LLMProviderSuite struct {
	Primary  string
	Fallback []string
}
