package secrets

import (
	"context"
	"os"
	"testing"
)

func TestEnvFallbackStoreUsesEnvWithoutWritingSecret(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	store := EnvFallbackStore{Primary: NewMemoryStore()}

	got, err := store.Get(context.Background(), "provider:openrouter")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got != "sk-or-test" {
		t.Fatalf("Get = %q, want env value", got)
	}
}

func TestEnvFallbackStoreReportsMissingCredential(t *testing.T) {
	_ = os.Unsetenv("MISTRAL_API_KEY")
	store := EnvFallbackStore{Primary: NewMemoryStore()}

	status, err := store.Status(context.Background(), "provider:mistral")
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Configured {
		t.Fatal("Status configured = true, want false")
	}
}
