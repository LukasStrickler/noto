# Storage Bug Fixes

## Bugs Fixed

### Bug 1: Path Traversal in ListObjects (local.go:149)
**Severity:** CRITICAL  
**Fix:** Used `safeJoin(a.basePath, prefix)` instead of direct `filepath.Join`. Returns error if path attempts to escape basePath.

```go
fullPath, err := safeJoin(a.basePath, prefix)
if err != nil {
    return nil, ErrList(prefix, err)
}
```

### Bug 2: Keys Returned with Traversal Sequences (local.go:170-175)
**Severity:** HIGH  
**Fix:** Added `filepath.Clean(key)` to remove traversal sequences from returned keys.

```go
key := filepath.Join(prefix, entry.Name())
key = filepath.Clean(key)
```

### Bug 7: Missing fsyncDir After Checksum Rename (meeting.go:49-52)
**Severity:** HIGH  
**Fix:** Added `fsyncDir(filepath.Dir(checksumPath))` after checksum rename but before manifest rename to ensure atomic write ordering.

```go
if err := os.Rename(tmpChecksumPath, checksumPath); err != nil {
    os.Remove(tmpChecksumPath)
    return ErrAtomicWrite(checksumPath, err)
}
if err := fsyncDir(filepath.Dir(checksumPath)); err != nil {
    return err
}
```

### Bug 8: CopyAudioToVersion Reads Entire File Into Memory (meeting.go:255-261)
**Severity:** MEDIUM  
**Fix:** Replaced `os.ReadFile` + `os.WriteFile` with streaming `io.Copy` to avoid memory exhaustion on large audio files.

```go
srcFile, err := os.Open(srcAudioPath)
defer srcFile.Close()

dstFile, err := os.Create(versionAudioPath)
defer dstFile.Close()

if _, err := io.Copy(dstFile, srcFile); err != nil {
    os.Remove(versionAudioPath)
    return ErrWriteFailed(versionAudioPath, err)
}
```

## Files Modified
- internal/storage/adapters/local.go
- internal/storage/meeting.go
