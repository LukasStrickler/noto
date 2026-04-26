package artifacts

import (
	"github.com/lukasstrickler/noto/internal/notoerr"
)

type PromptVersion struct {
	SchemaVersion string `json:"schema_version"`
	Version       string `json:"version"`
	PromptID      string `json:"prompt_id"`
	Content       string `json:"content"`
	CreatedAt     string `json:"created_at"`
}

func (p *PromptVersion) Kind() ArtifactKind {
	return KindPrompt
}

func (p *PromptVersion) Validate() *notoerr.Error {
	if p.SchemaVersion == "" {
		return NewMissingFieldError("schema_version")
	}
	if p.Version == "" {
		return NewMissingFieldError("version")
	}
	if p.PromptID == "" {
		return NewMissingFieldError("prompt_id")
	}
	return nil
}
