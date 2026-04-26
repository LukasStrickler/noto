package providers

import (
	"context"

	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/notoerr"
)

type RoutingProfile string

const (
	RoutingProfileManual            RoutingProfile = "manual"
	RoutingProfileBenchmarkSelected RoutingProfile = "benchmark-selected"
	RoutingProfileOfflineOnly       RoutingProfile = "offline_only"
)

type RoutingPolicy struct {
	SpeechProvider       string         `json:"speech_provider"`
	SpeechProviders     []string       `json:"speech_providers"`
	LLMProvider         string         `json:"llm_provider"`
	LLMProviders        []string       `json:"llm_providers"`
	LLMModel            string         `json:"llm_model"`
	Profile             RoutingProfile `json:"profile"`
}

func DefaultRoutingPolicy() RoutingPolicy {
	return RoutingPolicy{
		SpeechProvider: "mistral",
		SpeechProviders: []string{"mistral", "assemblyai"},
		LLMProvider:    "openrouter",
		LLMProviders:   []string{"openrouter", "mistral"},
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

type ProviderRouter struct {
	Registry Registry
	Policy   RoutingPolicy
}

func (r ProviderRouter) STTProvider() STTProviderSuite {
	return STTProviderSuite{
		Primary:   r.Policy.SpeechProvider,
		Fallback: r.fallbackProviders(r.Policy.SpeechProviders, r.Policy.SpeechProvider),
	}
}

func (r ProviderRouter) LLMProvider() LLMProviderSuite {
	return LLMProviderSuite{
		Primary:   r.Policy.LLMProvider,
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
	Primary   string
	Fallback  []string
}

type LLMProviderSuite struct {
	Primary   string
	Fallback  []string
}

type STTProviderWrapper struct {
	inner   STTProvider
	jobID   string
}

func NewSTTProviderWrapper(inner STTProvider) *STTProviderWrapper {
	return &STTProviderWrapper{inner: inner}
}

func (w *STTProviderWrapper) ProviderID() string {
	return w.inner.ProviderID()
}

func (w *STTProviderWrapper) Transcribe(ctx context.Context, audio []byte, opts TranscribeOptions) (*artifacts.Transcript, error) {
	transcript, err := w.inner.Transcribe(ctx, audio, opts)
	if transcript != nil {
		w.jobID = transcript.Provider.JobID
	}
	return transcript, err
}

func (w *STTProviderWrapper) JobID() string {
	return w.jobID
}

type LLMProviderWrapper struct {
	inner LLMProvider
}

func NewLLMProviderWrapper(inner LLMProvider) *LLMProviderWrapper {
	return &LLMProviderWrapper{inner: inner}
}

func (w *LLMProviderWrapper) ProviderID() string {
	return w.inner.ProviderID()
}

func (w *LLMProviderWrapper) Summarize(ctx context.Context, transcript artifacts.Transcript, opts SummarizeOptions) (*artifacts.Summary, error) {
	return w.inner.Summarize(ctx, transcript, opts)
}