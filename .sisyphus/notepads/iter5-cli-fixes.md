# CLI Bug Fixes Report

## Overview
Fixed 10 bugs in `internal/cli/cli.go` as reported in `iter5-cli-bugs.md`.

---

## Bug 1: Silent Config Load Failure (Line 42)
**Severity:** Critical  
**Original Code:**
```go
loadedCfg, _ := cfg.Load()
```
**Fix:** Changed to properly handle error:
```go
loadedCfg, err := cfg.Load()
if err != nil {
    notoerr.WriteJSON(errOut, notoerr.Wrap("config_load_failed", "Could not load config", err))
    return 1
}
```
**Rationale:** Config load failure now returns proper exit code 1 with JSON error instead of proceeding with potentially nil config.

---

## Bug 2: Incorrect Permissions 0644
**Severity:** Critical  
**Original:** Used `0644` (rw-r--r--) for sensitive files  
**Fix:** Changed all occurrences to `0600` (rw-------)
- Line 454: `os.WriteFile(audioPath, audioData, 0600)`
- Line 463: `os.WriteFile(filepath.Join(versionAudioPath, "recording.m4a"), audioData, 0600)`
- Line 490: `os.WriteFile(tmpPath, audioMetaData, 0600)`
**Rationale:** Audio data and metadata should not be readable by group/others.

---

## Bug 3: Empty Title in transcribe (Line 740)
**Severity:** Medium  
**Original:**
```go
if err := a.indexMeeting(meetingIDStr, "", transcript, nil); err != nil {
```
**Fix:** Fetch meeting title before indexing:
```go
meeting, _ := storage.GetMeeting(a.recordingsDir, meetingID)
indexTitle := ""
if meeting != nil {
    indexTitle = meeting.Title
}
if err := a.indexMeeting(meetingIDStr, indexTitle, transcript, nil); err != nil {
```
**Rationale:** Search index now receives actual meeting title instead of empty string.

---

## Bug 4: stop() meeting title concern (Line 508)
**Severity:** Medium  
**Status:** Acknowledged - code was already correct  
**Analysis:** The `stop()` function uses `lastMeeting.Title` which correctly references the most recent meeting in storage. The concern about "wrong meeting" was a false alarm - `ListMeetings` returns meetings sorted by creation time descending, so `refs[0]` is indeed the most recent recording.

---

## Bug 5: Import Audio Writes Manifest Before Audio Processing
**Severity:** Medium  
**Original Flow:**
1. Call `ia.Import()` which populates `result.AudioMetadata`
2. Write manifest (without audio metadata)
3. Later: transcribe and other processing

**Fix:** Restructured to delay manifest write:
1. Call `ia.Import()` 
2. Check for speech provider
3. If no speech provider: write manifest with audio metadata
4. If transcription succeeds: write manifest BEFORE transcription result check
5. Transcribe audio
6. Return results

Manifest is now written only after confirming audio is imported and processing can proceed.

**Rationale:** Ensures manifest represents complete state after import processing.

---

## Bug 6: files Command Returns Non-Existent Paths
**Severity:** Medium  
**Original:** Returned paths without checking existence
**Fix:** Added `fileExists()` helper and `exists` field to each file entry:
```go
fileExists := func(path string) bool {
    if path == "" {
        return false
    }
    _, err := os.Stat(path)
    return err == nil
}
files := []map[string]any{
    {"path": layout.ManifestPath, "type": "manifest", "exists": fileExists(layout.ManifestPath)},
    ...
}
```
**Rationale:** API consumers can now determine which files actually exist.

---

## Bug 7: benchmarkCompare Output Confusion
**Severity:** Medium  
**Original:** Loop only over File2 metrics, showing "(none)" for File1 values  
**Fix:** Changed to iterate over File1 first (common metrics), then show REMOVED:
```go
for _, m1 := range result1.Results {
    m2, ok := result2Map[m1.Metric]
    if !ok {
        fmt.Fprintf(a.out, "%-45s %12.4f %12s %12s %-8s\n", m1.Metric, m1.Value, "---", "N/A", "REMOVED")
        continue
    }
    // ... show comparison
}

for _, m2 := range result2.Results {
    if _, ok := result1Map[m2.Metric]; !ok {
        fmt.Fprintf(a.out, "%-45s %12s %12.4f %12s %-8s\n", m2.Metric, "---", m2.Value, "NEW", "NEW")
    }
}
```
**Rationale:** File1 (baseline) metrics are shown first with proper delta, NEW metrics appear at end with "---" placeholder.

---

## Bug 8: randomSuffix Uses Predictable UUID ID
**Severity:** Low  
**Original:**
```go
b[i] = byte(uuid.New().ID() % 256)
```
**Fix:** Use `crypto/rand` for cryptographic randomness:
```go
func randomSuffix() string {
    b := make([]byte, 4)
    if _, err := rand.Read(b); err != nil {
        for i := range b {
            b[i] = byte(uuid.New().ID() % 256)
        }
    }
    return fmt.Sprintf("%x", b)
}
```
**Rationale:** crypto/rand provides proper randomness; fallback to uuid for error cases.

---

## Bug 9: transcript Command Checks isJSON After Processing
**Severity:** Low  
**Original:** Transcript loaded before checking `--json` flag  
**Fix:** Check flag first and return JSON error if transcript missing in JSON mode:
```go
isJSON := hasJSONFlag(args)

transcript, err := storage.ReadTranscript(layout)
if err != nil {
    if isJSON {
        return writeJSON(a.errOut, map[string]any{
            "ok":          false,
            "error":       "no_transcript",
            "message":     "No transcript found.",
            "meeting_id": meetingIDStr,
        })
    }
    return notoerr.New("no_transcript", "No transcript found.", map[string]any{"meeting_id": meetingIDStr})
}
```
**Rationale:** JSON mode now returns structured error response instead of text error.

---

## Bug 10: setSpeech Always Overwrites LLM Provider
**Severity:** Low  
**Original:**
```go
cfg.Routing.SpeechProvider = providerID
cfg.Routing.LLMProvider = "openrouter"  // Always overwrites
```
**Fix:** Only set default if LLMProvider is not already configured:
```go
cfg.Routing.SpeechProvider = providerID
if cfg.Routing.LLMProvider == "" {
    cfg.Routing.LLMProvider = "openrouter"
}
```
**Rationale:** Users who configured a different LLM retain their selection when setting speech provider.

---

## Summary

| Bug | Severity | Status |
|-----|----------|--------|
| 1 | Critical | Fixed |
| 2 | Critical | Fixed |
| 3 | Medium | Fixed |
| 4 | Medium | Acknowledged (already correct) |
| 5 | Medium | Fixed |
| 6 | Medium | Fixed |
| 7 | Medium | Fixed |
| 8 | Low | Fixed |
| 9 | Low | Fixed |
| 10 | Low | Fixed |

Total: 9 bugs fixed, 1 acknowledged as not a bug.