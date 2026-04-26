package llm

import (
	"context"

	"github.com/lukasstrickler/noto/internal/artifacts"
)

// SummarizeOptions contains options for summarization.
type SummarizeOptions struct {
	// MeetingID is the noto meeting ID for artifact lineage.
	MeetingID string
	// PromptVersion is the version of the prompt template to use.
	PromptVersion string
	// Temperature controls randomness (0.0-1.0). nil uses provider default.
	Temperature *float64
}

// LLMProvider is the interface for LLM-based summary providers.
// Implementations must be safe for concurrent use.
type LLMProvider interface {
	// ProviderID returns the provider's canonical ID (e.g., "openrouter", "mistral").
	ProviderID() string
	// Summarize generates a summary from a transcript.
	Summarize(ctx context.Context, transcript artifacts.Transcript, opts SummarizeOptions) (*artifacts.Summary, error)
}