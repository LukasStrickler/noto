# CLI Bug Report: `internal/cli/cli.go`

## Bug 1: Silent Config Load Failure (Line 42)
**Severity:** Critical  
**Location:** Line 42

```go
loadedCfg, _ := cfg.Load()
```

The error from `cfg.Load()` is silently ignored with `_`. If loading fails, `loadedCfg` could be nil or zero-valued. Subsequent calls like `loadedCfg.GetRecordingsDir()` and `loadedCfg.ConfigDir` will panic or use empty defaults, leading to unpredictable behavior.

**Impact:** Application may run with zero-valued config, creating recordings in wrong locations or failing silently.

---

## Bug 2: Incorrect Permissions on Written Files (Lines 450, 486, 489, 559, 772)
**Severity:** Critical  
**Location:** Lines 450, 486, 489, 559, 772

```go
os.WriteFile(audioPath, audioData, 0644)
os.WriteFile(tmpPath, audioMetaData, 0644)
```

File permissions `0644` (rw-r--r--) allow group and others to read written files, including potentially sensitive audio data and secrets. The standard umask-based permission `0640` or `0600` should be used instead.

**Impact:** Sensitive audio recordings and meeting data could be readable by other users on multi-user systems.

---

## Bug 3: Empty Title Passed to Index During Transcribe (Line 740)
**Severity:** Medium  
**Location:** Line 740

```go
if err := a.indexMeeting(meetingIDStr, "", transcript, nil); err != nil {
```

When `transcribe` command is called, an empty string `""` is passed as the title to `indexMeeting()`. The actual meeting title is never fetched from storage, so the search index entry has no title.

**Impact:** Search results will show meetings with empty titles, degrading search usability.

---

## Bug 4: Empty Title Also in `stop()` Command (Line 508)
**Severity:** Medium  
**Location:** Line 508

```go
if err := a.indexMeeting(lastMeeting.MeetingID.String(), lastMeeting.Title, transcript, nil); err != nil {
```

Wait—actually this one uses `lastMeeting.Title` correctly. The bug is that `stop()` fetches the meeting title from `lastMeeting` (which is the most recent meeting in storage, not the one that was just recorded). If the recording just started and hasn't been properly finalized as the "last" meeting, this could index the wrong meeting.

**Impact:** Transcripts may be indexed under wrong meeting or with wrong title.

---

## Bug 5: Import Audio Does Not Update Manifest Audio Metadata (Lines 571-573)
**Severity:** Medium  
**Location:** Lines 571-573

```go
if err := storage.WriteManifest(layout, manifest); err != nil {
    return err
}
audioData, err := os.ReadFile(audioPath)
```

The manifest is written BEFORE audio metadata is populated. After `ia.Import()`, the `result.AudioMetadata` contains audio info, but the manifest is written without waiting for audio processing to complete. The manifest's audio metadata association is incomplete.

**Impact:** Manifest may reference audio that wasn't properly imported or verified.

---

## Bug 6: `files` Command Returns Non-Existent Paths (Lines 1183-1188)
**Severity:** Medium  
**Location:** Lines 1183-1188

```go
files := []map[string]string{
    {"path": layout.ManifestPath, "type": "manifest"},
    {"path": layout.AudioPath, "type": "audio"},
    {"path": layout.TranscriptPath, "type": "transcript"},
    {"path": layout.SummaryPath, "type": "summary"},
}
```

The `files` command returns paths for audio, transcript, and summary WITHOUT checking if they exist. These paths may not exist for meetings that haven't been fully processed.

**Impact:** API consumers get misleading information about file existence.

---

## Bug 7: `benchmarkCompare` Only Shows Metrics in File2 (Lines 1350-1372)
**Severity:** Medium  
**Location:** Lines 1350-1378

```go
for _, m2 := range result2.Results {
    m1, ok := result1Map[m2.Metric]
    // ...
}
```

The comparison loop only iterates over metrics present in `result2`. If File1 has metrics that don't exist in File2, they're only shown in the "REMOVED" section at the end. But if File2 has metrics not in File1 (NEW), they're shown but their "before" value shows as "(none)" in the first column. The column alignment is confusing.

**Impact:** Benchmark comparison output is difficult to interpret.

---

## Bug 8: `randomSuffix` Uses `uuid.ID()` Internally (Line 1545)
**Severity:** Low  
**Location:** Line 1545

```go
b[i] = byte(uuid.New().ID() % 256)
```

`uuid.New().ID()` returns a uint64 that is a counter, not a cryptographically random value. While UUIDs are unique, their internal counter is predictable. Using `crypto/rand` or at minimum `rand.Int()` with a proper seed would be more appropriate for generating random suffixes.

**Impact:** Potential predictability of version IDs if security relies on their randomness.

---

## Bug 9: `transcript` Command Checks `isJSON` After Processing (Line 1058)
**Severity:** Low  
**Location:** Lines 1058-1065

```go
isJSON := hasJSONFlag(args)
if !isJSON {
    for _, seg := range transcript.Segments {
        // ...
    }
    return nil
}
```

The transcript is loaded and parsed (line 1053) BEFORE checking whether JSON output was requested. For meetings without transcripts, this will fail with "No transcript found" even when `--json` flag would have returned a structured error.

**Impact:** Inconsistent error handling between JSON and text modes.

---

## Bug 10: `setSpeech` Always Overwrites LLM Provider (Line 1565)
**Severity:** Low  
**Location:** Line 1565

```go
cfg.Routing.SpeechProvider = providerID
cfg.Routing.LLMProvider = "openrouter"  // Always overwrites!
```

When setting a speech provider, `setSpeech` unconditionally sets `LLMProvider` to `"openrouter"`. This is intentional but worth noting: setting speech doesn't preserve any previous LLM selection.

**Impact:** Users who manually configured a different LLM will have it overwritten when setting speech provider.

---

## Summary Table

| Bug | Severity | Type | Lines |
|-----|----------|------|-------|
| 1 | Critical | Error handling | 42 |
| 2 | Critical | Security (permissions) | 450, 486, 489, 559, 772 |
| 3 | Medium | Data integrity | 740 |
| 4 | Medium | Logic error | 508 |
| 5 | Medium | Flow error | 571-573 |
| 6 | Medium | Data accuracy | 1183-1188 |
| 7 | Medium | UX/Output | 1350-1378 |
| 8 | Low | Security | 1545 |
| 9 | Low | UX | 1058-1065 |
| 10 | Low | Design | 1565 |
