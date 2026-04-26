# iter4-artifact-bugs: Internal Artifacts Bug Report

## Summary
Bug hunting in internal/artifacts/ directory found **9 bugs** across writer.go, manifest.go, and related files.

---

## BUG 1: Audio Import Metadata Write Missing fsync (CRITICAL)

**File:** internal/artifacts/writer.go:613-621

**Code:**
```go
tmpPath := filepath.Join(layout.TmpDir, "audio_meta.tmp")
if err := os.WriteFile(tmpPath, audioMetaData, 0644); err != nil {
    return nil, storage.ErrWriteFailed(tmpPath, err)
}

if err := os.Rename(tmpPath, audioMetaPath); err != nil {
    os.Remove(tmpPath)
    return nil, storage.ErrAtomicWrite(audioMetaPath, err)
}
```

**Issue:** Missing `fsyncFile(tmpPath)` before rename. The audio.json metadata is NOT guaranteed durable before rename completes.

**Severity:** CRITICAL

---

## BUG 2: Audio Import Metadata Missing fsyncDir After Rename (CRITICAL)

**File:** internal/artifacts/writer.go:618-621

**Issue:** After rename at line 618, there is no `fsyncDir(filepath.Dir(audioMetaPath))` call. The directory entry for audio.json may not be durable.

**Severity:** CRITICAL

---

## BUG 3: Audio File Copy Not Atomic (CRITICAL)

**File:** internal/artifacts/writer.go:603-605

**Code:**
```go
versionAudioPath := filepath.Join(versionAudioDir, "recording.m4a")
if err := os.WriteFile(versionAudioPath, data, 0644); err != nil {
    return nil, storage.ErrWriteFailed(versionAudioPath, err)
}
```

**Issue:** Audio file is written DIRECTLY to final path, not via temp file + rename pattern. If crash/power loss during write, corrupted/large file left at destination with no recovery path.

**Severity:** CRITICAL

---

## BUG 4: copyFile() Function Not Atomic (CRITICAL)

**File:** internal/artifacts/writer.go:380-391

**Code:**
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

**Issues:**
- Reads entire file into memory before writing - large files cause memory pressure
- No fsync on destination before returning success
- If crash after WriteFile but before return, dst is partial/corrupt
- No temp file + atomic rename pattern used

**Severity:** CRITICAL

---

## BUG 5: Version Creation - copyFile Calls Not Atomic (HIGH)

**File:** internal/artifacts/writer.go:285-295

**Code:**
```go
for _, artifact := range artifactsToCopy {
    srcPath := artifact.srcPath
    dstPath := filepath.Join(versionDir, artifact.dstPath)

    if err := copyFile(srcPath, dstPath); err != nil {
        if os.IsNotExist(err) {
            continue
        }
        return "", err
    }
}
```

**Issue:** Uses copyFile() which is not atomic. If crash during version creation, some artifacts may be partial copies.

**Severity:** HIGH

---

## BUG 6: Manifest Checksum Written BEFORE Manifest Data (HIGH)

**File:** internal/artifacts/writer.go:42-72 (writeManifestAtomic)

**Sequence:**
1. Line 42: Compute checksum of manifest data
2. Lines 46-52: Write checksum to disk and fsync
3. Lines 58-70: Write manifest data to disk and fsync

**Issue:** Checksum file is durable BEFORE manifest data is written. If crash after checksum fsync but before manifest write completes, the checksum file references data that doesn't exist on disk.

**Severity:** HIGH

---

## BUG 7: Checksums.json Written Before Manifest in WriteAll (HIGH)

**File:** internal/artifacts/writer.go:105-154

**Sequence:**
1. Lines 105-127: Write all artifact files
2. Lines 129-152: Write checksums.json with fsync
3. Line 154: Write manifest

**Issue:** If crash after checksums.json is durable but before manifest is written, `VerifyAll` will fail checksum verification when it reads the manifest and computes a NEW checksum that doesn't match the already-written checksums.json.

**Severity:** HIGH

---

## BUG 8: TOCTOU in verifyManifestChecksum (MEDIUM)

**File:** internal/artifacts/writer.go:482-509

**Code:**
```go
func (vc *VerifyChecksums) verifyManifestChecksum(layout storage.DirectoryLayout) error {
    manifestPath := layout.ManifestPath
    checksumPath := layout.ChecksumPath

    expectedData, err := os.ReadFile(checksumPath)
    // ... error handling ...

    manifestData, err := os.ReadFile(manifestPath)
    // ... error handling ...

    if err := VerifyChecksum(manifestData, expected); err != nil {
        // ...
    }
}
```

**Issue:** Time-of-check to time-of-use vulnerability. Between reading checksumPath and manifestPath, both files could be modified by a concurrent writer. On weakly consistent filesystems, the manifest could change between reads.

**Severity:** MEDIUM

---

## BUG 9: Import Audio Reads File Before Verifying Write Succeeds (MEDIUM)

**File:** internal/artifacts/writer.go:541-605

**Code:**
```go
func (ia *ImportAudio) Import(meetingID uuid.UUID, audioPath string) (*ImportResult, error) {
    data, err := os.ReadFile(audioPath)  // Line 542
    // ...
    versionAudioPath := filepath.Join(versionAudioDir, "recording.m4a")
    if err := os.WriteFile(versionAudioPath, data, 0644); err != nil {  // Line 603
        return nil, storage.ErrWriteFailed(versionAudioPath, err)
    }
```

**Issue:** 
1. Reads entire audio file into memory (large file = memory pressure)
2. Writes to final path directly (not atomic)
3. No verification that written data matches read data
4. If write fails silently (disk full but OS reports success?), data corruption goes undetected

**Severity:** MEDIUM

---

## Bug Count by Severity

| Severity | Count |
|----------|-------|
| CRITICAL | 4 |
| HIGH     | 3 |
| MEDIUM   | 2 |
| **TOTAL**| **9** |

## Files Reviewed

- writer.go (627 lines)
- manifest.go (77 lines)
- checksum.go (86 lines)
- artifact.go (28 lines)
- audio.go (71 lines)
- transcript.go (113 lines)
- summary.go (94 lines)
- version.go (30 lines)
- errors.go (38 lines)
- writer_test.go (493 lines)
- artifacts_test.go (631 lines)
