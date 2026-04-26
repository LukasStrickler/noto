# CLI Bugs Report (iter4-cli-bugs.md)

## Bugs Found in internal/cli/cli.go

### Bug 1: CRITICAL - `importTranscript` returns pointer to stack-local variable (panic/crash)

**Location:** Line 684

**Code:**
```go
return writeJSON(a.out, map[string]any{
    "ok":         true,
    "meeting_id": meetingID.String(),
    "transcript": &transcript,  // BUG: &transcript is pointer to stack variable
})
```

**Problem:** `transcript` is a local variable of type `artifacts.Transcript`. Taking its address and returning it via `writeJSON` means the pointer escapes to the heap, but Go's escape analysis and the subsequent JSON serialization may cause the pointer to reference stale stack memory.

**Severity:** Critical - potential panic

**Fix:** Remove the `&` - pass the value directly since `writeJSON` takes `any`:
```go
return writeJSON(a.out, map[string]any{
    "ok":         true,
    "meeting_id": meetingID.String(),
    "transcript": transcript,
})
```

---

### Bug 2: CRITICAL - `summary` command returns pointer to stack-local variable (panic/crash)

**Location:** Line 1117

**Code:**
```go
return writeJSON(a.out, map[string]any{
    "ok":         true,
    "meeting_id": meetingIDStr,
    "summary":    &summary,  // BUG: &summary is pointer to stack variable
})
```

**Problem:** Same as Bug 1 - `summary` is a local variable of type `artifacts.Summary`. Taking its address and passing to `writeJSON` creates the same escape analysis issue.

**Severity:** Critical - potential panic

**Fix:** Pass the value directly:
```go
return writeJSON(a.out, map[string]any{
    "ok":         true,
    "meeting_id": meetingIDStr,
    "summary":    summary,
})
```

---

### Bug 3: CRITICAL - `stop` command has nil pointer dereference path

**Location:** Lines 436-514

**Code:**
```go
if stopResult != nil && len(audioData) > 0 {
    refs, err := storage.ListMeetings(a.recordingsDir)
    if err == nil && len(refs) > 0 {
        lastMeeting := refs[0]
        layout, err := storage.LayoutFor(a.recordingsDir, lastMeeting.MeetingID)
        if err == nil {
            // ... all the audio processing ...
        }
    }
}
```

**Problem:** If `stopResult == nil` OR `len(audioData) == 0`, the entire audio processing block is skipped, but the function returns `nil` without checking if actual work was done. More critically, if `refs` is empty or `err != nil`, silently continues without any error or warning.

**Severity:** High - logic error, missing error propagation

**Fix:** Add proper error handling when no meetings found or layout fails:
```go
if stopResult != nil && len(audioData) > 0 {
    refs, err := storage.ListMeetings(a.recordingsDir)
    if err != nil {
        return fmt.Errorf("failed to list meetings: %w", err)
    }
    if len(refs) == 0 {
        return fmt.Errorf("no meetings found to associate audio with")
    }
    lastMeeting := refs[0]
    // ... rest of logic ...
} else {
    if stopResult == nil {
        fmt.Fprintf(a.out, "Note: No recording stopped.\n")
    } else if len(audioData) == 0 {
        fmt.Fprintf(a.out, "Note: No audio data captured.\n")
    }
}
```

---

### Bug 4: HIGH - `search` command may include non-JSON args incorrectly

**Location:** Lines 806-837

**Code:**
```go
func (a app) search(ctx context.Context, args []string) error {
    query := extractSearchQuery(args)
    if query == "" {
        return notoerr.New("missing_query", "Usage: noto search --json \"query\".", nil)
    }
    // ...
}

func extractSearchQuery(args []string) string {
    for i, arg := range args {
        if arg == "--json" || arg == "-j" {
            continue
        }
        if !strings.HasPrefix(arg, "-") {
            return arg
        }
    }
    return ""
}
```

**Problem:** `extractSearchQuery` returns the first non-flag argument. But if the query contains spaces and the user provides it without quotes (e.g., `noto search pricing decision`), only "pricing" is captured, not "pricing decision". The error message also has a typo: `"query"` should end with a closing quote.

**Severity:** Medium - degraded UX but not a crash

**Fix:** 
1. Fix the typo in error message at line 809: `"query\""` should be `"query\""` 
2. Better approach: collect remaining positional args as the query string

---

### Bug 5: MEDIUM - `importAudio` missing "ok": false in error case (inconsistent API)

**Location:** Lines 592-600

**Code:**
```go
if err != nil {
    fmt.Fprintf(a.errOut, "Transcription failed: %v\n", err)
    return writeJSON(a.out, map[string]any{
        "ok":         false,
        "meeting_id":  meetingID.String(),
        "audio":      result.AudioMetadata,
        "transcribed": false,
        "error":      err.Error(),
    })
}
```

**Problem:** The JSON response has inconsistent field spacing: `"ok": false,` vs other fields. While not a functional bug, it looks messy. Also, if `writeJSON` itself fails, the error is not captured.

**Severity:** Low - cosmetic/inconsistent

**Fix:** Use consistent spacing and handle potential writeJSON error

---

### Bug 6: HIGH - `files` command has incorrect path reference

**Location:** Lines 1183-1186

**Code:**
```go
files = append(files, map[string]string{
    "path": layout.VersionManifestPath(manifest.CurrentVersionID),
    "type": "version_manifest",
})
```

**Problem:** If `manifest` is `nil` (which can happen if `storage.ReadManifest(layout)` fails silently), calling `manifest.CurrentVersionID` will panic.

**Severity:** High - potential nil pointer dereference panic

**Fix:** Check manifest is not nil before use:
```go
if manifest != nil {
    files = append(files, map[string]string{
        "path": layout.VersionManifestPath(manifest.CurrentVersionID),
        "type": "version_manifest",
    })
} else {
    files = append(files, map[string]string{
        "path": "",
        "type": "version_manifest",
    })
}
```

---

### Bug 7: MEDIUM - `stop` command audio metadata marshal error not properly propagated

**Location:** Lines 469-482

**Code:**
```go
var stopErrs []error

versionAudioMetaPath := filepath.Join(versionAudioDir, "audio.json")
audioMetaData, err := json.MarshalIndent(audioMeta, "", "  ")
if err != nil {
    stopErrs = append(stopErrs, fmt.Errorf("failed to marshal audio metadata: %w", err))
} else {
    tmpPath := filepath.Join(layout.TmpDir, "audio_meta.tmp")
    if err := os.WriteFile(tmpPath, audioMetaData, 0644); err != nil {
        stopErrs = append(stopErrs, fmt.Errorf("failed to write audio metadata temp file: %w", err))
    } else if err := os.Rename(tmpPath, versionAudioMetaPath); err != nil {
        stopErrs = append(stopErrs, fmt.Errorf("failed to rename audio metadata file: %w", err))
    }
}

fmt.Fprintf(a.out, "Audio metadata written.\n")  // Always printed even on error
```

**Problem:** The "Audio metadata written." message prints even when there was an error. The error list handling is good, but the success message should be conditional.

**Severity:** Medium - misleading output

**Fix:** Move success message inside the `else` block or add error check

---

### Bug 8: MEDIUM - `providerAuth` "test" returns status incorrectly

**Location:** Lines 1604-1610

**Code:**
```go
case "test":
    status, err := a.secrets.Status(ctx, suite.CredentialRef)
    if err != nil {
        return err
    }
    return writeJSON(a.out, map[string]any{"ok": status.Configured, "provider": providerID, "status": status})
```

**Problem:** The response field is `"ok": status.Configured` which returns true/false, but `"ok"` typically indicates whether the command itself succeeded, not the status of the provider. The structure is inconsistent with other commands.

**Severity:** Low - API inconsistency

---

## Summary

| Bug | Severity | Type | Lines |
|-----|----------|------|-------|
| 1 | Critical | Stack pointer escape | 684 |
| 2 | Critical | Stack pointer escape | 1117 |
| 3 | High | Nil/error handling | 436-514 |
| 4 | Medium | UX/typo | 806-837 |
| 5 | Low | Cosmetic | 592-600 |
| 6 | High | Nil pointer dereference | 1173-1186 |
| 7 | Medium | Misleading output | 469-482 |
| 8 | Low | API inconsistency | 1604-1610 |

**Total bugs identified: 8**
**Critical bugs: 2**
**High severity bugs: 2**
