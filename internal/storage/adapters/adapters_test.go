package adapters

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLocalAdapter_PutObject(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("NOTO_LOCAL_PATH", tmpDir)
	defer os.Unsetenv("NOTO_LOCAL_PATH")

	adapter, err := newLocalAdapter()
	if err != nil {
		t.Fatalf("newLocalAdapter() = %v", err)
	}

	ctx := context.Background()
	content := []byte("hello world")
	reader := bytes.NewReader(content)

	err = adapter.PutObject(ctx, "test/file.txt", reader, PutOptions{ContentType: "text/plain"})
	if err != nil {
		t.Fatalf("PutObject() = %v", err)
	}

	path := filepath.Join(tmpDir, "test/file.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() = %v", err)
	}

	if string(data) != string(content) {
		t.Errorf("got %q, want %q", string(data), string(content))
	}
}

func TestLocalAdapter_GetObject(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("NOTO_LOCAL_PATH", tmpDir)
	defer os.Unsetenv("NOTO_LOCAL_PATH")

	adapter, err := newLocalAdapter()
	if err != nil {
		t.Fatalf("newLocalAdapter() = %v", err)
	}

	path := filepath.Join(tmpDir, "test/file.txt")
	err = os.WriteFile(path, []byte("hello world"), 0644)
	if err != nil {
		t.Fatalf("WriteFile() = %v", err)
	}

	ctx := context.Background()
	var buf bytes.Buffer

	err = adapter.GetObject(ctx, "test/file.txt", &buf)
	if err != nil {
		t.Fatalf("GetObject() = %v", err)
	}

	if buf.String() != "hello world" {
		t.Errorf("got %q, want %q", buf.String(), "hello world")
	}
}

func TestLocalAdapter_DeleteObject(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("NOTO_LOCAL_PATH", tmpDir)
	defer os.Unsetenv("NOTO_LOCAL_PATH")

	adapter, err := newLocalAdapter()
	if err != nil {
		t.Fatalf("newLocalAdapter() = %v", err)
	}

	path := filepath.Join(tmpDir, "test/file.txt")
	err = os.WriteFile(path, []byte("hello world"), 0644)
	if err != nil {
		t.Fatalf("WriteFile() = %v", err)
	}

	ctx := context.Background()
	err = adapter.DeleteObject(ctx, "test/file.txt")
	if err != nil {
		t.Fatalf("DeleteObject() = %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestLocalAdapter_ListObjects(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("NOTO_LOCAL_PATH", tmpDir)
	defer os.Unsetenv("NOTO_LOCAL_PATH")

	adapter, err := newLocalAdapter()
	if err != nil {
		t.Fatalf("newLocalAdapter() = %v", err)
	}

	subdir := filepath.Join(tmpDir, "prefix")
	os.MkdirAll(subdir, 0755)
	os.WriteFile(filepath.Join(subdir, "file1.txt"), []byte("1"), 0644)
	os.WriteFile(filepath.Join(subdir, "file2.txt"), []byte("2"), 0644)

	ctx := context.Background()
	objects, err := adapter.ListObjects(ctx, "prefix/")
	if err != nil {
		t.Fatalf("ListObjects() = %v", err)
	}

	if len(objects) != 2 {
		t.Errorf("got %d objects, want 2", len(objects))
	}
}

func TestLocalAdapter_GetPresignedURL(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("NOTO_LOCAL_PATH", tmpDir)
	defer os.Unsetenv("NOTO_LOCAL_PATH")

	adapter, err := newLocalAdapter()
	if err != nil {
		t.Fatalf("newLocalAdapter() = %v", err)
	}

	path := filepath.Join(tmpDir, "test/file.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	ctx := context.Background()
	url, err := adapter.GetPresignedURL(ctx, "test/file.txt", time.Hour)
	if err != nil {
		t.Fatalf("GetPresignedURL() = %v", err)
	}

	if url != "file://"+path {
		t.Errorf("got %q, want %q", url, "file://"+path)
	}
}

func TestLocalAdapter_HeadBucket(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("NOTO_LOCAL_PATH", tmpDir)
	defer os.Unsetenv("NOTO_LOCAL_PATH")

	adapter, err := newLocalAdapter()
	if err != nil {
		t.Fatalf("newLocalAdapter() = %v", err)
	}

	ctx := context.Background()
	meta, err := adapter.HeadBucket(ctx)
	if err != nil {
		t.Fatalf("HeadBucket() = %v", err)
	}

	if meta.Region != "local" {
		t.Errorf("got region %q, want %q", meta.Region, "local")
	}
}

func TestNewSyncAdapter_UnknownBackend(t *testing.T) {
	_, err := NewSyncAdapter("unknown")
	if err == nil {
		t.Error("expected error for unknown backend")
	}
}

func TestNewSyncAdapter_Local(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("NOTO_LOCAL_PATH", tmpDir)
	defer os.Unsetenv("NOTO_LOCAL_PATH")

	adapter, err := NewSyncAdapter("local")
	if err != nil {
		t.Fatalf("NewSyncAdapter(local) = %v", err)
	}

	if adapter == nil {
		t.Error("adapter should not be nil")
	}
}

func TestGenerateKey(t *testing.T) {
	meetingID := uuid.New()
	key := GenerateKey(meetingID, "transcript.json")

	if len(key) == 0 {
		t.Error("key should not be empty")
	}

	if !bytes.Contains([]byte(key), []byte(meetingID.String())) {
		t.Error("key should contain meeting ID")
	}

	expected := "noto/v1/meetings/"
	if key[:len(expected)] != expected {
		t.Errorf("key should start with %q, got %q", expected, key[:len(expected)])
	}
}

func TestComputeChecksum(t *testing.T) {
	data := []byte("hello world")
	checksum := ComputeChecksum(data)

	if checksum[:7] != "sha256:" {
		t.Error("checksum should start with sha256:")
	}

	if len(checksum) != 7+64 {
		t.Error("checksum length is wrong")
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("hello world")
	checksum := ComputeChecksum(data)

	err := VerifyChecksum(data, checksum)
	if err != nil {
		t.Errorf("VerifyChecksum() = %v, want nil", err)
	}

	err = VerifyChecksum(data, "invalid")
	if err == nil {
		t.Error("VerifyChecksum() should return error for invalid checksum")
	}
}

func TestLocalAdapter_VerifyChecksumOnDownload(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("NOTO_LOCAL_PATH", tmpDir)
	defer os.Unsetenv("NOTO_LOCAL_PATH")

	adapter, err := newLocalAdapter()
	if err != nil {
		t.Fatalf("newLocalAdapter() = %v", err)
	}

	ctx := context.Background()
	content := []byte("hello world")
	reader := bytes.NewReader(content)

	err = adapter.PutObject(ctx, "test/file.txt", reader, PutOptions{})
	if err != nil {
		t.Fatalf("PutObject() = %v", err)
	}

	var buf bytes.Buffer
	err = adapter.GetObject(ctx, "test/file.txt", &buf)
	if err != nil {
		t.Fatalf("GetObject() = %v", err)
	}

	checksum := ComputeChecksum(buf.Bytes())
	err = VerifyChecksum(buf.Bytes(), checksum)
	if err != nil {
		t.Errorf("checksum verification failed: %v", err)
	}
}