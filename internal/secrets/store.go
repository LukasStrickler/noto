package secrets

import (
	"context"
	"os"
	"strings"

	"github.com/lukasstrickler/noto/internal/notoerr"
)

type Store interface {
	Set(ctx context.Context, ref string, value string) error
	Get(ctx context.Context, ref string) (string, error)
	Remove(ctx context.Context, ref string) error
	Status(ctx context.Context, ref string) (Status, error)
}

type Status struct {
	Ref        string `json:"ref"`
	Configured bool   `json:"configured"`
	Source     string `json:"source"`
}

type EnvFallbackStore struct {
	Primary Store
	Env     map[string]string
}

func DefaultEnvRefs() map[string]string {
	return map[string]string{
		"provider:mistral":    "MISTRAL_API_KEY",
		"provider:assemblyai": "ASSEMBLYAI_API_KEY",
		"provider:elevenlabs": "ELEVENLABS_API_KEY",
		"provider:openrouter": "OPENROUTER_API_KEY",
	}
}

func (s EnvFallbackStore) Set(ctx context.Context, ref string, value string) error {
	if s.Primary == nil {
		return notoerr.New("credential_store_unavailable", "No writable credential store is available.", map[string]any{"ref": ref})
	}
	return s.Primary.Set(ctx, ref, value)
}

func (s EnvFallbackStore) Get(ctx context.Context, ref string) (string, error) {
	if s.Primary != nil {
		value, err := s.Primary.Get(ctx, ref)
		if err == nil && value != "" {
			return value, nil
		}
	}
	envName := s.envName(ref)
	if envName != "" {
		if value := os.Getenv(envName); value != "" {
			return value, nil
		}
	}
	return "", notoerr.New("missing_credential", "Provider credential is not configured.", map[string]any{"ref": ref})
}

func (s EnvFallbackStore) Remove(ctx context.Context, ref string) error {
	if s.Primary == nil {
		return notoerr.New("credential_store_unavailable", "No writable credential store is available.", map[string]any{"ref": ref})
	}
	return s.Primary.Remove(ctx, ref)
}

func (s EnvFallbackStore) Status(ctx context.Context, ref string) (Status, error) {
	if s.Primary != nil {
		status, err := s.Primary.Status(ctx, ref)
		if err == nil && status.Configured {
			return status, nil
		}
	}
	envName := s.envName(ref)
	if envName != "" && os.Getenv(envName) != "" {
		return Status{Ref: ref, Configured: true, Source: "env:" + envName}, nil
	}
	return Status{Ref: ref, Configured: false, Source: "missing"}, nil
}

func (s EnvFallbackStore) envName(ref string) string {
	if s.Env == nil {
		s.Env = DefaultEnvRefs()
	}
	return s.Env[ref]
}

type MemoryStore struct {
	Values map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{Values: map[string]string{}}
}

func (s *MemoryStore) Set(_ context.Context, ref string, value string) error {
	if s.Values == nil {
		s.Values = map[string]string{}
	}
	s.Values[ref] = value
	return nil
}

func (s *MemoryStore) Get(_ context.Context, ref string) (string, error) {
	if value := s.Values[ref]; value != "" {
		return value, nil
	}
	return "", notoerr.New("missing_credential", "Provider credential is not configured.", map[string]any{"ref": ref})
}

func (s *MemoryStore) Remove(_ context.Context, ref string) error {
	delete(s.Values, ref)
	return nil
}

func (s *MemoryStore) Status(_ context.Context, ref string) (Status, error) {
	value := strings.TrimSpace(s.Values[ref])
	return Status{Ref: ref, Configured: value != "", Source: "memory"}, nil
}
