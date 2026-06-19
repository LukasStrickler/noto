package service

import (
	"context"
	"net/url"
	"os"
	"runtime"
	"strings"

	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// GetSystem reports where this backend runs and what it can do, so a client can
// label the connection ("local · Mac M1 · CoreML" vs "remote · linux-cuda")
// and decide whether to offer recording. Compute is resolved once at startup;
// CaptureAvailable reflects whether a capture helper (Swift IPC) is wired —
// false on a headless Linux server.
func (s *Service) GetSystem(_ context.Context) (notoapi.System, error) {
	cfg := s.currentCfg()
	hostname, _ := os.Hostname()
	storageType := cfg.GetStorageBackend()
	if storageType == "" {
		storageType = "local"
	}
	dataPlane := notoapi.DataPlane{Location: "local", Storage: storageType}
	if cfg.Storage.IsRemote() {
		dataPlane.Location = "remote"
		dataPlane.Endpoint = hostOf(cfg.Storage.Remote.URL)
		dataPlane.Storage = "remote"
		dataPlane.Trust = trustOf(dataPlane.Endpoint)
	}

	return notoapi.System{
		SchemaVersion:    "system.v1",
		Mode:             backendModeLabel(cfg.Backend.Mode),
		Hostname:         hostname,
		OS:               runtime.GOOS,
		Arch:             runtime.GOARCH,
		Accelerator:      string(s.compute.Backend),
		AcceleratorOK:    s.compute.Verified,
		ModelTier:        string(s.compute.Tier),
		StorageType:      storageType,
		CaptureAvailable: s.ipc != nil,
		Version:          s.version,
		Compute: notoapi.ComputeTopology{
			Speech:  computePlacement(cfg.Compute, cfg.Compute.Speech),
			Diarize: computePlacement(cfg.Compute, cfg.Compute.Diarize),
			Embed:   computePlacement(cfg.Compute, cfg.Compute.Embed),
		},
		DataPlane: dataPlane,
	}, nil
}

// computePlacement maps a config compute route to the topology placement a
// client renders. A remote route reports its endpoint host (never the token or
// full URL).
func computePlacement(cc config.ComputeConfig, r config.ComputeRoute) notoapi.ComputePlacement {
	if ep, remote := cc.Resolve(r); remote {
		host := hostOf(ep.URL)
		return notoapi.ComputePlacement{Location: "remote", Endpoint: host, Trust: trustOf(host)}
	}
	return notoapi.ComputePlacement{Location: "local"}
}

// cloudHostSuffixes are domains of third-party GPU/inference providers. A remote
// endpoint on one of these is "cloud" (data leaves the user's systems); anything
// else (a bare IP, a LAN host, the user's own server) is "owned".
var cloudHostSuffixes = []string{
	"modal.run", "modal.com", "runpod.io", "runpod.net", "replicate.com",
	"baseten.co", "beam.cloud", "fal.run", "fal.ai", "lambdalabs.com",
	"together.ai", "together.xyz", "banana.dev", "cerebrium.ai",
}

// trustOf classifies a remote host as a third-party "cloud" provider or a system
// the user "owns" (their own server / LAN box / IP).
func trustOf(host string) string {
	h := strings.ToLower(host)
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i] // drop port
	}
	for _, suffix := range cloudHostSuffixes {
		if h == suffix || strings.HasSuffix(h, "."+suffix) {
			return notoapi.TrustCloud
		}
	}
	return notoapi.TrustOwned
}

// hostOf reduces a URL to a host:port hint for the topology diagram, dropping
// the scheme and any path so nothing sensitive leaks into the rendered view.
func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	// Bare host:port or scheme-less value.
	return strings.TrimSuffix(strings.TrimPrefix(raw, "//"), "/")
}

func backendModeLabel(mode string) string {
	if mode == "remote" {
		return "remote"
	}
	return "local"
}
