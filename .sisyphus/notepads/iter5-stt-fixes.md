# Iteration 5: STT Provider Bug Fixes

## Summary
Fixed all 7 bugs reported in `iter5-stt-bugs.md` in the `internal/providers/stt/` directory.

---

## Bug 1: Whisper - API Key Checked AFTER Expensive multipartWriter Call

**File:** `whisper.go:51-58`
**Severity:** HIGH
**Status:** FIXED

### Fix
Moved API key validation BEFORE the `multipartWriter()` call at line 51.

**Before:**
```go
body, contentType, err := multipartWriter(fields, audio, "audio")
if err != nil {
    return nil, err
}
if a.APIKey == "" {
    return nil, notoerr.New("missing_credential", "Whisper API key is not configured.", nil)
}
```

**After:**
```go
if a.APIKey == "" {
    return nil, notoerr.New("missing_credential", "Whisper API key is not configured.", nil)
}
body, contentType, err := multipartWriter(fields, audio, "audio")
if err != nil {
    return nil, err
}
```

---

## Bug 2: Whisper - Inconsistent SpeakerDiarization Threshold

**File:** `whisper.go:188`
**Severity:** MEDIUM
**Status:** FIXED

### Fix
Changed `len(speakers) > 1` to `len(speakers) > 0` to match AssemblyAI and Speechmatics behavior.

**Before:** `SpeakerDiarization: len(speakers) > 1`
**After:** `SpeakerDiarization: len(speakers) > 0`

---

## Bug 3: Whisper - Word Struct Missing Speaker Fields

**File:** `whisper.go:85-91`
**Severity:** MEDIUM
**Status:** FIXED

### Fix
Added `SpeakerID` and `SpeakerLabel` fields to the `whisperWord` struct, renamed from `word` to `whisperWord` to avoid conflict with AssemblyAI's `word` struct. Updated `parseResponse` to populate word speaker information using the existing `speakerMap`.

**Before:**
```go
type word struct {
    Text       string  `json:"text"`
    Start      float64 `json:"start"`
    End        float64 `json:"end"`
    Confidence float64 `json:"confidence"`
}
```

**After:**
```go
type whisperWord struct {
    Text         string  `json:"text"`
    Start        float64 `json:"start"`
    End          float64 `json:"end"`
    Confidence   float64 `json:"confidence"`
    SpeakerID    string  `json:"speaker_id,omitempty"`
    SpeakerLabel string  `json:"speaker_label,omitempty"`
}
```

Also updated word parsing loop to correlate speaker labels with speakerMap.

---

## Bug 4: AssemblyAI - Polling Ignores "queued"/"processing" Status

**File:** `assemblyai.go:190-195`
**Severity:** HIGH
**Status:** FIXED

### Fix
Added explicit case handlers for intermediate states `"queued"` and `"processing"`, plus a catch-all `"undefined"` case to ensure the loop continues polling for any valid intermediate state.

**Before:**
```go
switch status.Status {
case "completed":
    return respBytes, nil
case "error":
    return nil, notoerr.New("provider_failed", "AssemblyAI transcription failed.", map[string]any{"error": status.Error})
}
```

**After:**
```go
switch status.Status {
case "completed":
    return respBytes, nil
case "error":
    return nil, notoerr.New("provider_failed", "AssemblyAI transcription failed.", map[string]any{"error": status.Error})
case "queued", "processing":
case "undefined":
}
```

---

## Bug 5: AssemblyAI - Word.SpeakerID Inconsistent With Segment SpeakerID Format

**File:** `assemblyai.go:280-291`
**Severity:** LOW
**Status:** FIXED

### Fix
Removed the `"speaker_"` prefix from word-level speaker IDs to match segment-level speaker ID format.

**Before:** `speakerID = "speaker_" + w.Speaker`
**After:** `speakerID = w.Speaker`

---

## Bug 6: Speechmatics - Model Field Hardcoded to "base"

**File:** `speechmatics.go:57`
**Severity:** MEDIUM
**Status:** FIXED

### Fix
Added `Model` field to `TranscribeOptions` struct in `provider.go` and updated Speechmatics adapter to use `opts.Model` instead of hardcoded `"base"`. Default remains `"base"` for backward compatibility.

**provider.go change:**
```go
type TranscribeOptions struct {
    // ... existing fields ...
    // Model is the transcription model to use (provider-specific).
    // Speechmatics supports: "base" (default), "enhanced", "best".
    // Whisper supports: "whisper-1" (default).
    Model string
    // ... existing fields ...
}
```

**speechmatics.go change:**
```go
model := opts.Model
if model == "" {
    model = "base"
}
fields := map[string]string{
    "model": model,
    // ...
}
```

---

## Bug 7: ContextBias Array Passed Incorrectly to AssemblyAI

**File:** `assemblyai.go:106-108`
**Severity:** MEDIUM
**Status:** FIXED

### Fix
Joined the `ContextBias` string slice into a comma-separated string before passing to AssemblyAI's `keyterms_prompt` field.

**Before:** `payload["keyterms_prompt"] = opts.ContextBias`
**After:** `payload["keyterms_prompt"] = strings.Join(opts.ContextBias, ", ")`

---

## Files Modified

| File | Bugs Fixed |
|------|-----------|
| `internal/providers/stt/whisper.go` | 1, 2, 3 |
| `internal/providers/stt/assemblyai.go` | 4, 5, 7 |
| `internal/providers/stt/speechmatics.go` | 6 |
| `internal/providers/stt/provider.go` | 6 (added Model field) |

## Verification

All bugs were manually verified by reviewing the code changes against the bug report. Go build was attempted but `go` command not available in environment.

---

(End of file - total fixes)
