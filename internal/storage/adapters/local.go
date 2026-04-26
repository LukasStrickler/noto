package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type localAdapter struct {
	basePath string
}

func newLocalAdapter() (*localAdapter, error) {
	basePath := os.Getenv("NOTO_LOCAL_PATH")
	if basePath == "" {
		basePath = filepath.Join(os.Getenv("HOME"), "Noto")
	}

	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, ErrConfig("failed to create base directory: " + err.Error())
	}

	return &localAdapter{basePath: basePath}, nil
}

// safeJoin ensures the resolved path stays within basePath to prevent
// directory traversal attacks. Returns error if key attempts to escape.
func safeJoin(basePath, key string) (string, error) {
	// Use filepath.Join and then verify the result is still within basePath
	fullPath := filepath.Join(basePath, key)
	cleanPath := filepath.Clean(fullPath)

	// Resolve any symlinks in the final path to prevent symlink-based bypass attacks
	// This is critical for security - attackers could create symlinks inside basePath
	// that point outside, bypassing the prefix check
	resolvedPath, err := filepath.EvalSymlinks(cleanPath)
	if err != nil && !os.IsNotExist(err) {
		return "", errors.New("path traversal attempt detected: " + key)
	}

	// Ensure the clean path starts with basePath (with trailing separator)
	// This prevents attacks like "../../../etc/passwd"
	if !strings.HasPrefix(resolvedPath+string(filepath.Separator), basePath+string(filepath.Separator)) {
		return "", errors.New("path traversal attempt detected: " + key)
	}

	return resolvedPath, nil
}

func (a *localAdapter) PutObject(ctx context.Context, key string, body io.Reader, opts PutOptions) error {
	fullPath, err := safeJoin(a.basePath, key)
	if err != nil {
		return ErrUpload(key, err)
	}

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ErrUpload(key, err)
	}

	file, err := os.Create(fullPath)
	if err != nil {
		return ErrUpload(key, err)
	}
	defer file.Close()

	if _, err := io.Copy(file, body); err != nil {
		os.Remove(fullPath)
		return ErrUpload(key, err)
	}

	// Sync to ensure data is durable before close - if sync succeeds,
	// data is safely on disk even if close fails afterward
	if err := file.Sync(); err != nil {
		os.Remove(fullPath)
		return ErrUpload(key, err)
	}

	if err := file.Close(); err != nil {
		// Data is already synced to disk, file is valid despite close error
		return ErrUpload(key, err)
	}

	if opts.ContentType != "" {
		mimePath := fullPath + ".mime"
		if err := os.WriteFile(mimePath, []byte(opts.ContentType), 0644); err != nil {
			return ErrUpload(key, err)
		}
	}

	return nil
}

func (a *localAdapter) GetObject(ctx context.Context, key string, dest io.WriterAt) error {
	fullPath, err := safeJoin(a.basePath, key)
	if err != nil {
		return ErrDownload(key, err)
	}

	file, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrDownload(key, errors.New("object not found"))
		}
		return ErrDownload(key, err)
	}
	defer file.Close()

	offset := int64(0)
	buffer := make([]byte, 32*1024)
	for {
		n, err := file.Read(buffer)
		if n > 0 {
			_, werr := dest.WriteAt(buffer[:n], offset)
			if werr != nil {
				return ErrDownload(key, werr)
			}
			offset += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrDownload(key, err)
		}
	}

	return nil
}

func (a *localAdapter) DeleteObject(ctx context.Context, key string) error {
	fullPath, err := safeJoin(a.basePath, key)
	if err != nil {
		return ErrDelete(key, err)
	}

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return ErrDelete(key, err)
	}

	mimePath := fullPath + ".mime"
	os.Remove(mimePath)

	return nil
}

func (a *localAdapter) ListObjects(ctx context.Context, prefix string) ([]ObjectMeta, error) {
	fullPath, err := safeJoin(a.basePath, prefix)
	if err != nil {
		return nil, ErrList(prefix, err)
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []ObjectMeta{}, nil
		}
		return nil, ErrList(prefix, err)
	}

	var objects []ObjectMeta
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		key := filepath.Join(prefix, entry.Name())
		// Clean the key to remove any traversal sequences for consistent API responses
		key = filepath.Clean(key)
		objects = append(objects, ObjectMeta{
			Key:          key,
			Size:         info.Size(),
			LastModified: info.ModTime(),
		})
	}

	return objects, nil
}

func (a *localAdapter) GetPresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	fullPath, err := safeJoin(a.basePath, key)
	if err != nil {
		return "", ErrPresign(key, err)
	}

	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			return "", ErrPresign(key, errors.New("object not found"))
		}
		return "", ErrPresign(key, err)
	}

	return "file://" + fullPath, nil
}

func (a *localAdapter) HeadBucket(ctx context.Context) (BucketMeta, error) {
	return BucketMeta{Region: "local"}, nil
}

func (a *localAdapter) Close() error {
	return nil
}

func ComputeLocalChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func VerifyLocalChecksum(data []byte, expected string) error {
	if !strings.HasPrefix(expected, "sha256:") {
		expected = "sha256:" + expected
	}
	hash := sha256.Sum256(data)
	actual := "sha256:" + hex.EncodeToString(hash[:])
	if actual != expected {
		return errors.New("checksum mismatch")
	}
	return nil
}