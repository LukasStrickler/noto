# iter4-artifact-fixes: Artifacts Bug Fixes

## Summary
Fixed **7 bugs** in internal/artifacts/writer.go (6 CRITICAL/HIGH severity bugs from the 9 total bugs found).

---

## BUG 1 Fixed: Audio Metadata Missing fsyncFile Before Rename

**File:** writer.go:613-621 (now ~652-663)

**Before:**
```go
if err := os.WriteFile(tmpPath, audioMetaData, 0644); err != nil {
    return nil, storage.ErrWriteFailed(tmpPath, err)
}
if err := os.Rename(tmpPath, audioMetaPath); err != nil {
```

**After:**
```go
if err := os.WriteFile(tmpPath, audioMetaData, 0644); err != nil {
    return nil, storage.ErrWriteFailed(tmpPath, err)
}
if err := fsyncFile(tmpPath); err != nil {
    os.Remove(tmpPath)
    return nil, storage.ErrWriteFailed(tmpPath, err)
}
if err := os.Rename(tmpPath, audioMetaPath); err != nil {
```

---

## BUG 2 Fixed: Audio Metadata Missing fsyncDir After Rename

**File:** writer.go:618-621 (now ~660-664)

**After Rename, added:**
```go
if err := fsyncDir(filepath.Dir(audioMetaPath)); err != nil {
    return nil, err
}
```

---

## BUG 3 Fixed: Audio File Written Directly to Final Path

**File:** writer.go:603-605 (now ~629-644)

**Before:**
```go
versionAudioPath := filepath.Join(versionAudioDir, "recording.m4a")
if err := os.WriteFile(versionAudioPath, data, 0644); err != nil {
    return nil, storage.ErrWriteFailed(versionAudioPath, err)
}
```

**After:** Uses temp file + atomic rename pattern:
```go
versionAudioPath := filepath.Join(versionAudioDir, "recording.m4a")
tmpAudioPath := filepath.Join(layout.TmpDir, "audio_recording.tmp")
if err := os.WriteFile(tmpAudioPath, data, 0644); err != nil {
    return nil, storage.ErrWriteFailed(tmpAudioPath, err)
}
if err := fsyncFile(tmpAudioPath); err != nil {
    os.Remove(tmpAudioPath)
    return nil, storage.ErrWriteFailed(tmpAudioPath, err)
}
if err := os.Rename(tmpAudioPath, versionAudioPath); err != nil {
    os.Remove(tmpAudioPath)
    return nil, storage.ErrAtomicWrite(versionAudioPath, err)
}
if err := fsyncDir(versionAudioDir); err != nil {
    return nil, err
}
```

---

## BUG 4 Fixed: copyFile() Not Atomic, No fsync

**File:** writer.go:380-391 (now ~395-417)

**Before:**
```go
func copyFile(src, dst string) error {
    data, err := os.ReadFile(src)
    if err != nil {
        return err
    }
    if err := os.WriteFile(dst, data, 0644); err != nil {
        return err
    }
    return nil
}
```

**After:** Uses temp file + atomic rename + fsync:
```go
func copyFile(src, dst string) error {
    data, err := os.ReadFile(src)
    if err != nil {
        return err
    }

    tmpPath := dst + ".tmp"
    if err := os.WriteFile(tmpPath, data, 0644); err != nil {
        return err
    }
    if err := fsyncFile(tmpPath); err != nil {
        os.Remove(tmpPath)
        return err
    }
    if err := os.Rename(tmpPath, dst); err != nil {
        os.Remove(tmpPath)
        return err
    }
    if err := fsyncDir(filepath.Dir(dst)); err != nil {
        return err
    }

    return nil
}
```

---

## BUG 6 Fixed: Manifest Checksum Written BEFORE Manifest Data

**File:** writer.go:42-72 (now ~37-80)

**Before:** checksum written first, then manifest data
**After:** manifest data written first, then checksum

Order is now:
1. Write manifest.json.tmp → fsync → rename to manifestPath → fsyncDir
2. Write manifest_checksum.tmp → fsync → rename to checksumPath → fsyncDir

**Important:** This same fix was applied to `VersionArtifact.writeManifestAtomic` (~lines 353-390)

---

## BUG 7 Fixed: Checksums.json Written Before Manifest in WriteAll

**File:** writer.go:105-154 (now ~106-166)

**Before:** artifacts → checksums.json → manifest
**After:** artifacts → manifest → checksums.json

Order is now:
1. Write all artifacts with atomic pattern (temp + fsync + rename + fsyncDir)
2. Write manifest via writeManifestAtomic (data first, then checksum)
3. Write checksums.json

---

## Bugs NOT Fixed (out of scope or MEDIUM severity)

- **BUG 5:** Version creation uses copyFile - atomic fix applied via copyFile fix (BUG 4)
- **BUG 8:** TOCTOU in verifyManifestChecksum - MEDIUM severity, not in task scope
- **BUG 9:** Import audio reads file before verifying write - MEDIUM severity, not in task scope

---

## Files Modified

- `internal/artifacts/writer.go` - All 7 fixes applied

## Verification

Build could not be verified as Go toolchain is not installed in this environment. The fixes follow established patterns in the codebase (temp file + fsync + rename + fsyncDir).

---

## Key Patterns Used

1. **Atomic file write:**
   ```go
   tmpPath := path + ".tmp"
   os.WriteFile(tmpPath, data, 0644)
   fsyncFile(tmpPath)
   os.Rename(tmpPath, path)
   fsyncDir(filepath.Dir(path))
   ```

2. **Data-first ordering:** Always write data before checksums - the checksum verifies data that exists, not data that might exist.