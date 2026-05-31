package service

import (
	"context"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// ListProviders returns the full provider suite with status info merged.
func (s *Service) ListProviders(ctx context.Context) ([]notoapi.ProviderInfo, error) {
	cfg := s.currentCfg()
	suites := s.registry.List()
	out := make([]notoapi.ProviderInfo, 0, len(suites))
	for _, suite := range suites {
		info := notoapi.ProviderInfo{
			ID:                     suite.ID,
			DisplayName:            suite.DisplayName,
			Kind:                   string(suite.Kind),
			RequiresNetwork:        suite.RequiresNetwork,
			SendsRawAudioOffDevice: suite.SendsRawAudioOffDevice,
			Notes:                  suite.Notes,
		}
		for _, cap := range suite.Capabilities {
			info.Capabilities = append(info.Capabilities, string(cap))
		}
		for _, m := range suite.Models {
			model := notoapi.Model{
				ID:          m.ID,
				DisplayName: m.DisplayName,
			}
			for _, cap := range m.Capabilities {
				model.Capabilities = append(model.Capabilities, string(cap))
			}
			info.Models = append(info.Models, model)
		}
		if suite.CredentialRef != "" {
			status, _ := s.secrets.Status(ctx, suite.CredentialRef)
			info.HasKey = status.Configured
			info.KeySource = status.Source
		}
		if suite.Kind == providers.ProviderKindSpeech && cfg.Routing.SpeechProvider == suite.ID {
			info.IsActiveSpeech = true
		}
		if suite.Kind == providers.ProviderKindLLM && cfg.Routing.LLMProvider == suite.ID {
			info.IsActiveLLM = true
			info.ActiveModel = cfg.Routing.LLMModel
		}
		out = append(out, info)
	}
	return out, nil
}

// SetProviderKey stores a credential via the configured secrets store.
func (s *Service) SetProviderKey(ctx context.Context, providerID, value string) error {
	suite, ok := s.registry.Get(providerID)
	if !ok {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "unknown provider", map[string]any{"id": providerID})
	}
	if suite.CredentialRef == "" {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "provider does not accept a credential", map[string]any{"id": providerID})
	}
	if strings.TrimSpace(value) == "" {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "credential value is empty", nil)
	}
	if err := s.secrets.Set(ctx, suite.CredentialRef, value); err != nil {
		return notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	return nil
}

func (s *Service) DeleteProviderKey(ctx context.Context, providerID string) error {
	suite, ok := s.registry.Get(providerID)
	if !ok {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "unknown provider", map[string]any{"id": providerID})
	}
	if suite.CredentialRef == "" {
		return nil
	}
	if err := s.secrets.Remove(ctx, suite.CredentialRef); err != nil {
		return notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	return nil
}

// TestProvider reports whether the provider is usable. V1 only checks that
// the required credential is configured; a real round-trip test is
// provider-specific and not yet implemented.
func (s *Service) TestProvider(ctx context.Context, providerID string) (notoapi.TestProviderResult, error) {
	start := time.Now()
	suite, ok := s.registry.Get(providerID)
	if !ok {
		return notoapi.TestProviderResult{OK: false, Error: "unknown provider"},
			notoapi.NewError(notoapi.CodeInvalidRequest, "unknown provider", nil)
	}
	if suite.CredentialRef == "" {
		return notoapi.TestProviderResult{OK: true, LatencyMS: 0, Detail: "no credential required"}, nil
	}
	status, _ := s.secrets.Status(ctx, suite.CredentialRef)
	res := notoapi.TestProviderResult{
		LatencyMS: time.Since(start).Milliseconds(),
		OK:        status.Configured,
		Detail:    status.Source,
	}
	if !status.Configured {
		res.Error = "credential not configured"
	}
	return res, nil
}

// SetActiveSpeech updates the active speech provider in config + saves.
func (s *Service) SetActiveSpeech(ctx context.Context, providerID string) error {
	suite, ok := s.registry.Get(providerID)
	if !ok {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "unknown provider", nil)
	}
	if suite.Kind != providers.ProviderKindSpeech && suite.Kind != providers.ProviderKindFake {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "provider is not a speech provider", map[string]any{"kind": suite.Kind})
	}
	if err := s.updateCfg(func(cfg *config.Config) {
		cfg.Routing.SpeechProvider = providerID
	}); err != nil {
		return notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	return nil
}

// SetActiveLLMModel updates the active LLM model in config + saves.
// V1 routes LLM only through OpenRouter, so the provider stays fixed
// and only the model id changes.
func (s *Service) SetActiveLLMModel(ctx context.Context, modelID string) error {
	if strings.TrimSpace(modelID) == "" {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "model id is empty", nil)
	}
	if err := s.updateCfg(func(cfg *config.Config) {
		cfg.Routing.LLMProvider = "openrouter"
		cfg.Routing.LLMModel = modelID
	}); err != nil {
		return notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	return nil
}
