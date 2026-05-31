package service

import (
	"context"
	"path/filepath"

	"github.com/lukasstrickler/noto/internal/config"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/providers"
)

// GetConfig returns the live config as a JSON-friendly struct.
func (s *Service) GetConfig(_ context.Context) (notoapi.Config, error) {
	return s.publicConfig(), nil
}

// PatchConfig accepts a partial update and persists merged config.
func (s *Service) PatchConfig(_ context.Context, patch notoapi.ConfigPatch) (notoapi.Config, error) {
	err := s.updateCfg(func(cfg *config.Config) {
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
				// providers.RoutingProfile is a typed string; accept the raw
				// value. Strict checking is provider-side.
				cfg.Routing.Profile = providers.RoutingProfile(patch.Routing.Profile)
			}
		}
	})
	if err != nil {
		return notoapi.Config{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	return s.publicConfig(), nil
}

// GetPaths returns the directory + socket paths used by the server.
func (s *Service) GetPaths(_ context.Context) (notoapi.Paths, error) {
	cfg := s.currentCfg()
	return notoapi.Paths{
		ConfigDir:     cfg.ConfigDir,
		ArtifactRoot:  cfg.GetArtifactRoot(),
		RecordingsDir: s.recordingsDir,
		SQLitePath:    s.sqlitePath,
		APISocket:     filepath.Join(cfg.ConfigDir, "api.sock"),
	}, nil
}

func (s *Service) publicConfig() notoapi.Config {
	cfg := s.currentCfg()
	return notoapi.Config{
		SchemaVersion: cfg.SchemaVersion,
		ArtifactRoot:  cfg.GetArtifactRoot(),
		RecordingsDir: s.recordingsDir,
		ConfigDir:     cfg.ConfigDir,
		UI: notoapi.ConfigUI{
			Theme: cfg.UI.Theme,
		},
		Routing: notoapi.ConfigRouting{
			SpeechProvider: cfg.Routing.SpeechProvider,
			LLMProvider:    cfg.Routing.LLMProvider,
			LLMModel:       cfg.Routing.LLMModel,
			Profile:        string(cfg.Routing.Profile),
		},
	}
}
