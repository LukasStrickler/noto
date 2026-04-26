# Fix Report: Critical Issues in `internal/artifacts/*.go`

**Date:** 2026-04-26  
**Files Modified:** `writer.go`, `version.go`

---

## Bug 1: Checksum Written Before Manifest in `WritePipeline.writeManifestAtomic`

**Status:** ✅ FIXED

**File:** `writer.go`  
**Lines:** 207-251 (previously 207-244)

**Problem:** The `WritePipeline.writeManifestAtomic` wrote the checksum file BEFORE the manifest file, violating atomicity. If manifest write failed after checksum was committed, verification would fail with a checksum pointing to a non-existent manifest.

**Fix:** Reordered operations to write manifest FIRST, then checksum - matching the correct pattern in `ManifestWriter.writeManifestAtomic` (lines 37-80).

**Changes:**
- Manifest is now written to temp file, fsync'd, renamed to final path, and directory fsync'd (lines 216-231)
- Checksum is then written to temp file, fsync'd, renamed to final path, and directory fsync'd (lines 233-248)
- Added fsync of manifest directory before writing checksum (line 229-231)

**Verification:** Manifest write is durable before checksum exists; if manifest write fails, no checksum is left orphaned.

---

## Bug 2: Version Artifact Creates Copy Files From Non-Existent Paths

**Status:** ✅ FIXED

**File:** `writer.go`  
**Lines:** 292-330 (previously 285-306)

**Problems:**
1. Both `layout.ManifestPath` and `layout.VersionManifestPath(currentVersionID)` copied to same destination `"manifest.json"` - second copy overwrote first
2. Missing files were silently skipped with `continue` - no warning

**Fix:**
- Added `required bool` field to `artifactsToCopy` struct to distinguish required vs optional artifacts
- Changed second entry to copy to `"previous_manifest.json"` instead of overwriting `"manifest.json"`
- Added explicit `os.Stat()` check before attempting copy (lines 309-318)
- Added `missingFiles` slice to track missing required files
- Added warning output for missing required files (lines 325-330)

**Changes:**
```go
artifactsToCopy := []struct {
    srcPath string
    dstPath string
    required bool  // NEW: distinguish required vs optional
}{
    {layout.ManifestPath, "manifest.json", true},  // required
    {layout.VersionManifestPath(currentVersionID), "previous_manifest.json", false},  // renamed dst
    {layout.VersionTranscriptPath(currentVersionID), "transcript.diarized.json", false},
    {layout.VersionSummaryPath(currentVersionID), "summary.v1.md", false},
    {layout.VersionChecksumPath(currentVersionID), "checksum.sha256", false},
}
```

---

## Bug 3: Hardcoded Audio Metadata Not From Actual File

**Status:** ✅ PARTIALLY FIXED

**File:** `writer.go`  
**Lines:** 628, 653 (previously 600-620)

**Problem:** `ImportAudio.Import` hardcoded audio metadata:
- `Path` was hardcoded to `"audio/recording.m4a"` regardless of actual file extension
- `DurationSeconds`, `Codec`, `Channels`, `SampleRateHz` were hardcoded assumptions

**Fix:** Changed Path to use actual file extension.

**Changes:**
- `Path: "audio/recording.m4a"` → `Path: filepath.Join("audio", "recording"+ext)`
- `versionAudioPath` also updated to use actual extension: `filepath.Join(versionAudioDir, "recording"+ext)`

**Limitations:** Full audio analysis (duration, codec, channels, sample rate) would require an audio analysis library (e.g., `github.com/tcolgate/mp3` for MP3 decoding) which was not added per constraint "Do NOT add new dependencies". The `Format` field correctly uses the derived format from the file extension.

---

## Bug 4: Empty Schema Version Passes Validation

**Status:** ✅ FIXED

**File:** `version.go`  
**Lines:** 19-32 (previously 19-29)

**Problem:** `PromptVersion.Validate()` only checked that `schema_version` was non-empty, not that it equals `"prompt.v1"`. Any value like `"prompt.v2"`, `"foo"`, or `"PROMPT.V1"` would pass validation.

**Fix:** Added explicit schema version value check matching the pattern in `MeetingManifest.Validate()`.

**Changes:**
```go
func (p *PromptVersion) Validate() *notoerr.Error {
    if p.SchemaVersion == "" {
        return NewMissingFieldError("schema_version")
    }
    // NEW: Check that schema_version has correct value
    if p.SchemaVersion != "prompt.v1" {
        return notoerr.New(ErrCodeValidationFailed, "schema_version must be prompt.v1", map[string]any{"schema_version": p.SchemaVersion})
    }
    // ... rest of validation
}
```

---

## Bug 5: Checksum File Orphaned on Manifest Write Failure

**Status:** ✅ ADDRESSED (via Bug 1 fix)

**File:** `writer.go`

**Problem:** Atomicity violation from Bug 1 - if manifest write failed after checksum was committed, checksum file would be left orphaned.

**Resolution:** The fix for Bug 1 (reordering manifest-then-checksum) resolves this issue. Now:
- If manifest write fails before commit, no checksum exists
- If manifest write succeeds and checksum write fails, manifest exists without checksum (verifiable state)

---

## Summary of Changes

| Bug | File | Lines | Status |
|-----|------|-------|--------|
| 1 | writer.go | 207-251 | ✅ FIXED |
| 2 | writer.go | 292-330 | ✅ FIXED |
| 3 | writer.go | 628, 653 | ✅ PARTIAL (Path only) |
| 4 | version.go | 19-32 | ✅ FIXED |
| 5 | writer.go | - | ✅ ADDRESSED (via Bug 1) |

---

## Files Modified

1. **`internal/artifacts/writer.go`** - 5 fixes applied
2. **`internal/artifacts/version.go`** - 1 fix applied

## Constraints Followed

- ✅ Did NOT add new dependencies
- ✅ Did NOT change overall architecture
- ✅ Kept fixes targeted and minimal
