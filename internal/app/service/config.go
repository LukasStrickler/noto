package service

import (
	"context"
	"path/filepath"

	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// GetConfig returns the live config as a JSON-friendly struct.
func (s *Service) GetConfig(_ context.Context) (notoapi.Config, error) {
	return s.publicConfig(), nil
}

// PatchConfig accepts a partial update and persists merged config.
func (s *Service) PatchConfig(_ context.Context, patch notoapi.ConfigPatch) (notoapi.Config, error) {
	err := s.updateCfg(func(cfg *config.Config) {
		if patch.UI != nil {
			// Guard each field: a patch that carries only one UI setting (e.g. a
			// sidebar-width drag) must not blank the others.
			if patch.UI.Theme != "" {
				cfg.UI.Theme = patch.UI.Theme
			}
			if patch.UI.SidebarWidth > 0 {
				cfg.UI.SidebarWidth = patch.UI.SidebarWidth
			}
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
		if patch.Compute != nil {
			if patch.Compute.Provider != "" {
				cfg.Compute.Provider = patch.Compute.Provider
			}
			if patch.Compute.EndpointURL != "" {
				cfg.Compute.Endpoint.URL = patch.Compute.EndpointURL
			}
			if patch.Compute.EndpointTokenRef != "" {
				cfg.Compute.Endpoint.TokenRef = patch.Compute.EndpointTokenRef
			}
			if patch.Compute.SpeechLocation != "" {
				cfg.Compute.Speech.Location = patch.Compute.SpeechLocation
			}
			if patch.Compute.DiarizeLocation != "" {
				cfg.Compute.Diarize.Location = patch.Compute.DiarizeLocation
			}
			if patch.Compute.EmbedLocation != "" {
				cfg.Compute.Embed.Location = patch.Compute.EmbedLocation
			}
			patchModalConfig(&cfg.Compute.Modal, patch.Compute.Modal)
		}
		if patch.Privacy != nil {
			// Pointer presence = intent to set all three; bools can't carry an
			// "unset" sentinel, so callers send the full desired posture.
			cfg.Routing.LLMPrivacy.ZDR = patch.Privacy.ZDR
			cfg.Routing.LLMPrivacy.DenyDataCollection = patch.Privacy.DenyDataCollection
			cfg.Routing.LLMPrivacy.RequireParameters = patch.Privacy.RequireParameters
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
			Theme:        cfg.UI.Theme,
			SidebarWidth: cfg.UI.SidebarWidth,
		},
		Routing: notoapi.ConfigRouting{
			SpeechProvider: cfg.Routing.SpeechProvider,
			LLMProvider:    cfg.Routing.LLMProvider,
			LLMModel:       cfg.Routing.LLMModel,
			Profile:        string(cfg.Routing.Profile),
		},
		Compute: publicComputeConfig(cfg),
		Privacy: notoapi.ConfigPrivacy{
			ZDR:                cfg.Routing.LLMPrivacy.ZDR,
			DenyDataCollection: cfg.Routing.LLMPrivacy.DenyDataCollection,
			RequireParameters:  cfg.Routing.LLMPrivacy.RequireParameters,
		},
	}
}

func patchModalConfig(dst *config.ModalComputeConfig, src notoapi.ModalConfig) {
	if src.AppName != "" {
		dst.AppName = src.AppName
	}
	if src.Environment != "" {
		dst.Environment = src.Environment
	}
	if src.GPU != "" {
		dst.GPU = src.GPU
	}
	if src.EndpointURL != "" {
		dst.EndpointURL = src.EndpointURL
	}
	if src.TokenRef != "" {
		dst.TokenRef = src.TokenRef
	}
	if src.EndpointTokenRef != "" {
		dst.EndpointTokenRef = src.EndpointTokenRef
	}
	if src.DeploymentVersion != "" {
		dst.DeploymentVersion = src.DeploymentVersion
	}
	if src.ModelVolume != "" {
		dst.ModelVolume = src.ModelVolume
	}
	if src.BenchmarkVolume != "" {
		dst.BenchmarkVolume = src.BenchmarkVolume
	}
	if src.UserTransferMode != "" {
		dst.UserTransferMode = src.UserTransferMode
	}
	if src.BenchmarkTransferMode != "" {
		dst.BenchmarkTransferMode = src.BenchmarkTransferMode
	}
	if src.TemporaryThresholdMB > 0 {
		dst.TemporaryThresholdMB = src.TemporaryThresholdMB
	}
	if src.ScaledownWindowSeconds > 0 {
		dst.ScaledownWindowSeconds = src.ScaledownWindowSeconds
	}
	if src.MinContainers > 0 {
		dst.MinContainers = src.MinContainers
	}
	if src.ModelCache != "" {
		dst.ModelCache = src.ModelCache
	}
}

func publicComputeConfig(cfg config.Config) notoapi.ConfigCompute {
	return notoapi.ConfigCompute{
		Provider:         cfg.Compute.Provider,
		SpeechLocation:   cfg.Compute.Speech.Location,
		DiarizeLocation:  cfg.Compute.Diarize.Location,
		EmbedLocation:    cfg.Compute.Embed.Location,
		EndpointURL:      cfg.Compute.Endpoint.URL,
		EndpointTokenRef: cfg.Compute.Endpoint.TokenRef,
		Modal: notoapi.ModalConfig{
			AppName:                cfg.Compute.Modal.AppName,
			Environment:            cfg.Compute.Modal.Environment,
			GPU:                    cfg.Compute.Modal.GPU,
			EndpointURL:            cfg.Compute.Modal.EndpointURL,
			TokenRef:               cfg.Compute.Modal.TokenRef,
			EndpointTokenRef:       cfg.Compute.Modal.EndpointTokenRef,
			DeploymentVersion:      cfg.Compute.Modal.DeploymentVersion,
			ModelVolume:            cfg.Compute.Modal.ModelVolume,
			BenchmarkVolume:        cfg.Compute.Modal.BenchmarkVolume,
			UserTransferMode:       cfg.Compute.Modal.UserTransferMode,
			BenchmarkTransferMode:  cfg.Compute.Modal.BenchmarkTransferMode,
			TemporaryThresholdMB:   cfg.Compute.Modal.TemporaryThresholdMB,
			ScaledownWindowSeconds: cfg.Compute.Modal.ScaledownWindowSeconds,
			MinContainers:          cfg.Compute.Modal.MinContainers,
			ModelCache:             cfg.Compute.Modal.ModelCache,
		},
	}
}
