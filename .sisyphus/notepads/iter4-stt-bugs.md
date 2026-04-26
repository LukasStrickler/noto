# iter4-stt-bugs.md

## STT Provider Bug Reports

### BUG 1: Hardcoded "speaker_1" fallback in Whisper
**File:** whisper.go:121
**Severity:** MEDIUM
**Description:** When both `SpeakerLabel` and `SpeakerID` are empty, the code falls back to `"speaker_1"` hardcoded. If multiple speakers exist, they all get "speaker_1" as label, causing speaker confusion.
```go
if speakerLabel == "" {
    speakerLabel = "speaker_1"
}
```
**Impact:** Multi-speaker transcripts will incorrectly label all speakers as "speaker_1".

---

### BUG 2: Word speaker mapping creates wrong IDs in Whisper
**File:** whisper.go:283
**Severity:** MEDIUM
**Description:** Word parsing creates speakerID as `"speaker_" + w.Speaker`, but speaker labels in segments are stored as their raw form (e.g., "A", "B"). If `w.Speaker = "A"`, word's SpeakerID becomes "speaker_A" which won't match segment's speakerID "A".
```go
if w.Speaker != "" {
    speakerID = "speaker_" + w.Speaker
}
```
**Impact:** Word-level timestamps won't correctly associate with speaker segments.

---

### BUG 3: Hardcoded "audio.m4a" filename in Whisper
**File:** whisper.go:51
**Severity:** MEDIUM
**Description:** Multipart form uses hardcoded filename "audio.m4a" regardless of actual audio format. If audio is MP3, WAV, or other format, the API may reject based on extension mismatch.
```go
body, contentType, err := multipartWriter(fields, audio, "audio.m4a")
```
**Impact:** Non-M4A audio files may be rejected by OpenAI Whisper API due to filename extension mismatch.

---

### BUG 4: Hardcoded "audio.m4a" filename in Speechmatics
**File:** speechmatics.go:69
**Severity:** MEDIUM
**Description:** Same issue as Whisper - hardcoded "audio.m4a" filename regardless of actual audio format.
```go
body, contentType, err := multipartWriter(fields, audio, "audio.m4a")
```
**Impact:** Non-M4A audio files may be rejected by Speechmatics API.

---

### BUG 5: API key check AFTER multipartWriter call in Speechmatics
**File:** speechmatics.go:69-72
**Severity:** HIGH
**Description:** `multipartWriter(fields, audio, "audio.m4a")` is called BEFORE checking if `a.APIKey == ""`. If key is missing, the expensive multipart creation happens first, then returns an error.
```go
body, contentType, err := multipartWriter(fields, audio, "audio.m4a") // line 69
if err != nil {
    return "", err
}
// ... then much later at line 92:
if a.APIKey == "" {
    return "", notoerr.New("missing_credential", ...)
}
```
**Fix should be:** Check API key BEFORE calling multipartWriter.

---

### BUG 6: No polling/retry logic in Speechmatics fetch
**File:** speechmatics.go:105-128
**Severity:** HIGH
**Description:** Unlike AssemblyAI which polls with `MaxPolls`, Speechmatics `fetch()` makes only ONE request. If the job isn't ready yet, transcription fails immediately with no retry.
```go
func (a *SpeechmaticsAdapter) fetch(ctx context.Context, client HTTPDoer, baseURL string, jobID string) ([]byte, error) {
    // Single request - no polling loop
    resp, err := client.Do(req)
    // ...
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, notoerr.New("provider_failed", "Speechmatics fetch failed.", ...)
    }
    return respBytes, nil
}
```
**Impact:** Transcription fails if job not immediately ready; no way to wait for async completion.

---

### BUG 7: AssemblyAI ContextBias array passed incorrectly
**File:** assemblyai.go:106-107
**Severity:** MEDIUM
**Description:** AssemblyAI's `keyterms_prompt` field expects a specific format (likely string or structured object), but `opts.ContextBias` (a `[]string`) is passed directly as a JSON value. This likely produces wrong JSON structure.
```go
if len(opts.ContextBias) > 0 {
    payload["keyterms_prompt"] = opts.ContextBias
}
```
**Impact:** Context bias may not work due to incorrect JSON format sent to AssemblyAI API.

---

### BUG 8: Word speaker ID inconsistency in AssemblyAI
**File:** assemblyai.go:281-282
**Severity:** MEDIUM
**Description:** Words create speakerID as `"speaker_" + w.Speaker`, but utterances use the raw speaker label directly. If AssemblyAI returns speaker labels like "A" or "B", word mapping creates "speaker_A" which doesn't match utterance's speakerID "A".
```go
// Words:
if w.Speaker != "" {
    speakerID = "speaker_" + w.Speaker
}
// But utterances store raw label:
speakerID = speakerLabel  // e.g., "A" not "speaker_A"
```
**Impact:** Word timestamps don't correctly associate with speaker segments.

---

### BUG 9: Whisper SpeakerDiarization flag is incorrect
**File:** whisper.go:188
**Severity:** LOW
**Description:** The condition `SpeakerDiarization: len(speakers) > 1` is semantically wrong. Even with 1 detected speaker, diarization was attempted (via speaker labels). The flag should indicate whether diarization was requested/attempted, not number of speakers detected.
```go
SpeakerDiarization: len(speakers) > 1,  // Wrong - 1 speaker still means diarization was on
```
**Impact:** False negative - single speaker transcripts incorrectly report no diarization.

---

### BUG 10: No HTTP timeout on poll loop in AssemblyAI
**File:** assemblyai.go:144-206
**Severity:** LOW
**Description:** While `MaxPolls` limits iterations, there's no overall timeout for the entire polling operation. A long-running transcription could poll for MaxPolls * PollInterval duration. The context timeout would eventually kick in, but the polling loop doesn't have explicit overall timeout.
```go
for i := 0; i < maxPolls; i++ {
    // ... poll request
    // No check for total elapsed time
    select {
    case <-ctx.Done():
        // ...
    case <-timer.C:
        // ...
    }
}
```
**Impact:** Could wait longer than necessary if context has shorter timeout.

---

## Summary

| # | File:Line | Bug | Severity |
|---|-----------|-----|----------|
| 1 | whisper.go:121 | Hardcoded "speaker_1" fallback | MEDIUM |
| 2 | whisper.go:283 | Word speaker ID mismatch | MEDIUM |
| 3 | whisper.go:51 | Hardcoded "audio.m4a" filename | MEDIUM |
| 4 | speechmatics.go:69 | Hardcoded "audio.m4a" filename | MEDIUM |
| 5 | speechmatics.go:69 | API key check AFTER multipartWriter | HIGH |
| 6 | speechmatics.go:105 | No polling/retry in fetch | HIGH |
| 7 | assemblyai.go:107 | ContextBias array format wrong | MEDIUM |
| 8 | assemblyai.go:281 | Word speaker ID inconsistency | MEDIUM |
| 9 | whisper.go:188 | SpeakerDiarization flag incorrect | LOW |
| 10 | assemblyai.go:159 | No overall poll timeout | LOW |

**Total: 10 bugs (2 HIGH, 7 MEDIUM, 1 LOW)**