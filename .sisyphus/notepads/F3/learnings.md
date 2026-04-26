# F3: Real Manual QA - Learnings

## Summary
The implementation is largely complete with some hardcoded placeholder values.

## Findings

### 1. cmd/noto/main.go
- Simple 11-line file that delegates to cli.Run()
- Build structure looks valid - standard Go main package
- No issues found

### 2. cmd/capture/main.swift (694 lines)
- Audio capture logic is complete using AVAudioEngine
- JSON-RPC protocol over Unix domain socket implemented
- Microphone capture works, system audio has comment noting complexity
- No TODOs or stubs

### 3. internal/cli/cli.go (1477 lines)
- All CLI commands wired up (record, stop, import-audio, transcribe, summarize, search, etc.)
- benchmark() command returns "not_implemented" error - ISSUE
- No TODOs or stubs in this file

### 4. internal/tui/screens.go (661 lines)
- 5 screens present: Dashboard, Meetings, Search, Detail, Transcript
- All properly routed via renderActive()
- No TODOs or stubs

### 5. internal/storage/adapters/s3.go (463 lines)
- S3 upload fully implemented with PutObject, multipart upload, R2 support
- No TODOs or stubs

### 6. internal/search/search.go (318 lines)
- FTS5 search fully implemented with BM25 ranking
- Virtual table created with proper schema
- No TODOs or stubs

### 7. cmd/noto/main_test.go (522 lines)
- E2E test covers: manifest, audio, transcript, search, summary, checksum verification
- Full pipeline test present
- No TODOs or stubs

### 8. internal/benchmarks/benchmarker.go (468 lines)
- 5 metric categories: transcription, FTS5, S3, TUI (3 sub-metrics)
- All implemented with mock providers
- No TODOs or stubs

## Issues Found

### CRITICAL: AssemblyAI has hardcoded "placeholder" auth header
File: internal/providers/stt/assemblyai.go
Lines: 66, 110, 153

All three HTTP requests use:
```go
req.Header.Set("authorization", "placeholder")
```

This means AssemblyAI provider cannot actually work - auth will always fail.

### Clamp Pattern Panic Fix (internal/tui/shell.go)
The pattern `results[clamp(index, 0, len(results)-1)]` can panic when `len(results)==0` because `clamp(..., 0, -1)` returns 0, and `results[0]` on empty slice panics.

**Fix applied**: Added `if len(results) > 0` guard before clamp access in `searchOverlay()` at shell.go line 214.

**All 5 occurrences verified**:
- input.go:172 - guarded by `if len(results) > 0` at line 171
- screens.go:504 - guarded by `if len(results) == 0 { return }` at line 501
- screens.go:523 - guarded by `if len(results) == 0 { return }` at line 520
- actions.go:514 - guarded by `if len(results) == 0 { return }` at line 510
- shell.go:215 - **FIXED** - added explicit `if len(results) > 0` guard

The `clamp()` function itself handles `maxValue < minValue` by returning `minValue`, but this doesn't prevent the subsequent array access panic.
