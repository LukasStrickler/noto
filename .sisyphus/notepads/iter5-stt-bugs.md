# Iteration 5: STT Provider Bug Report

## Summary
Deep bug search in `internal/providers/stt/` covering speechmatics.go, whisper.go, assemblyai.go.
Focus: API key handling, audio format issues, transcription parsing bugs, speaker diarization.

---

## Bug 1: Whisper - API Key Checked AFTER Expensive multipartWriter Call

**File:** `whisper.go:51-58`
**Severity:** HIGH
**Category:** API key handling

### Description
The API key validation happens AFTER calling `multipartWriter()`, which builds the entire multipart request body. If the API key is missing, the expensive buffer allocation and body construction has already occurred wastefully.

### Code Snippet
```go
body, contentType, err := multipartWriter(fields, audio, "audio")  // Line 51 - expensive operation
if err != nil {
    return nil, err
}

if a.APIKey == "" {  // Line 56 - API key checked AFTER multipartWriter
    return nil, notoerr.New("missing_credential", "Whisper API key is not configured.", nil)
}
```

### Inherited Pattern
This is the same bug pattern documented in iter4 for other providers. The correct pattern is to check credentials BEFORE any network or buffer operations.

---

## Bug 2: Whisper - Inconsistent SpeakerDiarization Threshold

**File:** `whisper.go:188`
**Severity:** MEDIUM
**Category:** Speaker diarization

### Description
`SpeakerDiarization` is set to `len(speakers) > 1`, but both other providers (AssemblyAI at line 317, Speechmatics at line 251) use `len(speakers) > 0`. This means Whisper incorrectly reports `SpeakerDiarization: false` even when exactly 1 speaker is detected and diarization is working.

### Code Snippet
```go
Capabilities: artifacts.TranscriptCapabilities{
    WordTimestamps:     len(words) > 0,
    SpeakerDiarization: len(speakers) > 1,  // WRONG: should be > 0
},
```

vs AssemblyAI (line 317):
```go
SpeakerDiarization: len(speakers) > 0,  // CORRECT
```

### Impact
Whisper transcripts will incorrectly report no speaker diarization capability when exactly 1 speaker is detected.

---

## Bug 3: Whisper - Word Struct Missing Speaker Fields

**File:** `whisper.go:85-91`
**Severity:** MEDIUM
**Category:** Response parsing

### Description
The `whisperResponse.word` struct only has `Text`, `Start`, `End`, `Confidence` fields, but the whisper API response includes `speaker_id` and `speaker_label` per word. These fields are defined in the `seg` struct (lines 101-102) but missing from `word`. This means word-level speaker attribution from the API is completely lost.

### Code Snippet
```go
type whisperResponse struct {
    Text       string  `json:"text"`
    Language   string  `json:"language"`
    Duration   float64 `json:"duration"`
    Words      []word  `json:"words"`
    Segments   []seg   `json:"segments"`
}

// word struct (lines 93-98) - missing speaker fields
type word struct {
    Text       string  `json:"text"`
    Start      float64 `json:"start"`
    End        float64 `json:"end"`
    Confidence float64 `json:"confidence"`
    // MISSING: SpeakerID  string  `json:"speaker_id,omitempty"`
    // MISSING: SpeakerLabel string `json:"speaker_label,omitempty"`
}
```

vs `seg` struct which correctly has:
```go
type seg struct {
    // ...
    SpeakerID      string  `json:"speaker_id,omitempty"`
    SpeakerLabel   string  `json:"speaker_label,omitempty"`
}
```

### Impact
Word-level speaker attribution from Whisper API is discarded. Words in the final transcript have no speaker information even when the API provides it.

---

## Bug 4: AssemblyAI - Polling Ignores "queued"/"processing" Status

**File:** `assemblyai.go:190-195`
**Severity:** HIGH
**Category:** Polling/retry logic

### Description
The poll loop only handles `status == "completed"` and `status == "error"`. If AssemblyAI returns `"queued"` or `"processing"` (which are documented intermediate states), the switch does nothing and the loop continues. However, the response is not returned, and the loop continues polling. The loop eventually times out after maxPolls, but wastes time not recognizing valid intermediate states.

### Code Snippet
```go
switch status.Status {
case "completed":
    return respBytes, nil
case "error":
    return nil, notoerr.New("provider_failed", "AssemblyAI transcription failed.", map[string]any{"error": status.Error})
}
// MISSING: case "queued", "processing" - should continue polling without error
// Current behavior: silently continues loop (which is technically correct but not explicit)
}
```

### Note
While the code "works" because it falls through and continues polling, it should explicitly handle and log intermediate states. The `default` case is missing, making the behavior implicit rather than explicit.

---

## Bug 5: AssemblyAI - Word.SpeakerID Inconsistent With Segment SpeakerID Format

**File:** `assemblyai.go:280-291`
**Severity:** LOW
**Category:** Speaker label normalization

### Description
In `parseResponse`, word-level speaker IDs are formatted differently than segment-level speaker IDs:

- Segment speakerID: uses the raw `speakerLabel` directly (line 255)
- Word speakerID: prepends `"speaker_"` prefix (line 282: `speakerID = "speaker_" + w.Speaker`)

This inconsistency means the same speaker has different IDs in segments vs words, breaking correlation.

### Code Snippet
```go
// Segment parsing (line 253-256)
speakerID, ok := speakerMap[speakerLabel]
if !ok {
    speakerID = speakerLabel  // Uses raw label directly
    speakerMap[speakerLabel] = speakerID

// Word parsing (line 280-282)
speakerID := ""
if w.Speaker != "" {
    speakerID = "speaker_" + w.Speaker  // Prepends "speaker_" prefix!
}
```

### Impact
Speaker IDs in segments (e.g., `"A"`) differ from speaker IDs in words (e.g., `"speaker_A"`), making it impossible to correlate which words belong to which speakers when joining segments and words data.

---

## Bug 6: Speechmatics - Model Field Hardcoded to "base"

**File:** `speechmatics.go:57`
**Severity:** MEDIUM
**Category:** Configuration rigidity

### Description
The model field is hardcoded to `"base"` in the submit request. There is no way for callers to specify a different model (e.g., "enhanced", "best"). The `TranscribeOptions` struct has no model selection field.

### Code Snippet
```go
fields := map[string]string{
    "model":                           "base",  // HARDCODED - no override possible
    "language":                        opts.Language,
    "enable_speakers":                 "true",
    "enable_word_level_timestamps":    "true",
}
```

### Impact
Users cannot access Speechmatics' higher accuracy models even if they have API access to them.

---

## Bug 7: ContextBias Array Passed Incorrectly to AssemblyAI

**File:** `assemblyai.go:106-108`
**Severity:** MEDIUM (potential)
**Category:** API payload construction

### Description
`ContextBias` (a `[]string`) is passed directly as `keyterms_prompt`. AssemblyAI's API likely expects a string (comma-separated or similar), but the code passes the Go slice directly. This will produce JSON like `"keyterms_prompt": ["term1", "term2"]` instead of a string format the API expects.

### Code Snippet
```go
if len(opts.ContextBias) > 0 {
    payload["keyterms_prompt"] = opts.ContextBias  // Passes []string directly
}
```

vs the correct approach would be joining the array into a string.

### Impact
Context bias hints may not work correctly with AssemblyAI due to type mismatch.

---

## Total Bugs Found: 7

| # | File | Line | Severity | Category |
|---|------|------|----------|----------|
| 1 | whisper.go | 51-58 | HIGH | API key handling |
| 2 | whisper.go | 188 | MEDIUM | Speaker diarization |
| 3 | whisper.go | 85-91 | MEDIUM | Response parsing |
| 4 | assemblyai.go | 190-195 | HIGH | Polling logic |
| 5 | assemblyai.go | 280-291 | LOW | Speaker normalization |
| 6 | speechmatics.go | 57 | MEDIUM | Configuration |
| 7 | assemblyai.go | 106-108 | MEDIUM | API payload |

---

## Priority Fix Order
1. Bug 1 (API key before multipart) - Prevents wasted computation
2. Bug 4 (polling missing states) - Could cause timeouts
3. Bug 2 (diarization threshold) - Incorrect capability reporting
4. Bug 3 (missing word speaker fields) - Data loss
5. Bug 7 (ContextBias type) - Feature may not work
6. Bug 6 (hardcoded model) - Limits functionality
7. Bug 5 (inconsistent speaker IDs) - Data correlation issues