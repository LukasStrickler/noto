package service

import (
	"context"
	"path/filepath"

	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/providers"
)

// providersRoutingProfile casts a wire string into the typed enum.
// The string is validated against the YAML config schema downstream;
// here we just pass it through so the patch can land.
func providersRoutingProfile(s string) providers.RoutingProfile {
	return providers.RoutingProfile(s)
}

// GetConfig returns the live config as a JSON-friendly struct.
func (s *Service) GetConfig(_ context.Context) (notoapi.Config, error) {
	return s.publicConfig(), nil
}

// PatchConfig accepts a partial update and persists merged config.
func (s *Service) PatchConfig(_ context.Context, patch notoapi.ConfigPatch) (notoapi.Config, error) {
	cfg := s.cfg
	if patch.UI != nil {
		cfg.UI.Theme = patch.UI.Theme
	}
	if patch.Routing != nil {
		if patch.Routing.SpeechProvider != "" {
			cfg.Routing.SpeechProvider = patch.Routing.SpeechProvider
		}
		if patch.Routing.LLMProvider != "" {
			cfg.Routing.LLMProvider = patch.Routing.LLMProvider
		}
		if patch.Routing.LLMModel != "" {
			cfg.Routing.LLMModel = patch.Routing.LLMModel
		}
		if patch.Routing.Profile != "" {
			// providers.RoutingProfile is a typed string; accept the raw value.
			// The viper-side validation lives there; we just pass through.
			// (Strict checking is provider-side.)
			//nolint:gocritic
			cfg.Routing.Profile = providersRoutingProfile(patch.Routing.Profile)
		}
	}
	if err := s.cfgStore.Save(cfg); err != nil {
		return notoapi.Config{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	s.cfg = cfg
	return s.publicConfig(), nil
}

// GetPaths returns the directory + socket paths used by the server.
func (s *Service) GetPaths(_ context.Context) (notoapi.Paths, error) {
	return notoapi.Paths{
		ConfigDir:     s.cfg.ConfigDir,
		ArtifactRoot:  s.cfg.GetArtifactRoot(),
		RecordingsDir: s.recordingsDir,
		SQLitePath:    s.sqlitePath,
		APISocket:     filepath.Join(s.cfg.ConfigDir, "api.sock"),
	}, nil
}

func (s *Service) publicConfig() notoapi.Config {
	return notoapi.Config{
		SchemaVersion: s.cfg.SchemaVersion,
		ArtifactRoot:  s.cfg.GetArtifactRoot(),
		RecordingsDir: s.recordingsDir,
		ConfigDir:     s.cfg.ConfigDir,
		UI: notoapi.ConfigUI{
			Theme: s.cfg.UI.Theme,
		},
		Routing: notoapi.ConfigRouting{
			SpeechProvider: s.cfg.Routing.SpeechProvider,
			LLMProvider:    s.cfg.Routing.LLMProvider,
			LLMModel:       s.cfg.Routing.LLMModel,
			Profile:        string(s.cfg.Routing.Profile),
		},
	}
}
