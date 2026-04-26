# LLM Provider Bug Reports

## Summary
Files reviewed: mistral.go, openrouter.go, provider.go, adapters_test.go, openrouter_test.go

## BUGS FOUND

---

### BUG 1: Test References Undefined Types
**File:** internal/providers/llm/openrouter_test.go  
**Lines:** 31, 61-68  

**Description:**  
The test file references types and methods that do NOT exist in openrouter.go:
- `OpenRouterSummaryAdapter` (line 31) - used with `.Summarize()` but openrouter.go only defines `OpenRouterAdapter`
- `OpenRouterClient` (line 61) - struct not defined in openrouter.go
- `Chat()` method - not defined in openrouter.go
- `ChatRequest` struct - not defined in openrouter.go

The test will not compile against the current openrouter.go implementation.

**Severity:** CRITICAL  
**Impact:** Test suite broken - `go test ./internal/providers/llm/...` will fail to compile.

---

### BUG 2: Empty API Key Silently Proceeds Without Auth
**File:** internal/providers/llm/mistral.go  
**Line:** 67-69  

**File:** internal/providers/llm/openrouter.go  
**Line:** 65-68  

**Description:**  
When `APIKey` is empty or whitespace-only, both providers skip adding the Authorization header entirely and proceed with the request anyway:

```go
if strings.TrimSpace(a.APIKey) != "" {
    req.Header.Set("Authorization", "Bearer "+a.APIKey)
}
```

If the API key is not set (common configuration error), the request will be sent WITHOUT any authentication. The API will likely reject the request, but this happens at the network layer rather than being caught early with a clear validation error.

**Severity:** MEDIUM  
**Impact:** Silent failure mode - no early validation when API key is missing.

---

### BUG 3: No HTTP Client Timeout Configured
**File:** internal/providers/llm/mistral.go  
**Line:** 31-34  

**File:** internal/providers/llm/openrouter.go  
**Line:** 31-34  

**Description:**  
Both providers use `http.DefaultClient` or allow nil client to fall back to `http.DefaultClient`. The DefaultClient has no timeout (timeout = 0 = indefinite). While context cancellation is respected by `http.NewRequestWithContext`, if the context has no deadline, a slow or hung API response could cause the request to hang forever.

```go
client := a.HTTP
if client == nil {
    client = http.DefaultClient
}
```

**Severity:** MEDIUM  
**Impact:** Could cause indefinite hangs if upstream LLM API is unresponsive and context has no deadline.

---

### BUG 4: No Content-Type Validation on Response
**File:** internal/providers/llm/mistral.go  
**Lines:** 78-86  

**File:** internal/providers/llm/openrouter.go  
**Lines:** 78-86  

**Description:**  
After reading the response body, neither provider checks `resp.Header.Get("Content-Type")` to verify it's actually JSON. If the API returns an error page (HTML) or a different content type, the subsequent `json.Unmarshal` will fail with a confusing parse error rather than a clear "unexpected content type" error.

Both providers only check status code and then try to parse JSON directly.

**Severity:** LOW  
**Impact:** Users may see confusing "Could not parse JSON" errors when API returns error pages.

---

### BUG 5: buildMessages and buildSummaryMessages Truncate at 50 Segments
**File:** internal/providers/llm/mistral.go  
**Lines:** 129-132  

**File:** internal/providers/llm/openrouter.go  
**Lines:** 106-109  

**Description:**  
Both functions limit transcript processing to the first 50 segments:

```go
if i >= 50 {
    textBuilder.WriteString("... (truncated)")
    break
}
```

Long transcripts (common in extended meetings) will have their content silently truncated. The LLM receives an incomplete picture of the meeting, potentially missing important decisions or action items that occurred later.

**Severity:** MEDIUM  
**Impact:** Long meetings may have incomplete summaries due to truncation.

---

## Severity Summary

| Bug | Severity | Description |
|-----|----------|-------------|
| 1 | CRITICAL | Test references undefined types - won't compile |
| 2 | MEDIUM | Empty API key proceeds without auth |
| 3 | MEDIUM | No HTTP client timeout configured |
| 4 | LOW | No Content-Type validation on response |
| 5 | MEDIUM | Transcript truncated at 50 segments |

## Total: 5 bugs (1 CRITICAL, 3 MEDIUM, 1 LOW)