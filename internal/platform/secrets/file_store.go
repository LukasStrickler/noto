package secrets

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/lukasstrickler/noto/internal/core/notoerr"
)

// FileStore persists credentials to a JSON file with 0600 perms. It is
// the cross-platform fallback for systems without a native keychain
// (Linux, Windows, container builds).
//
// Security note: contents are stored at rest with permission bits
// only — no encryption. For sensitive deployments use the platform
// keychain (macOS Security framework, libsecret on Linux). The TUI
// surfaces the storage origin so users can see at a glance which
// backend protects their keys.
type FileStore struct {
	Path string

	mu sync.Mutex
}

func NewFileStore(path string) *FileStore {
	return &FileStore{Path: path}
}

func (s *FileStore) load() (map[string]string, error) {
	bytes, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := map[string]string{}
	if len(bytes) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *FileStore) save(entries map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

func (s *FileStore) Set(_ context.Context, ref, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.load()
	if err != nil {
		return notoerr.Wrap("credential_store_failed", "Could not read credential file.", err)
	}
	entries[ref] = value
	if err := s.save(entries); err != nil {
		return notoerr.Wrap("credential_store_failed", "Could not write credential file.", err)
	}
	return nil
}

func (s *FileStore) Get(_ context.Context, ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.load()
	if err != nil {
		return "", notoerr.Wrap("credential_store_failed", "Could not read credential file.", err)
	}
	v, ok := entries[ref]
	if !ok || v == "" {
		return "", notoerr.New("missing_credential", "Provider credential is not configured.", map[string]any{"ref": ref})
	}
	return v, nil
}

func (s *FileStore) Remove(_ context.Context, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.load()
	if err != nil {
		return notoerr.Wrap("credential_store_failed", "Could not read credential file.", err)
	}
	delete(entries, ref)
	return s.save(entries)
}

func (s *FileStore) Status(ctx context.Context, ref string) (Status, error) {
	_, err := s.Get(ctx, ref)
	if err != nil {
		return Status{Ref: ref, Configured: false, Source: "file"}, nil
	}
	return Status{Ref: ref, Configured: true, Source: "file"}, nil
}
