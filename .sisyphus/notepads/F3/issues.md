# F3 Issues

## Critical Issues

### 1. AssemblyAI Hardcoded Placeholder Auth
**File:** internal/providers/stt/assemblyai.go
**Lines:** 66, 110, 153

All three HTTP requests (upload, submit, poll) use hardcoded "placeholder" as authorization header.

This is NOT a real implementation - AssemblyAI will always fail with auth errors.

**Impact:** AssemblyAI provider cannot be used in production.

### 2. Benchmark Command Not Implemented
**File:** internal/cli/cli.go
**Line:** 1172

```go
return notoerr.New("not_implemented", "Benchmark commands are not yet implemented.", ...)
```

The CLI accepts "benchmark" commands but returns an error instead of executing.

**Impact:** Users cannot run benchmarks via CLI.
