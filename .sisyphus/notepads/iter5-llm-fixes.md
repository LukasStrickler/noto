# LLM Provider Bug Fixes (Iter5)

## Summary

All 7 bugs from `iter5-llm-bugs.md` have been fixed in `internal/providers/llm/openrouter.go` and `internal/providers/llm/mistral.go`.

## Bugs Fixed

### BUG 1: Dead Code - Nil Check on Non-Pointer Struct Field ✅ FIXED

**Files:** openrouter.go (line 177), mistral.go (line 189)

**Problem:** `resp.Choices[0].Message == nil` was dead code since `Message` is a struct (value type), not a pointer, so the nil check is always false.

**Fix:** Removed the `== nil` check since it can never be true:
```go
// Before
if len(resp.Choices) == 0 || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content == "" {

// After
if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
```

---

### BUG 2: No Retry Logic for Transient HTTP Errors ✅ FIXED

**Files:** openrouter.go (lines 78-108), mistral.go (lines 78-108)

**Problem:** All non-2xx responses were treated identically with no retry logic. HTTP 429, 502, 503, 504 are transient errors that often resolve with backoff.

**Fix:** Added retry loop with exponential backoff (1s, 2s) for:
- HTTP 429 (rate limit)
- HTTP 502, 503, 504 (server errors)

Client errors (4xx except 429) are returned immediately without retry.

---

### BUG 3: 10MB Response Limit But No Request Size Limit ✅ FIXED

**Files:** openrouter.go (line 65-67), mistral.go (line 67-69)

**Problem:** Response body was limited to 10MB but request body had no limit, risking memory exhaustion with large transcripts.

**Fix:** Added 1MB request body limit with early return error:
```go
if len(body) > 1024*1024 {
    return nil, notoerr.New("provider_request_too_large", "OpenRouter request body exceeds 1MB limit.", nil)
}
```

---

### BUG 4: Fallback Summary Skips Population of Structured Fields ✅ FIXED

**Files:** openrouter.go (line 219-221), mistral.go (line 231-233)

**Problem:** When JSON content parsing failed, code fell back to creating a summary with only `ShortSummary` populated, leaving Decisions, ActionItems, Risks, OpenQuestions empty.

**Fix:** Replaced the silent fallback with explicit error return:
```go
if err := json.Unmarshal([]byte(content), &parsed); err != nil {
    return nil, notoerr.Wrap("summary_parse_failed", "Failed to parse summary JSON from OpenRouter response.", err)
}
```

This ensures callers receive a clear error rather than an incomplete summary.

---

### BUG 5: Duplicate API Key Check in Same Function ✅ FIXED

**Files:** openrouter.go (lines 70-76), mistral.go (lines 72-76)

**Problem:** First check at lines 32-34 returns early if API key is empty. Second redundant check at lines 70-76 could never be true after the first check passes.

**Fix:** Removed the redundant conditional check, keeping only the header set:
```go
// Before
if strings.TrimSpace(a.APIKey) != "" {
    req.Header.Set("Authorization", "Bearer "+a.APIKey)
}

// After
req.Header.Set("Authorization", "Bearer "+a.APIKey)
```

---

### BUG 6: Error Code "provider_failed" Used for Both Permanent and Transient Errors ✅ FIXED

**Files:** openrouter.go (lines 101, 110), mistral.go (lines 101, 110)

**Problem:** All HTTP errors used `"provider_failed"` code regardless of whether they were permanent (4xx) or transient (5xx, 429).

**Fix:** Added distinct error codes:
- `"provider_client_error"` - for 4xx client errors (don't retry)
- `"provider_server_error"` - for 5xx server errors after exhausting retries
- `"provider_request_too_large"` - for oversized requests

---

### BUG 7: Segment Truncation at 200 Still High - No Token Budget Awareness ✅ FIXED

**Files:** openrouter.go (lines 113-134), mistral.go (lines 136-157)

**Problem:** Truncation at 200 segments ignored token budget. Very long segments could still exceed context limits.

**Fix:** Added both segment count AND character count limits:
```go
totalChars := 0
maxChars := 100_000
maxSegments := 150

for i, seg := range transcript.Segments {
    if i >= maxSegments || totalChars > maxChars {
        textBuilder.WriteString("... (truncated)")
        break
    }
    // ... build segment
    totalChars += len(segText)
}
```

This ensures:
- Maximum 150 segments (reduced from 200)
- Maximum 100k characters (soft budget limit)
- Either limit triggers truncation with notice to model

---

## Severity Summary

| Bug | Severity | Status |
|-----|----------|--------|
| 1 | MEDIUM | ✅ Fixed |
| 2 | MEDIUM | ✅ Fixed |
| 3 | LOW | ✅ Fixed |
| 4 | LOW | ✅ Fixed |
| 5 | LOW | ✅ Fixed |
| 6 | LOW | ✅ Fixed |
| 7 | MEDIUM | ✅ Fixed |

Total: 7 bugs fixed (4 MEDIUM, 3 LOW)

---

## Files Modified

- `internal/providers/llm/openrouter.go` - All 7 bugs fixed
- `internal/providers/llm/mistral.go` - All 7 bugs fixed

No other files were modified per constraint: "Do NOT modify any files outside internal/providers/llm/".