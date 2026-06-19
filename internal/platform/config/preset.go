package config

import (
	"strings"

	"github.com/lukasstrickler/noto/internal/core/notoerr"
)

// DeploymentPreset names a common three-plane topology so users pick a shape
// rather than wiring a dozen individual knobs. Each preset rewrites the
// deployment-related config (backend / compute / storage) coherently.
type DeploymentPreset string

const (
	// PresetLocal runs everything in-process: capture, compute, and storage on
	// this machine. The default, zero-config, fully private.
	PresetLocal DeploymentPreset = "local"
	// PresetOffloadCompute keeps capture, identity, and data local but offloads
	// STT + diarization to a remote GPU compute endpoint. Identity stays
	// on-device (voice prints are cheap and the most sensitive data).
	PresetOffloadCompute DeploymentPreset = "offload-compute"
	// PresetRemoteBackend is the thin client: storage, compute, and the API all
	// live on a remote backend; only capture stays local (edge capture).
	PresetRemoteBackend DeploymentPreset = "remote-backend"
	// PresetAPIServer is the full three-way split: a local TUI captures and
	// orchestrates, STT + diarization run on a remote GPU (e.g. Modal), and
	// storage + the agent API live on a remote server. Identity stays on-device.
	// This is "local tui · modal gpu · server storage/api".
	PresetAPIServer DeploymentPreset = "api-server"
	// PresetCustom is reported by DetectPreset when the config matches no named
	// preset (e.g. a one-off per-capability mix).
	PresetCustom DeploymentPreset = "custom"
)

// PresetOptions carries the endpoints a preset needs. Each preset uses only the
// fields it requires; the bearer tokens live in the secrets store under stable
// refs (this only wires the references).
type PresetOptions struct {
	// ComputeURL is the remote GPU compute endpoint (Modal / a `noto serve` GPU
	// box) for offloaded STT + diarization.
	ComputeURL string
	// DataURL is the remote server hosting storage + the repo/agent API.
	DataURL string
	// BackendURL is the remote backend for the thin-client preset.
	BackendURL string
}

// TokenRef defaults the presets assign, so the corresponding bearer token has a
// stable secrets-store key the user can populate.
const (
	computeEndpointTokenRef = "compute:endpoint"
	remoteBackendTokenRef   = "backend:remote"
	remoteStorageTokenRef   = "storage:remote"
)

// localCompute resets every compute capability to in-process.
func localCompute() ComputeConfig {
	return ComputeConfig{
		Speech:  ComputeRoute{Location: ComputeLocationLocal},
		Diarize: ComputeRoute{Location: ComputeLocationLocal},
		Embed:   ComputeRoute{Location: ComputeLocationLocal},
	}
}

// offloadCompute routes STT + diarization to a remote GPU endpoint while keeping
// identity (embed) on-device — the recommended posture.
func offloadCompute(endpoint string) ComputeConfig {
	return ComputeConfig{
		Endpoint: ComputeEndpoint{URL: endpoint, TokenRef: computeEndpointTokenRef},
		Speech:   ComputeRoute{Location: ComputeLocationRemote},
		Diarize:  ComputeRoute{Location: ComputeLocationRemote},
		Embed:    ComputeRoute{Location: ComputeLocationLocal},
	}
}

// ApplyPreset rewrites the deployment config to a named topology, using the URLs
// each preset requires from opts. The bearer tokens live separately in the
// secrets store under stable refs; this only wires the references.
func (c *Config) ApplyPreset(p DeploymentPreset, opts PresetOptions) error {
	computeURL := strings.TrimSpace(opts.ComputeURL)
	dataURL := strings.TrimSpace(opts.DataURL)
	backendURL := strings.TrimSpace(opts.BackendURL)
	switch p {
	case PresetLocal:
		c.Backend = BackendConfig{Mode: DefaultBackendMode}
		c.Compute = localCompute()
		if strings.EqualFold(strings.TrimSpace(c.Storage.Type), "remote") {
			c.Storage.Type = DefaultStorageType
			c.Storage.Remote = RemoteStorageConfig{}
		}
		return nil
	case PresetOffloadCompute:
		if computeURL == "" {
			return notoerr.New("preset_endpoint_required", "The offload-compute preset needs a compute endpoint URL.", nil)
		}
		c.Compute = offloadCompute(computeURL)
		return nil
	case PresetRemoteBackend:
		if backendURL == "" {
			return notoerr.New("preset_endpoint_required", "The remote-backend preset needs a backend URL.", nil)
		}
		c.Backend = BackendConfig{Mode: "remote", Remote: RemoteBackendConfig{URL: backendURL, TokenRef: remoteBackendTokenRef}}
		return nil
	case PresetAPIServer:
		// Local TUI · Modal GPU · server storage+API. This node captures and
		// orchestrates locally, offloads STT+diarization to the GPU endpoint, and
		// writes artifacts through to the remote server (the source of truth +
		// agent API). Identity stays on-device.
		if computeURL == "" || dataURL == "" {
			return notoerr.New("preset_endpoint_required", "The api-server preset needs both a compute (GPU) URL and a data (server) URL.", nil)
		}
		c.Backend = BackendConfig{Mode: DefaultBackendMode} // this node orchestrates
		c.Compute = offloadCompute(computeURL)
		c.Storage.Type = "remote"
		c.Storage.Remote = RemoteStorageConfig{URL: dataURL, TokenRef: remoteStorageTokenRef}
		return nil
	default:
		return notoerr.New("unknown_preset", "Unknown deployment preset.", map[string]any{"preset": string(p)})
	}
}

// DetectPreset reports which named preset the current config matches, or
// PresetCustom for any shape that isn't one of the three (e.g. store-off-site or
// a per-capability mix). The deployment diagram uses this to label the topology.
func (c Config) DetectPreset() DeploymentPreset {
	if c.Backend.IsRemote() {
		return PresetRemoteBackend
	}
	_, speechRemote := c.Compute.Resolve(c.Compute.Speech)
	_, diarRemote := c.Compute.Resolve(c.Compute.Diarize)
	_, embedRemote := c.Compute.Resolve(c.Compute.Embed)
	storeRemote := c.Storage.IsRemote()
	switch {
	case storeRemote && speechRemote && diarRemote && !embedRemote:
		return PresetAPIServer
	case storeRemote:
		return PresetCustom // store-off-site without the matching compute split is an advanced shape
	case !speechRemote && !diarRemote && !embedRemote:
		return PresetLocal
	case speechRemote && diarRemote && !embedRemote:
		return PresetOffloadCompute
	default:
		return PresetCustom
	}
}

// PresetLabel is a short human label for a preset, for the diagram/UX.
func PresetLabel(p DeploymentPreset) string {
	switch p {
	case PresetLocal:
		return "Local"
	case PresetOffloadCompute:
		return "Offload compute"
	case PresetRemoteBackend:
		return "Remote backend"
	case PresetAPIServer:
		return "API server"
	default:
		return "Custom"
	}
}
