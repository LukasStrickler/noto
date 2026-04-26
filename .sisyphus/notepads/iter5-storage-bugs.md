# Iteration 5: Storage Deep Bug Report

## Bug 1: CopyAudioToVersion Missing fsync - Durable Write Not Guaranteed
**File:** internal/storage/meeting.go
**Lines:** 275-280
**Severity:** CRITICAL

```go
if err := dstFile.Close(); err != nil {
    os.Remove(versionAudioPath)
    return ErrWriteFailed(versionAudioPath, err)
}

return nil
```

**Description:** `CopyAudioToVersion` writes audio data via `io.Copy` and then closes the destination file, but never calls `fsyncFile` to ensure the data is durably persisted to disk. Unlike `WriteManifest`, `WriteTranscript`, and `WriteSummary` which all call `fsyncFile` before rename, this function skips that step. Additionally, there is no `fsyncDir` call after the operation to ensure directory metadata is updated.

**Impact:** System crash after file close but before data hits disk could result in zero-length or truncated audio files being treated as valid recordings.

---

## Bug 2: CopyAudioToVersion No fsyncDir After Write
**File:** internal/storage/meeting.go
**Lines:** 264-280
**Severity:** HIGH

```go
dstFile, err := os.Create(versionAudioPath)
if err != nil {
    return ErrWriteFailed(versionAudioPath, err)
}
defer dstFile.Close()

if _, err := io.Copy(dstFile, srcFile); err != nil {
    os.Remove(versionAudioPath)
    return ErrWriteFailed(versionAudioPath, err)
}

if err := dstFile.Close(); err != nil {
    os.Remove(versionAudioPath)
    return ErrWriteFailed(versionAudioPath, err)
}
// NO fsyncDir call here!
return nil
```

**Description:** Unlike other write functions (WriteManifest, WriteTranscript, WriteSummary) which all call `fsyncDir` after their respective renames/closes, `CopyAudioToVersion` makes no `fsyncDir` call. Even if `fsyncFile` were added (Bug 1), the directory entry itself might not be synced, causing filesystem inconsistencies after crashes.

**Impact:** Directory entry for newly copied file may not exist after crash, leading to "file not found" errors for valid recordings.

---

## Bug 3: GetObjectWithChecksumVerification Buffers Entire Object in Memory
**File:** internal/storage/adapters/s3.go
**Lines:** 518-544
**Severity:** CRITICAL

```go
type checksumWriter struct {
    w   io.WriterAt
    buf *bytes.Buffer
}

func (c *checksumWriter) WriteAt(p []byte, off int64) (n int, err error) {
    c.buf.Write(p)
    return c.w.WriteAt(p, off)
}

func GetObjectWithChecksumVerification(ctx context.Context, adapter SyncAdapter, key string, dest io.WriterAt, expectedChecksum string) error {
    wrapper := &checksumWriter{
        w:   dest,
        buf: &bytes.Buffer{},
    }
    err := adapter.GetObject(ctx, key, wrapper)
    // ... checksum computed from wrapper.buf.Bytes()
}
```

**Description:** `checksumWriter` accumulates ALL written data in an unbounded `bytes.Buffer`. For large objects (e.g., 500MB audio files), this causes memory exhaustion. The entire object is loaded into RAM before checksum verification completes.

**Impact:** OOM crashes when downloading large audio files. Attackers could trigger OOM by requesting large files with invalid checksums, forcing the full download into memory before the checksum failure is detected.

---

## Bug 4: ComputeFileChecksum Reads Entire File Into Memory
**File:** internal/storage/meeting.go
**Lines:** 333-340
**Severity:** MEDIUM

```go
func ComputeFileChecksum(path string) (string, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return "", ErrReadFailed(path, err)
    }
    hash := sha256.Sum256(data)
    return "sha256:" + fmt.Sprintf("%x", hash), nil
}
```

**Description:** Uses `os.ReadFile` which loads the entire file into memory. For large audio files (hundreds of MB), this causes high memory pressure and potential OOM.

**Impact:** Memory exhaustion for large audio files during checksum computation. Could cause system slowdown or crashes on memory-constrained systems.

---

## Bug 5: safeJoin Returns Unresolved Path Despite Security Check
**File:** internal/storage/adapters/local.go
**Lines:** 34-54
**Severity:** MEDIUM

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

    return fullPath, nil  // <-- Returns unresolved path!
}
```

**Description:** The security check validates `resolvedPath` (symlinks resolved), but returns `fullPath` (unresolved). If an attacker can create a symlink at `fullPath` AFTER `safeJoin` returns but BEFORE the file is actually accessed, they could bypass the traversal check. Additionally, if `filepath.EvalSymlinks` fails with `os.IsNotExist(err)`, `resolvedPath` stays as `cleanPath` which is clean but not verified to exist or be safe.

**Impact:** Time-of-check-time-of-use (TOCTOU) race condition. If attacker has write access to basePath, they could create symlinks to bypass the path safety check.

---

## Bug 6: LocalAdapter PutObject Silent Close Failure
**File:** internal/storage/adapters/local.go
**Lines:** 67-80
**Severity:** LOW

```go
file, err := os.Create(fullPath)
if err != nil {
    return ErrUpload(key, err)
}
defer file.Close()

if _, err := io.Copy(file, body); err != nil {
    os.Remove(fullPath)
    return ErrUpload(key, err)
}

if err := file.Close(); err != nil {
    return ErrUpload(key, err)
}
```

**Description:** After `io.Copy` succeeds (data is written and synced internally by the OS), `file.Close()` is called. If this fails, an error is returned even though the upload is actually successful - the data was already written to the OS page cache. The user would see an error but the file would be valid on disk.

**Impact:** False negative - returns error to user even when file is successfully written. Could cause unnecessary retry attempts or confusion.

---

## Bug 7: WriteVersionManifest No Checksum for Version Manifest
**File:** internal/storage/meeting.go
**Lines:** 283-313
**Severity:** MEDIUM

```go
func WriteVersionManifest(layout DirectoryLayout, versionID string, m *artifacts.MeetingManifest) error {
    // ...
    tmpPath := filepath.Join(layout.TmpDir, "version_manifest.tmp")
    if err := os.WriteFile(tmpPath, data, 0644); err != nil {
        return ErrWriteFailed(tmpPath, err)
    }
    if err := fsyncFile(tmpPath); err != nil {
        os.Remove(tmpPath)
        return ErrWriteFailed(tmpPath, err)
    }

    finalPath := layout.VersionManifestPath(versionID)
    if err := os.Rename(tmpPath, finalPath); err != nil {
        os.Remove(tmpPath)
        return ErrAtomicWrite(finalPath, err)
    }
    // NO checksum file written for version manifest!
    if err := fsyncDir(filepath.Dir(finalPath)); err != nil {
        return err
    }

    return nil
}
```

**Description:** Unlike `WriteManifest` which writes both the manifest AND a checksum file, `WriteVersionManifest` only writes the manifest JSON without any accompanying checksum. There's no `VersionChecksumPath` helper and no checksum is computed or stored for version manifests.

**Impact:** No integrity verification for version manifests. Corrupted version manifests would not be detected.

---

## Summary Table

| Bug # | File | Line(s) | Severity | Type |
|-------|------|---------|----------|------|
| 1 | meeting.go | 275-280 | CRITICAL | Durability - Missing fsync |
| 2 | meeting.go | 264-280 | HIGH | Durability - Missing fsyncDir |
| 3 | s3.go | 518-544 | CRITICAL | Memory exhaustion |
| 4 | meeting.go | 333-340 | MEDIUM | Memory usage |
| 5 | local.go | 53 | MEDIUM | TOCTOU race condition |
| 6 | local.go | 78-80 | LOW | False error reporting |
| 7 | meeting.go | 283-313 | MEDIUM | Missing checksum verification |

---

## Root Cause Analysis

The primary issues stem from:
1. **Inconsistent fsync patterns**: CopyAudioToVersion was not updated when fsync patterns were added to other write functions
2. **Missing checksum infrastructure**: Version manifests lack the checksum infrastructure that main manifest has
3. **Unbounded buffer growth**: checksumWriter accumulates data without size limits
4. **Memory-first approach**: ComputeFileChecksum and CopyAudioToVersion read entire files into memory instead of streaming

## Comparison with iter4 Findings

The iter4 report correctly identified:
- Path traversal in ListObjects (fixed with safeJoin)
- Missing fsyncDir after checksum rename (fixed)
- io.Copy streaming added (fixed)

New issues found in iter5:
- Missing fsync after CopyAudioToVersion (wasn't in iter4 scope)
- Unbounded buffer in checksumWriter (new code from iter4)
- TOCTOU in safeJoin (edge case not caught before)