# F2: Code Quality Review - Security, Code Quality, and Robustness Audit

## Audit Overview

Reviewed all significant source files across the codebase including:
- `internal/storage/adapters/` (S3, local)
- `internal/config/`
- `internal/secrets/`
- `internal/providers/` (LLM, STT, router)
- `cmd/capture/` (Go IPC, Swift audio capture)
- `internal/artifacts/`
- `internal/storage/`
- `internal/search/`
- `internal/tui/`
- `internal/cli/`

---

## HIGH Severity Issues

### 1. Path Traversal Vulnerability in Local Adapter
**File:** `internal/storage/adapters/local.go` (lines 32-33, 65-66)

```go
func (a *localAdapter) PutObject(ctx context.Context, key string, ...) error {
    fullPath := filepath.Join(a.basePath, key)
```

```go
func (a *localAdapter) GetObject(ctx context.Context, key string, ...) error {
    fullPath := filepath.Join(a.basePath, key)
```

**Issue:** The `key` parameter is joined with `basePath` without any validation. A malicious key like `../../etc/passwd` could escape the recordings directory.

**Impact:** Could potentially write/read files outside intended directory.

**Recommendation:** Add path sanitization to ensure the resolved path stays within `basePath`:
```go
func safeJoin(base, key string) (string, error) {
    full := filepath.Join(base, key)
    rel, err := filepath.Rel(base, full)
    if err != nil || strings.HasPrefix(rel, "..") {
        return "", fmt.Errorf("path traversal attempt")
    }
    return full, nil
}
```

---

### 2. Hardcoded Fallback R2 Endpoint
**File:** `internal/storage/adapters/s3.go` (lines 369-375)

```go
func getR2Endpoint() string {
    accountID := getR2AccountID()
    if accountID == "" {
        return "https://1234567890abcdef.r2.cloudflarestorage.com"
    }
    return fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
}
```

**Issue:** Hardcoded Cloudflare R2 endpoint used when `CLOUDFLARE_ACCOUNT_ID` is not set. This could leak the hardcoded bucket address in error messages or configurations.

**Impact:** Credential exposure if this fallback is ever used in production with real credentials.

**Recommendation:** Return an error instead of using a hardcoded fallback:
```go
if accountID == "" {
    return "", fmt.Errorf("CLOUDFLARE_ACCOUNT_ID not set")
}
```

---

## MEDIUM Severity Issues

### 3. Nil Check Missing After JSON Unmarshal
**File:** `internal/providers/llm/openrouter.go` (lines 145-147)

```go
if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
    return nil, notoerr.New("provider_response_invalid", "OpenRouter response did not include message content.", nil)
}
```

**Issue:** After checking `len(resp.Choices) == 0`, the code accesses `resp.Choices[0]` without nil check on the slice itself (though Choices being nil would panic). More critically, there's no check for `resp.Choices[0].Message` being nil before accessing `.Content`.

**Impact:** Potential nil pointer dereference if API returns unexpected structure.

---

### 4. Missing Timeout on Keychain Exec
**File:** `internal/secrets/keychain.go` (lines 20, 31, 47)

```go
func (s KeychainStore) Set(ctx context.Context, ref string, value string) error {
    cmd := exec.CommandContext(ctx, "security", ...)
```

**Issue:** The `security` command may hang indefinitely if the keychain is locked. No timeout is set on the command itself.

**Impact:** Process could hang waiting for keychain.

**Recommendation:** Use `exec.CommandContext(ctx, ...)` with proper timeout handling or wrap in a goroutine with timeout.

---

### 5. HTTP Response Not Fully Consumed on Error
**File:** `internal/providers/stt/whisper.go` (lines 73-75)

```go
if resp.StatusCode < 200 || resp.StatusCode >= 300 {
    return nil, notoerr.New("provider_failed", "Whisper transcription failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
}
```

**Issue:** `respBytes` is read before checking status code, but the entire body isn't consumed. For large error responses, this could leave the connection in a bad state.

**Impact:** Connection pool exhaustion if many large error responses occur.

**Recommendation:** Either drain the body fully or use `io.Copy(os.Discard, resp.Body)` before returning error.

---

### 6. Swift Audio Capture - Printf-style Error Handling
**File:** `cmd/capture/main.swift` (lines 424-426)

```swift
} catch {
    print("Error writing audio buffer: \(error)")
}
```

**Issue:** Errors in audio buffer writing are silently ignored (only printed). Audio data loss could occur without any notification to the user or retry mechanism.

**Impact:** Silent data loss during recording.

**Recommendation:** Consider track failed writes and surface them.

---

### 7. IPC Connection - Potential Goroutine Leak
**File:** `cmd/capture/ipc.go` (lines 96-110)

```go
func (c *IPCClient) waitForSocket(ctx context.Context) error {
    ticker := time.NewTicker(50 * time.Millisecond)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            if _, err := os.Stat(c.socketPath); err == nil {
                return nil
            }
        }
    }
}
```

**Issue:** Ticker is created in every waitForSocket call. If called repeatedly without cleanup, could accumulate.

**Impact:** Minor resource leak over long-running sessions.

---

### 8. Search Index - Raw Query String Passed to FTS
**File:** `internal/search/search.go` (lines 136-162)

```go
func (s *SearchIndex) Search(query string) ([]SearchResult, error) {
    rows, err := s.db.Query(`
        ...
        WHERE meetings_fts MATCH ?
        ...
    `, query)
```

**Issue:** Raw user query passed directly to FTS MATCH. While sqlite3 parameterized queries prevent SQL injection, FTS5 MATCH syntax could allow user to craft expensive queries (e.g., very long wildcard searches).

**Impact:** Potential DoS from expensive FTS queries.

**Recommendation:** Validate query length or complexity before execution.

---

## LOW Severity Issues

### 9. Error Messages Could Leak Sensitive Info
**File:** `internal/providers/router.go` (lines 52-56)

```go
return ProviderSuite{}, notoerr.New("invalid_provider_route", "Real LLM capabilities must route through OpenRouter.", map[string]any{
    "capability": cap,
    "provider":   r.Policy.LLMProvider,
})
```

**Issue:** Error includes the configured LLM provider name which could be considered internal configuration.

**Impact:** Minor information disclosure in error logs.

---

### 10. Config File Permissions - ConfigDirMode
**File:** `internal/config/config.go` (line 180)

```go
if err := os.Chmod(dir, ConfigDirMode); err != nil {
```

**Issue:** Config directory permissions set but not checked what `ConfigDirMode` actually is. If it's 0755 or similar, group may have write access.

**Recommendation:** Verify `ConfigDirMode` is 0700 (owner only).

---

### 11. Missing Validation - MeetingID from User Input
**File:** `internal/cli/cli.go` (lines 666-668)

```go
meetingID, err := uuid.Parse(meetingIDStr)
if err != nil {
    return notoerr.New("invalid_meeting_id", "Invalid meeting ID format.", map[string]any{"id": meetingIDStr})
}
```

**Issue:** MeetingID string is directly passed to error map before validation, which could expose large inputs in error logs.

**Impact:** Minimal, but potential for large error objects.

---

### 12. Silent Failure in File Operations
**File:** `internal/cli/cli.go` (lines 434-443)

```go
if err := os.WriteFile(audioPath, audioData, 0644); err == nil {
    // continues without checking if write actually succeeded for all data
}
```

**Issue:** If write fails silently (e.g., disk full), subsequent operations continue.

**Impact:** Incomplete file write could corrupt audio data.

---

### 13. Missing Close on Database Rows
**File:** `internal/search/search.go` (lines 166-169)

```go
var results []SearchResult
for rows.Next() {
    ...
}
if err := rows.Err(); err != nil {
```

**Issue:** `rows.Close()` is called via defer at line 164, but if there's an early return before reaching the loop, rows might not be closed.

**Impact:** Minor connection leak in edge cases.

---

### 14. TmpDir Path Concatenation Bug
**File:** `internal/storage/meeting.go` (line 40)

```go
tmpPath := layout.TmpDir + ".manifest.tmp"
```

**Issue:** Direct string concatenation assumes TmpDir has no trailing slash. If TmpDir is `/path/.tmp`, result is `/path/.tmp.manifest.tmp` which is incorrect (missing slash).

**Impact:** Could create file in wrong location on certain path configurations.

**Recommendation:** Use `filepath.Join(layout.TmpDir, "manifest.tmp")`.

---

## GOOD PRACTICES OBSERVED

1. **Atomic file writes** - All file writes use temp file + rename pattern
2. **Checksum verification** - Manifests and files have SHA256 checksums
3. **Error wrapping** - Custom errors with codes and context
4. **Context propagation** - All network calls respect context cancellation
5. **Schema validation** - Artifacts validate their schema versions
6. **Environment variable separation** - API keys from env, not config file
7. **Unix socket IPC** - Secure local communication
8. **JSON-RPC protocol** - Well-defined request/response format
9. **Graceful error handling** - Capture helper continues if Swift process fails

---

## SUMMARY

| Severity | Count | Issues |
|----------|-------|--------|
| HIGH | 2 | Path traversal, hardcoded fallback endpoint |
| MEDIUM | 6 | Nil checks, timeout issues, response handling, FTS query validation |
| LOW | 7 | Info disclosure, file operations, resource leaks |

**Overall Assessment:** The codebase demonstrates good security practices overall. Credentials are properly separated from config, atomic writes protect against corruption, and network calls use proper timeouts. The main concerns are around path validation for local storage and avoiding hardcoded fallback endpoints with credentials.