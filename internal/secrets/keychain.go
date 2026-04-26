package secrets

import (
	"context"
	"os/exec"
	"runtime"
	"strings"

	"github.com/lukasstrickler/noto/internal/notoerr"
)

const KeychainService = "noto"

type KeychainStore struct{}

func (s KeychainStore) Set(ctx context.Context, ref string, value string) error {
	if runtime.GOOS != "darwin" {
		return notoerr.New("credential_store_unavailable", "macOS Keychain is not available on this platform.", map[string]any{"ref": ref})
	}
	cmd := exec.CommandContext(ctx, "security", "add-generic-password", "-U", "-s", KeychainService, "-a", ref, "-w", value)
	if out, err := cmd.CombinedOutput(); err != nil {
		return notoerr.New("credential_store_failed", "Could not write credential to macOS Keychain.", map[string]any{"ref": ref, "security": strings.TrimSpace(string(out))})
	}
	return nil
}

func (s KeychainStore) Get(ctx context.Context, ref string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", notoerr.New("credential_store_unavailable", "macOS Keychain is not available on this platform.", map[string]any{"ref": ref})
	}
	cmd := exec.CommandContext(ctx, "security", "find-generic-password", "-s", KeychainService, "-a", ref, "-w")
	out, err := cmd.Output()
	if err != nil {
		return "", notoerr.New("missing_credential", "Provider credential is not configured.", map[string]any{"ref": ref})
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", notoerr.New("missing_credential", "Provider credential is not configured.", map[string]any{"ref": ref})
	}
	return value, nil
}

func (s KeychainStore) Remove(ctx context.Context, ref string) error {
	if runtime.GOOS != "darwin" {
		return notoerr.New("credential_store_unavailable", "macOS Keychain is not available on this platform.", map[string]any{"ref": ref})
	}
	cmd := exec.CommandContext(ctx, "security", "delete-generic-password", "-s", KeychainService, "-a", ref)
	_ = cmd.Run()
	return nil
}

func (s KeychainStore) Status(ctx context.Context, ref string) (Status, error) {
	_, err := s.Get(ctx, ref)
	if err != nil {
		return Status{Ref: ref, Configured: false, Source: "keychain"}, nil
	}
	return Status{Ref: ref, Configured: true, Source: "keychain"}, nil
}
