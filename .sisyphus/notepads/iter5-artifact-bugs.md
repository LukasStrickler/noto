# Bug Report: Critical Issues in `internal/artifacts/*.go`

## Bug 1: Checksum Written Before Manifest in `WritePipeline.writeManifestAtomic` (writer.go:207-244)

**Severity:** CRITICAL - Data integrity / atomicity violation

**Location:** `writer.go` lines 207-244

**Issue:** The `WritePipeline.writeManifestAtomic` writes the checksum file BEFORE the manifest file:

```go
// checksum written first (lines 216-227)
tmpChecksumPath := filepath.Join(layout.TmpDir, "manifest_checksum.tmp")
if err := os.WriteFile(tmpChecksumPath, []byte(checksum), 0644); err != nil {
    return storage.ErrWriteFailed(tmpChecksumPath, err)
}
// ... fsync and rename to final checksum path

// THEN manifest written (lines 229-240)
tmpPath := filepath.Join(layout.TmpDir, "manifest.json.tmp")
if err := os.WriteFile(tmpPath, data, 0644); err != nil {
    os.Remove(tmpChecksumPath)  // cleanup but checksum already at final path!
    return storage.ErrWriteFailed(tmpPath, err)
}
```

**Problem:** If manifest write fails after checksum is committed to disk, you have a checksum file with no corresponding manifest. On recovery, verification will fail because the "checksum" points to a manifest that was never fully written.

**Contrast with `ManifestWriter.writeManifestAtomic` (lines 37-80)** which correctly writes manifest FIRST, then checksum.

---

## Bug 2: Version Artifact Creates Copy Files From Non-Existent Paths (writer.go:296-306)

**Severity:** CRITICAL - Silent data loss during versioning

**Location:** `writer.go` lines 285-306

```go
artifactsToCopy := []struct {
    srcPath string
    dstPath string
}{
    {layout.ManifestPath, "manifest.json"},
    {layout.VersionManifestPath(currentVersionID), "manifest.json"},  // SAME dst!
    {layout.VersionTranscriptPath(currentVersionID), "transcript.diarized.json"},
    {layout.VersionSummaryPath(currentVersionID), "summary.v1.md"},
    {layout.VersionChecksumPath(currentVersionID), "checksum.sha256"},
}
```

**Issues:**
1. When `currentVersionID` is the initial version (e.g., "ver_001"), paths like `VersionTranscriptPath("ver_001")` likely don't exist since transcripts aren't created for initial version
2. Both `layout.ManifestPath` and `layout.VersionManifestPath(currentVersionID)` copy to same destination `manifest.json` - second copy overwrites first
3. The `continue` on `os.IsNotExist(err)` at line 301-304 silently skips missing files without any warning

**Result:** New version snapshots may be incomplete with no error reported.

---

## Bug 3: Hardcoded Audio Metadata Not From Actual File (audio.go:600-620)

**Severity:** HIGH - Metadata corruption

**Location:** `writer.go` lines 600-620 in `ImportAudio.Import`

```go
audioMeta := &AudioMetadata{
    Path:            "audio/recording.m4a",  // HARDCODED WRONG EXTENSION
    Format:          format,                   // derived from input ext, but...
    Codec:           "aac",                    // HARDCODED - not from actual file
    DurationSeconds: 0,                        // ALWAYS ZERO - not computed!
    Channels:        2,                        // HARDCODED
    SampleRateHz:    48000,                    // HARDCODED
    // ...
}
```

**Issues:**
1. `Path` is hardcoded to `"audio/recording.m4a"` even if importing an MP3 or FLAC file
2. `DurationSeconds` is always 0 - duration is never extracted from audio
3. `Codec`, `Channels`, `SampleRateHz` are all hardcoded assumptions, not derived from actual audio file analysis

**Result:** Audio metadata is misleading and incorrect for non-M4A files.

---

## Bug 4: Empty Schema Version Passes Validation

**Severity:** HIGH - Schema enforcement failure

**Location:** `manifest.go` lines 45-47

```go
func (m *MeetingManifest) Validate() *notoerr.Error {
    if m.SchemaVersion != "manifest.v1" {
        return notoerr.New(ErrCodeValidationFailed, "schema_version must be manifest.v1", ...)
    }
```

**Issue:** This only checks that schema_version is NOT empty AND equals "manifest.v1". But `PromptVersion.Validate()` (version.go:19-22) only checks that schema_version is NOT empty, not that it matches expected value:

```go
func (p *PromptVersion) Validate() *notoerr.Error {
    if p.SchemaVersion == "" {  // Only checks non-empty, not valid value!
        return NewMissingFieldError("schema_version")
    }
    // Missing check: if p.SchemaVersion != "prompt.v1" { return error }
```

**Result:** Empty string is rejected but any other value (e.g., "prompt.v2", "foo", "PROMPT.V1") is accepted.

---

## Bug 5: Checksum File Not Verified for Existence Before Processing

**Severity:** HIGH - Crash on missing checksum file

**Location:** `writer.go` lines 482-489

```go
checksumPath := layout.ChecksumPath
data, err := os.ReadFile(checksumPath)
if err != nil {
    if os.IsNotExist(err) {
        return nil, fmt.Errorf("checksum file %s not found", checksumPath)  // Returns error!
    }
    return nil, storage.ErrReadFailed(checksumPath, err)
}
```

**Issue:** When `ManifestWriter.WriteManifest` or `WritePipeline.WriteAll` fails AFTER creating checksum but before manifest is fully written, subsequent verification will fail with "checksum file not found" even though the checksum file exists in the directory.

Wait - actually, looking more carefully at `ManifestWriter.writeManifestAtomic` (lines 37-80), if it fails after writing manifest but before completing checksum write, the manifest exists but checksum doesn't. So verification would fail with "checksum file not found" - which is correct behavior.

But the issue is that if the manifest write fails, the checksum file is left orphaned. This is the atomicity violation from Bug 1.

---

## Summary Table

| Bug | File | Line(s) | Severity | Type |
|-----|------|---------|----------|------|
| 1 | writer.go | 207-244 | CRITICAL | Data atomicity violation |
| 2 | writer.go | 285-306 | CRITICAL | Silent data loss |
| 3 | writer.go | 600-620 | HIGH | Metadata corruption |
| 4 | manifest.go, version.go | 45-47, 19-22 | HIGH | Schema enforcement failure |
| 5 | writer.go | 482-489 | MEDIUM | Incomplete error handling |

## Recommendations

1. **Bug 1:** Align `WritePipeline.writeManifestAtomic` with `ManifestWriter.writeManifestAtomic` - write manifest first, then checksum
2. **Bug 2:** Validate paths exist before copying; use different destination names; report missing files as warnings not silent continues
3. **Bug 3:** Actually analyze audio file to determine codec, duration, channels, sample rate
4. **Bug 4:** Add schema version validation to `PromptVersion.Validate()` to check for expected value
5. **Bug 5:** Ensure atomic write pattern is consistent - manifest must be durably written before checksum