# Issues Found - F2 Code Quality Review

## HIGH Severity

### 1. Path Traversal in localAdapter
- **File:** `internal/storage/adapters/local.go`
- **Lines:** 32-33, 65-66
- **Type:** Security - Path Traversal
- **Status:** Open

The `key` parameter in `PutObject` and `GetObject` is joined with `basePath` using `filepath.Join` without any sanitization. A malicious key like `../../../etc/passwd` could escape the recordings directory.

### 2. Hardcoded R2 Fallback Endpoint
- **File:** `internal/storage/adapters/s3.go`
- **Lines:** 369-375
- **Type:** Security - Credential Exposure
- **Status:** Open

The `getR2Endpoint()` function returns a hardcoded Cloudflare R2 endpoint when `CLOUDFLARE_ACCOUNT_ID` is not set. This hardcoded endpoint could be used in conjunction with credentials.

---

## MEDIUM Severity

### 3. Nil Pointer in OpenRouter Response Parsing
- **File:** `internal/providers/llm/openrouter.go`
- **Lines:** 145-147
- **Type:** Robustness - Nil Check
- **Status:** Open

`resp.Choices[0].Message.Content` accessed without verifying `Message` is not nil.

### 4. No Timeout on Keychain Operations
- **File:** `internal/secrets/keychain.go`
- **Lines:** 20, 31, 47
- **Type:** Robustness - Timeout
- **Status:** Open

The `security` CLI commands could hang indefinitely if keychain is locked.

### 5. HTTP Response Body Not Drained on Error
- **File:** `internal/providers/stt/whisper.go`
- **Lines:** 73-75
- **Type:** Robustness - Connection Management
- **Status:** Open

Same issue exists in `speechmatics.go` and `openrouter.go`.

### 6. Silent Audio Buffer Write Failures
- **File:** `cmd/capture/main.swift`
- **Lines:** 424-426
- **Type:** Robustness - Silent Failure
- **Status:** Open

Errors in audio buffer writing are printed but not surfaced to user or retry mechanism.

### 7. Ticker Leak in waitForSocket
- **File:** `cmd/capture/ipc.go`
- **Lines:** 96-110
- **Type:** Resource Leak
- **Status:** Open

Ticker created on each call without cleanup guarantee across all exit paths.

### 8. Unvalidated FTS Query Complexity
- **File:** `internal/search/search.go`
- **Lines:** 144-160
- **Type:** DoS - Query Complexity
- **Status:** Open

Raw user query passed to FTS MATCH without validation of query complexity.

---

## LOW Severity

### 9. Info Disclosure in Error Messages
- **File:** `internal/providers/router.go`
- **Lines:** 52-56
- **Type:** Information Disclosure
- **Status:** Open

LLM provider name included in error messages.

### 10. Config Directory Permissions Unverified
- **File:** `internal/config/config.go`
- **Line:** 180
- **Type:** Configuration Security
- **Status:** Open

`ConfigDirMode` value not verified to be 0700.

### 11. MeetingID in Error Map Before Validation
- **File:** `internal/cli/cli.go`
- **Lines:** 666-668
- **Type:** Information Disclosure
- **Status:** Open

MeetingID string passed to error map before UUID parsing.

### 12. Silent File Write Failure
- **File:** `internal/cli/cli.go`
- **Lines:** 434-443
- **Type:** Robustness - Silent Failure
- **Status:** Open

Write errors don't stop subsequent operations.

### 13. Rows Close in Early Return
- **File:** `internal/search/search.go`
- **Lines:** 166-169
- **Type:** Resource Leak
- **Status:** Open

Potential rows not closed if error occurs before loop.

### 14. TmpDir Path Concatenation
- **File:** `internal/storage/meeting.go`
- **Line:** 40
- **Type:** Bug - Path Handling
- **Status:** Open

`layout.TmpDir + ".manifest.tmp"` assumes TmpDir has no trailing separator.

---

## Resolved Issues

None identified during this review.