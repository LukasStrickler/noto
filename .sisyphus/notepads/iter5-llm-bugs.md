# LLM Provider Bug Reports (Iter5)

## Summary
Files reviewed: mistral.go, openrouter.go, provider.go, adapters_test.go, openrouter_test.go
Focus: Subtle API handling issues, retry logic bugs, streaming response parsing, token limit edge cases

## BUGS FOUND

---

### BUG 1: Dead Code - Nil Check on Non-Pointer Struct Field
**File:** internal/providers/llm/openrouter.go  
**Lines:** 158  

**File:** internal/providers/llm/mistral.go  
**Lines:** 170  

**Description:**  
The validation check uses `resp.Choices[0].Message == nil` but `Message` is defined as a struct (not a pointer):

```go
// openrouter.go line 148-152
Choices []struct {
    Message struct {
        Content string `json:"content"`
    } `json:"message"`
} `json:"choices"`
```

Since `Message` is a value type, not a pointer, `resp.Choices[0].Message == nil` is ALWAYS FALSE. This nil check is dead code and provides no protection. The subsequent `resp.Choices[0].Message.Content` access will always succeed (returning empty string if Message is zero value), bypassing the intended validation.

**Severity:** MEDIUM  
**Impact:** The intended validation guard is non-functional. If API returns malformed response with empty content but Message is zero-valued struct, the check passes and downstream parsing may produce unexpected results.

**Code Snippet:**
```go
if len(resp.Choices) == 0 || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content == "" {
    return nil, notoerr.New("provider_response_invalid", "OpenRouter response did not include message content.", nil)
}
```

---

### BUG 2: No Retry Logic for Transient HTTP Errors
**File:** internal/providers/llm/openrouter.go  
**Lines:** 77-80  

**File:** internal/providers/llm/mistral.go  
**Lines:** 77-80  

**Description:**  
Both providers treat ALL non-2xx responses identically without retry logic:

```go
if resp.StatusCode < 200 || resp.StatusCode >= 300 {
    return nil, notoerr.New("provider_failed", "OpenRouter summarization failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
}
```

HTTP 429 (rate limit), 503 (service unavailable), 502 (bad gateway) are transient errors that often resolve with backoff. The current implementation fails immediately on these, forcing callers to implement retry logic externally.

**Severity:** MEDIUM  
**Impact:** Poor resilience to temporary API issues. Users experience failures that could self-heal with simple retry.

---

### BUG 3: 10MB Response Limit But No Request Size Limit
**File:** internal/providers/llm/openrouter.go  
**Lines:** 83  

**File:** internal/providers/llm/mistral.go  
**Lines:** 83  

**Description:**  
The response body is guarded with `io.LimitReader(..., 10*1024*1024)` to prevent memory exhaustion from large responses. However, there is NO corresponding limit on the request body sent to the API:

```go
respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
```

A very long transcript (thousands of segments) could produce a large request payload that:
1. Consumes significant memory during `json.Marshal(payload)`
2. May be rejected by the API for being too large
3. Has no early warning or size checking

**Severity:** LOW  
**Impact:** Potential memory spike with very large transcripts; no feedback to caller about size issues.

---

### BUG 4: Fallback Summary Skips Population of Structured Fields
**File:** internal/providers/llm/openrouter.go  
**Lines:** 200-213  

**File:** internal/providers/llm/mistral.go  
**Lines:** 212-225  

**Description:**  
When the JSON content from the LLM fails to parse (inner parse of `content` field), the code falls back to creating a summary with only `ShortSummary` populated:

```go
if err := json.Unmarshal([]byte(content), &parsed); err != nil {
    summary := artifacts.Summary{
        SchemaVersion: "summary.v1",
        MeetingID:     meetingID,
        ShortSummary:  content,
        // Decisions, ActionItems, Risks, OpenQuestions all EMPTY
        Model: artifacts.SummaryModel{...},
    }
    if err := artifacts.ValidateSummary(summary, transcript); err != nil {
        return nil, err
    }
    return &summary, nil
}
```

A summary with empty `Decisions`, `ActionItems`, `Risks`, `OpenQuestions` may be technically valid per schema but is semantically incomplete. Callers expecting structured data receive an effectively empty summary.

**Severity:** LOW  
**Impact:** Fallback summaries provide poor utility - caller may treat them as successful but they lack actionable content.

---

### BUG 5: Duplicate API Key Check in Same Function
**File:** internal/providers/llm/openrouter.go  
**Lines:** 32-34 and 70-72  

**File:** internal/providers/llm/mistral.go  
**Lines:** 32-34 and 72-74  

**Description:**  
The code checks if API key is empty/spaces at the START of Summarize (lines 32-34) and returns early with error. However, lines 70-72 (openrouter) and 72-74 (mistral) have a SECOND redundant check:

```go
// First check at line 32-34
if strings.TrimSpace(a.APIKey) == "" {
    return nil, notoerr.New("provider_config_invalid", "OpenRouter API key is required.", nil)
}
// ... later at line 70-72
if strings.TrimSpace(a.APIKey) != "" {
    req.Header.Set("Authorization", "Bearer "+a.APIKey)
}
```

The second check can NEVER be true when the first check passes (due to early return). The second check appears to be leftover defensive coding from copy-paste or refactoring.

**Severity:** LOW  
**Impact:** Dead code that adds confusion; no runtime impact since first check always returns.

---

### BUG 6: Error Code "provider_failed" Used for Both Permanent and Transient Errors
**File:** internal/providers/llm/openrouter.go  
**Line:** 88  

**File:** internal/providers/llm/mistral.go  
**Line:** 88  

**Description:**  
The error code `"provider_failed"` is used for ALL HTTP error responses, regardless of status code:

```go
return nil, notoerr.New("provider_failed", "Mistral summarization failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
```

A 400 Bad Request and a 503 Service Unavailable get the same error code, making it impossible for callers to distinguish recoverable from non-recoverable failures programmatically.

**Severity:** LOW  
**Impact:** Callers cannot make intelligent retry decisions based on error type.

---

### BUG 7: Segment Truncation at 200 Still High - No Token Budget Awareness
**File:** internal/providers/llm/openrouter.go  
**Lines:** 111-114  

**File:** internal/providers/llm/mistral.go  
**Lines:** 134-137  

**Description:**  
Iter4 reported truncation at 50 segments, but current code truncates at 200 segments:

```go
if i >= 200 {
    textBuilder.WriteString("... (truncated)")
    break
}
```

The limit was increased to 200 but still has issues:
1. No awareness of token budget - 200 segments could exceed context window depending on segment length
2. Each segment's text length is unbounded - short segments vs. long monologue segments
3. No warning to caller that truncation occurred

**Severity:** MEDIUM  
**Impact:** Very long meetings may still exceed token limits, or miss content if segments are long.

---

## Severity Summary

| Bug | Severity | Category | Description |
|-----|----------|----------|-------------|
| 1 | MEDIUM | Logic Error | Nil check on non-pointer struct field is dead code |
| 2 | MEDIUM | Reliability | No retry logic for transient HTTP errors (429, 503, etc.) |
| 3 | LOW | Resource | No request size limit, only response size limit |
| 4 | LOW | Data Quality | Fallback summary produces incomplete structured data |
| 5 | LOW | Code Quality | Duplicate API key check is dead code |
| 6 | LOW | Error Handling | Error code doesn't distinguish transient vs permanent |
| 7 | MEDIUM | Completeness | 200 segment truncation still ignores token budgets |

## Total: 7 bugs (0 CRITICAL, 4 MEDIUM, 3 LOW)

## Comparison to Iter4

| Bug from Iter4 | Status | Notes |
|----------------|--------|-------|
| Empty API key silently proceeds | FIXED | Both providers now return early error |
| Test references undefined types | PERSISTS | openrouter_test.go still has compilation issues |
| No HTTP client timeout | PERSISTS | Still uses http.Client{} without Transport config |
| No Content-Type validation | PERSISTS | Still no check for JSON content type |
| 50-segment truncation | FIXED/UPGRADED | Now 200 segments but still no token awareness |

## New Bugs Found in Iter5 (not in Iter4)

1. Dead nil-check on struct field (BUG 1)
2. No retry for transient errors (BUG 2)  
3. Segment truncation ignores token budget (BUG 7)
4. No request size limit (BUG 3 - partial overlap with BUG 7)
