# Storage Bug Fixes (Iteration 5)

## Bugs Fixed

### Bug 1: CopyAudioToVersion Missing fsync - Durable Write Not Guaranteed
**Severity:** CRITICAL
**File:** internal/storage/meeting.go (lines 275-280)
**Fix:** Added `fsyncFile()` call after file close to ensure data is durably persisted before returning success.

```go
if err := dstFile.Close(); err != nil {
    os.Remove(versionAudioPath)
    return ErrWriteFailed(versionAudioPath, err)
}

if err := fsyncFile(versionAudioPath); err != nil {
    os.Remove(versionAudioPath)
    return ErrWriteFailed(versionAudioPath, err)
}

if err := fsyncDir(filepath.Dir(versionAudioPath)); err != nil {
    return err
}

return nil
```

### Bug 2: CopyAudioToVersion No fsyncDir After Write
**Severity:** HIGH
**File:** internal/storage/meeting.go (lines 264-280)
**Fix:** Added `fsyncDir()` call after fsync to ensure directory metadata is updated.

**Note:** This fix is combined with Bug 1 in the same code block above.

### Bug 3: GetObjectWithChecksumVerification Buffers Entire Object in Memory
**Severity:** CRITICAL
**File:** internal/storage/adapters/s3.go (lines 518-544)
**Fix:** Replaced unbounded `bytes.Buffer` with streaming hash computation using `sha256.New()` directly in `WriteAt`.

```go
type streamingChecksumWriter struct {
    w   io.WriterAt
    hash sha256.Hash
}

func (c *streamingChecksumWriter) WriteAt(p []byte, off int64) (n int, err error) {
    c.hash.Write(p)
    return c.w.WriteAt(p, off)
}

func GetObjectWithChecksumVerification(...) error {
    wrapper := &streamingChecksumWriter{
        w:   dest,
        hash: sha256.New(),
    }
    err := adapter.GetObject(ctx, key, wrapper)
    // ...
    actual := "sha256:" + hex.EncodeToString(wrapper.hash.Sum(nil))
    // ...
}
```

### Bug 4: ComputeFileChecksum Reads Entire File Into Memory
**Severity:** MEDIUM
**File:** internal/storage/meeting.go (lines 333-340)
**Fix:** Replaced `os.ReadFile` with streaming `io.Copy` to `sha256.New()` hash.

```go
func ComputeFileChecksum(path string) (string, error) {
    file, err := os.Open(path)
    if err != nil {
        return "", ErrReadFailed(path, err)
    }
    defer file.Close()

    hash := sha256.New()
    if _, err := io.Copy(hash, file); err != nil {
        return "", ErrReadFailed(path, err)
    }

    return "sha256:" + fmt.Sprintf("%x", hash.Sum(nil)), nil
}
```

### Bug 5: safeJoin Returns Unresolved Path Despite Security Check
**Severity:** MEDIUM
**File:** internal/storage/adapters/local.go (line 53)
**Fix:** Changed to return `resolvedPath` (symlinks resolved) instead of `fullPath` (unresolved).

```go
func safeJoin(basePath, key string) (string, error) {
    fullPath := filepath.Join(basePath, key)
    cleanPath := filepath.Clean(fullPath)

    resolvedPath, err := filepath.EvalSymlinks(cleanPath)
    if err != nil && !os.IsNotExist(err) {
        return "", errors.New("path traversal attempt detected: " + key)
    }

    if !strings.HasPrefix(resolvedPath+string(filepath.Separator), basePath+string(filepath.Separator)) {
        return "", errors.New("path traversal attempt detected: " + key)
    }

    return resolvedPath, nil
}
```

### Bug 6: LocalAdapter PutObject Silent Close Failure
**Severity:** LOW
**File:** internal/storage/adapters/local.go (lines 67-80)
**Fix:** Added `file.Sync()` before close to ensure data is durable. If sync succeeds, close error can be reported but file is valid.

```go
if _, err := io.Copy(file, body); err != nil {
    os.Remove(fullPath)
    return ErrUpload(key, err)
}

if err := file.Sync(); err != nil {
    os.Remove(fullPath)
    return ErrUpload(key, err)
}

if err := file.Close(); err != nil {
    return ErrUpload(key, err)
}
```

### Bug 7: WriteVersionManifest No Checksum for Version Manifest
**Severity:** MEDIUM
**File:** internal/storage/meeting.go (lines 283-313)
**Fix:** Added checksum write infrastructure to WriteVersionManifest, matching WriteManifest pattern.

```go
func WriteVersionManifest(layout DirectoryLayout, versionID string, m *artifacts.MeetingManifest) error {
    // ... existing setup ...

    checksum := artifacts.ComputeChecksum(data)
    checksumPath := layout.VersionChecksumPath(versionID)

    tmpChecksumPath := filepath.Join(layout.TmpDir, "version_checksum.tmp")
    if err := os.WriteFile(tmpChecksumPath, []byte(checksum), 0644); err != nil {
        return ErrWriteFailed(tmpChecksumPath, err)
    }
    if err := fsyncFile(tmpChecksumPath); err != nil {
        os.Remove(tmpChecksumPath)
        return ErrWriteFailed(tmpChecksumPath, err)
    }
    if err := os.Rename(tmpChecksumPath, checksumPath); err != nil {
        os.Remove(tmpChecksumPath)
        return ErrAtomicWrite(checksumPath, err)
    }
    if err := fsyncDir(filepath.Dir(checksumPath)); err != nil {
        return err
    }

    // ... manifest write continues ...
}
```

## Files Modified
- internal/storage/meeting.go
- internal/storage/adapters/s3.go
- internal/storage/adapters/local.go

## Summary Table

| Bug # | File | Severity | Status |
|-------|------|----------|--------|
| 1 | meeting.go | CRITICAL | Fixed |
| 2 | meeting.go | HIGH | Fixed |
| 3 | s3.go | CRITICAL | Fixed |
| 4 | meeting.go | MEDIUM | Fixed |
| 5 | local.go | MEDIUM | Fixed |
| 6 | local.go | LOW | Fixed |
| 7 | meeting.go | MEDIUM | Fixed |