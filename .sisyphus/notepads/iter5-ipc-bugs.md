# iter5-ipc-bugs: IPC Client Bug Reports (Iteration 5)

## Summary
Analyzed cmd/capture/ipc.go (418 lines) for critical bugs in socket lifecycle, process lifecycle, race conditions, and deadlock risks. Built on iter4 findings; focused on subtle concurrency issues and edge cases in the simplified model (cleanup goroutine removed).

---

## Bug Reports

### BUG 1 (CRITICAL): Lock Ordering Deadlock Between Close() and Connect()

**File:** cmd/capture/ipc.go  
**Lines:** 184-216 (Close), 138-182 (Connect)

**Description:**
Lock ordering is inconsistent, creating a potential deadlock:

- **Close()** acquires locks in order: `procMu` → `mu` (lines 197-198, 185-186)
- **Connect()** acquires locks in order: `mu` → calls EnsureHelperRunning which locks `procMu`

If both are called simultaneously on the same IPCClient instance:
1. Thread A (Close): locks `procMu`, tries to lock `mu`
2. Thread B (Connect): locks `mu`, tries to lock `procMu` via EnsureHelperRunning
3. **DEADLOCK** - circular wait

The comment at lines 139-141 acknowledges the risk but doesn't fix it:
```go
// EnsureHelperRunning uses procMu, not mu. Call it outside mu lock to avoid
// deadlock: Connect holds mu -> EnsureHelperRunning locks procMu vs
// Close locks procMu -> waits for mu.
```

This comment only addresses Connect→EnsureHelperRunning ordering, but Close() locks procMu first, then mu. If another goroutine is in Connect (holding mu, about to lock procMu) while Close() is called, deadlock occurs.

**Scenario:**
```go
// Two goroutines sharing IPCClient (unsafe but possible via API misuse)
go func() { client.Connect(ctx) }()
go func() { client.Close() }()
// Potential deadlock depending on timing
```

**Severity:** CRITICAL - API is not thread-safe, deadlock possible with concurrent use

**Note:** Current usage in main.go creates new IPCClient per call, so no deadlock in practice. But the API allows concurrent use.

---

### BUG 2 (LOW): Redundant defer cancel() in EnsureHelperRunning

**File:** cmd/capture/ipc.go  
**Lines:** 70-71

```go
waitCtx, cancel := context.WithTimeout(context.Background(), spawnTimeout)
defer cancel()

if err := c.waitForSocket(waitCtx); err != nil {
```

**Description:**
`defer cancel()` is called immediately after creating the context and before `waitForSocket()` uses it. The cancel function will execute when EnsureHelperRunning returns (after waitForSocket returns), but `waitForSocket` only uses `ctx.Done()` for its timeout. The cancel is dead code—the context is not passed anywhere that outlives this function.

This is not a bug per se (it doesn't cause incorrect behavior), but it's confusing and indicates a copy-paste error or misunderstanding of context lifecycle.

**Severity:** LOW - Functional but confusing

---

### BUG 3 (HIGH): Close() Can Block Indefinitely on Zombie Process

**File:** cmd/capture/ipc.go  
**Lines:** 200-207

```go
c.procMu.Lock()
defer c.procMu.Unlock()

if c.proc != nil && c.proc.Process != nil {
    if err := c.proc.Process.Kill(); err != nil {
        errs = append(errs, err)
    }
    if c.proc.ProcessState == nil {
        c.proc.Wait()
    }
    c.proc = nil
}
```

**Description:**
Wait() blocks until the process exits. If Kill() fails (e.g., process already dead, permission denied, or process in stuck state), and ProcessState is nil, Wait() blocks indefinitely.

Edge case: Process is a zombie (exited but not reaped). Kill() fails with "process already finished" error. ProcessState is nil (Wait not called yet). Next call to Wait() should return immediately since zombie is already terminated. But if the zombie somehow persists and Wait() blocks, we have indefinite blocking.

Additionally, if Kill() silently fails on a live process (extreme edge case: process ignores SIGKILL), Wait() would block forever.

**Severity:** HIGH - No timeout on Wait(), indefinite blocking possible

---

### BUG 4 (MEDIUM): call() Loop Ignores context cancellation during read

**File:** cmd/capture/ipc.go  
**Lines:** 286-314

```go
for {
    select {
    case <-ctx.Done():
        c.conn.Close()
        c.conn = nil
        return fmt.Errorf("context cancelled: %w", ctx.Err())
    default:
    }

    if err := c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
        // ...
    }

    n, err := c.conn.Read(buf)
    if err != nil && err != io.EOF {
        // ...
    }
    // ... continues reading even after ctx.Done() check
}
```

**Description:**
The select with `ctx.Done()` is checked at the START of each loop iteration. But after the check passes, the code proceeds to read from the connection. If reading blocks for longer than the 100ms deadline, ctx.Done() is not re-checked until the NEXT iteration.

This means context cancellation could be delayed by up to 100ms + any processing time. The context deadline is effectively ignored during the read operation.

Additionally, if `c.conn.Read()` blocks longer than the context timeout (e.g., network issue), the function doesn't cancel promptly.

**Severity:** MEDIUM - Context cancellation not promptly honored

---

### BUG 5 (MEDIUM): Unbounded Response Buffer in call()

**File:** cmd/capture/ipc.go  
**Lines:** 283-314

```go
var respBuf bytes.Buffer
buf := make([]byte, 65536)
for {
    // ...
    n, err := c.conn.Read(buf)
    // ...
    respBuf.Write(buf[:n])
    // ...
    if respBuf.Len() > 0 && respBuf.Bytes()[respBuf.Len()-1] == '\n' {
        break
    }
}
```

**Description:**
No maximum buffer size check. If the server sends a very long line without newline (or no newline at all), respBuf grows unboundedly.

Malicious or buggy server: could send 100MB of data without newline. Client would allocate increasingly large buffer, potentially causing memory exhaustion.

The 65536 buffer per read is fine, but the accumulated `respBuf` has no size limit.

**Severity:** MEDIUM - Resource exhaustion possible with malicious server

---

### BUG 6 (LOW): os.Remove(c.socketPath) Error Ignored in Close()

**File:** cmd/capture/ipc.go  
**Line:** 210

```go
os.Remove(c.socketPath)
```

**Description:**
The error from os.Remove is silently ignored. If removal fails (e.g., file already deleted, permission issue), the error is lost. While not critical (socket file is ephemeral and .noto directory is owned by user), it violates best practices for error handling.

**Severity:** LOW - Minor, socket files are transient

---

### BUG 7 (LOW): waitForSocket Has No Total Timeout

**File:** cmd/capture/ipc.go  
**Lines:** 98-112

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

**Description:**
The function relies entirely on `ctx` for timeout. If `ctx` has no deadline (context.Background()), waitForSocket could block forever if the helper never creates the socket. However, in EnsureHelperRunning, waitCtx has a spawnTimeout, so this is mitigated.

But: the ticker interval is 50ms. If ctx times out at exactly the same time a socket is created, we might miss it and return error instead of success.

**Severity:** LOW - Mitigated by caller, but timing race exists

---

## Patterns Identified

### Lock Ordering Risk
- Two mutexes (`mu`, `procMu`) with inconsistent locking order across methods
- Close: procMu → mu
- Connect/call: mu → procMu (via EnsureHelperRunning)
- Potential deadlock with concurrent access

### Context Lifecycle Issues
- Redundant defer cancel() in EnsureHelperRunning
- call() read loop doesn't promptly honor context cancellation
- No total operation timeout in waitForSocket

### Resource Management
- Unbounded response buffer in call()
- os.Remove error ignored
- Wait() with no timeout on potentially zombie process

---

## Iteration Comparison

| Bug | iter4 Status | iter5 Status |
|-----|--------------|--------------|
| Double-wait | Fixed (cleanup goroutine removed) | N/A |
| Stale socket race | Fixed (socket removed in Close) | N/A |
| Goroutine holding procMu | Fixed (cleanup goroutine removed) | N/A |
| Close() not removing socket | Fixed | N/A |
| Lock ordering deadlock | Mentioned but not fully addressed | BUG 1 (CRITICAL) |
| Close() blocking on zombie | Not identified | BUG 3 (HIGH) |
| call() context cancellation | Not identified | BUG 4 (MEDIUM) |
| Unbounded response buffer | Not identified | BUG 5 (MEDIUM) |

---

## Recommendations

1. **BUG 1** requires refactoring lock ordering or using a single mutex
2. **BUG 3** needs timeout on Wait() or ProcessState check improvement
3. **BUG 4** needs more frequent ctx.Done() checks in read loop
4. **BUG 5** needs respBuf size limit check