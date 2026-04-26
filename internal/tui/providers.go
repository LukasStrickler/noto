package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lukasstrickler/noto/internal/config"
	"github.com/lukasstrickler/noto/internal/providers"
	"github.com/lukasstrickler/noto/internal/secrets"
)

type ProviderScreen struct {
	Config    config.Config
	Providers []providers.ProviderSuite
	Statuses  map[string]secrets.Status
}

func RenderProviders(screen ProviderScreen) string {
	var b strings.Builder
	b.WriteString("Noto Providers\n")
	b.WriteString("==============\n\n")
	fmt.Fprintf(&b, "Artifacts: %s\n", screen.Config.ArtifactRoot)
	fmt.Fprintf(&b, "Config:    %s\n", screen.Config.ConfigDir)
	fmt.Fprintf(&b, "Speech:    %s\n", screen.Config.Routing.SpeechProvider)
	fmt.Fprintf(&b, "LLM:       openrouter / %s\n\n", screen.Config.Routing.LLMModel)

	b.WriteString("Speech-to-text\n")
	for _, p := range sortedByKind(screen.Providers, providers.ProviderKindSpeech) {
		status := screen.Statuses[p.ID]
		fmt.Fprintf(&b, "  %-10s %-12s %s\n", p.ID, keyState(status), capabilityText(p.Capabilities))
	}

	b.WriteString("\nLLM via OpenRouter\n")
	for _, p := range sortedByKind(screen.Providers, providers.ProviderKindLLM) {
		status := screen.Statuses[p.ID]
		fmt.Fprintf(&b, "  %-10s %-12s %s\n", p.ID, keyState(status), capabilityText(p.Capabilities))
	}

	b.WriteString("\nOffline/dev\n")
	for _, p := range sortedByKind(screen.Providers, providers.ProviderKindFake) {
		fmt.Fprintf(&b, "  %-10s %-12s %s\n", p.ID, "no key", capabilityText(p.Capabilities))
	}

	b.WriteString("\nWarnings\n")
	b.WriteString("  Live speech jobs send raw audio to the selected speech provider.\n")
	b.WriteString("  Production summaries and chat route only through OpenRouter.\n")
	b.WriteString("  API keys are stored by credential reference, not in config or artifacts.\n")
	return b.String()
}

func sortedByKind(all []providers.ProviderSuite, kind providers.ProviderKind) []providers.ProviderSuite {
	var out []providers.ProviderSuite
	for _, p := range all {
		if p.Kind == kind {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func keyState(status secrets.Status) string {
	if status.Configured {
		return "key ok"
	}
	return "missing key"
}

func capabilityText(caps []providers.Capability) string {
	sorted := providers.SortedCapabilities(caps)
	parts := make([]string, 0, len(sorted))
	for _, cap := range sorted {
		parts = append(parts, string(cap))
	}
	return strings.Join(parts, ",")
}
