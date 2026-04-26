# Decisions - F2 Code Quality Review

## Security Architecture Decisions

### 1. Path Validation Approach
**Decision:** Path traversal in local adapter is HIGH severity but requires careful mitigation.

**Rationale:** While Go's `filepath.Join` resolves `..` components, it doesn't prevent symlink-based escape. The fix should use `filepath.Rel` to verify the final path stays within the base directory.

### 2. Hardcoded R2 Endpoint
**Decision:** The hardcoded fallback endpoint is HIGH severity because R2 credentials could be present.

**Rationale:** Cloudflare R2 uses separate credentials from AWS. If `CLOUDFLARE_ACCOUNT_ID` is not set but `CLOUDFLARE_R2_ACCESS_KEY_ID` and `CLOUDFLARE_R2_SECRET_ACCESS_KEY` are set, the code could attempt to use the hardcoded endpoint with real credentials.

**Resolution:** The function should return an error when required env vars are missing, not a fallback value.

---

## Provider Routing Decisions

### 3. LLM Provider Constraint
**Decision:** Real LLM capabilities must route through OpenRouter only.

**Rationale:** The code explicitly enforces this in `CapabilityRouter.Resolve()` at lines 51-58. This is a business logic constraint enforced in code.

### 4. Speech Provider Routing
**Decision:** Speech providers use a primary + fallback pattern with configurable list.

**Rationale:** Allows graceful degradation when primary provider fails.

---

## Error Handling Philosophy

### 5. Error Codes
**Decision:** Use custom error codes (e.g., `invalid_provider_route`, `missing_credential`) instead of generic messages.

**Rationale:** Allows clients to programmatically handle specific error cases.

### 6. Error Wrapping
**Decision:** All errors are wrapped with context using `notoerr.Wrap()` or created with `notoerr.New()`.

**Rationale:** Provides full stack trace context for debugging while maintaining structured error codes.

---

## Config Security

### 7. API Keys vs Config
**Decision:** API keys stored in Keychain or read from environment variables, never in config files.

**Rationale:** Config files may be committed to version control or have overly permissive access. Keychain provides OS-level protection.

### 8. Config Directory Permissions
**Decision:** Config directory should be created with restricted permissions (0700).

**Rationale:** Config files may contain sensitive routing information and paths.

---

## Audit Scope

### 9. Go Code Review Without Toolchain
**Decision:** Manual review of Go code without `go build` or `go vet`.

**Rationale:** No Go toolchain available. Manual review identified structural issues and potential nil checks but cannot verify compilation.

### 10. Swift Code Review
**Decision:** Review Swift audio capture helper for memory safety and error handling.

**Rationale:** The Swift code handles real-time audio capture and communicates with Go via Unix socket. Issues found include silent error handling for audio buffer writes.
---

## LLM Adapter Bug Fixes (Task Completion)

### 11. Segment ID in Prompts
**Decision:** Include segment IDs in user prompts to prevent LLM from hallucinating IDs that fail `ValidateSummary()`.

**Before:** `Speaker: text` format with no segment IDs
**After:** `[segment_id] Speaker: text` format so LLM knows exact IDs to reference

**Files:** `internal/providers/llm/mistral.go`, `internal/providers/llm/openrouter.go`

### 12. JSON Marshal Error Handling  
**Decision:** Replace `_ = json.Marshal(...)` with proper error handling.

**Before:** `body, _ := json.Marshal(payload)` (silently ignored errors)
**After:** `body, err := json.Marshal(payload)` + `if err != nil { return nil, notoerr.Wrap(...) }`

**Files:** `internal/providers/llm/mistral.go`, `internal/providers/llm/openrouter.go`

### 13. Empty API Key Handling
**Decision:** Only set Authorization header when API key is non-empty.

**Before:** Always set `Bearer ` header even when APIKey was empty
**After:** `if strings.TrimSpace(a.APIKey) != ""` guard before setting header

**Files:** `internal/providers/llm/mistral.go`, `internal/providers/llm/openrouter.go`

### 14. Nil Check on Response Choices
**Decision:** Keep existing nil check pattern; order of checks prevents panics.

The original code already had `if len(resp.Choices) == 0 || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content == ""` which correctly short-circuits. No additional nil check was needed beyond what was already present (the redundant second nil check was removed during review).
