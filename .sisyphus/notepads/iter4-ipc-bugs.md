# iter4-ipc-bugs: IPC Client Bug Reports

## Summary
Analyzed cmd/capture/ipc.go (425 lines) for connection leaks, deadlocks, error handling bugs, socket cleanup issues, process double-wait bugs, and goroutine leaks.

## Bug Reports

### BUG 1 (CRITICAL): Process Double-Wait - cmd.Wait() in goroutine and c.proc.Wait() in Close()

**File:** cmd/capture/ipc.go  
**Lines:** 71 (goroutine) and 214 (Close)

**Description:**  
Two different code paths call Wait() on the same exec.Cmd:
- Line 71: A spawned goroutine calls `cmd.Wait()` to reap the helper process
- Line 214: `Close()` calls `c.proc.Wait()` 

If both are called on the same exec.Cmd instance, this is a double-wait. According to Go's exec.Cmd.Wait() documentation, it "must not be called simultaneously on multiple goroutines" and "must be called at most once." While Go 1.22+ safely returns an error on the second Wait, this is still incorrect usage.

**Scenario:**
1. EnsureHelperRunning starts helper, spawns goroutine at line 70
2. Goroutine calls cmd.Wait() and blocks waiting for process exit
3. User calls Close() while process still running
4. Close() calls Kill() (line 210) and then Wait() (line 214)
5. Both the goroutine and Close() are now waiting on the same process

**Severity:** CRITICAL

---

### BUG 2 (HIGH): Stale Socket Race Condition - Cleanup goroutine doesn't remove socket when c.proc=nil

**File:** cmd/capture/ipc.go  
**Lines:** 70-77 (cleanup goroutine), 216 (Close sets c.proc = nil)

**Description:**  
The cleanup goroutine at lines 70-77 conditionally removes the socket only if `c.proc != nil && c.proc == cmd`. However, Close() at line 216 sets `c.proc = nil` BEFORE the goroutine runs (if it hasn't run yet). When the goroutine eventually executes, it sees `c.proc == nil` and skips socket removal.

This leaves a stale socket file on the filesystem. While EnsureHelperRunning (line 51-53) removes any existing socket before starting a new helper, this creates a window where:
1. Old socket file exists from previous helper
2. New helper is started with same socket path
3. The socket file might be from the wrong (old) helper

**Scenario:**
1. EnsureHelperRunning starts helper, sets c.proc = cmd
2. Spawns cleanup goroutine that will call cmd.Wait()
3. User calls Close() → sets c.proc = nil, kills process, returns
4. Cleanup goroutine runs → sees c.proc == nil → skips os.Remove(c.socketPath)
5. Socket file remains on filesystem
6. Next EnsureHelperRunning call finds and removes it, but window of staleness exists

**Severity:** HIGH

---

### BUG 3 (HIGH): Goroutine Reference to Local Variable 'cmd'

**File:** cmd/capture/ipc.go  
**Line:** 70-77

**Description:**  
The goroutine captures the local variable `cmd` by reference:
```go
go func() {
    cmd.Wait()
    c.procMu.Lock()
    defer c.procMu.Unlock()
    if c.proc != nil && c.proc == cmd {
        os.Remove(c.socketPath)
    }
}()
```

While `cmd` is saved to `c.proc` at line 68 before the goroutine spawns, the goroutine closure captures `cmd` directly (not `c.proc`). This works because `cmd` is the same value as `c.proc` at that point. However, if the code is modified such that `c.proc` is reassigned before the goroutine runs, the goroutine would still reference the original `cmd`, potentially causing it to skip cleanup when `c.proc` has been changed to a new value.

**Scenario:**
1. cmd = exec.CommandContext(...) creates cmd
2. c.proc = cmd at line 68
3. Goroutine spawns capturing cmd
4. Close() is called, sets c.proc = nil
5. Goroutine runs with original cmd reference → checks c.proc != nil (FALSE) → skips cleanup

This is actually a symptom of BUG 2 - the same race condition.

**Severity:** HIGH

---

### BUG 4 (MEDIUM): Close() Doesn't Remove Socket File

**File:** cmd/capture/ipc.go  
**Lines:** 193-223 (Close function)

**Description:**  
The Close() function kills the helper process but does NOT call `os.Remove(c.socketPath)`. The socket file is only removed by:
1. The cleanup goroutine (conditionally, see BUG 2)
2. EnsureHelperRunning before starting a new helper (line 52)

If Close() is called while the cleanup goroutine is still pending, and the goroutine later skips cleanup due to c.proc == nil, the socket file remains orphaned.

**Scenario:**
1. EnsureHelperRunning starts helper, spawns cleanup goroutine
2. User calls Close() - process killed, but socket NOT removed
3. Cleanup goroutine hasn't run yet (or skips due to c.proc == nil)
4. Socket file remains at c.socketPath

**Severity:** MEDIUM

---

### BUG 5 (MEDIUM): Close() Waits on Process That Might Already Be Waited On

**File:** cmd/capture/ipc.go  
**Lines:** 213-215

**Description:**  
```go
if c.proc.ProcessState == nil {
    c.proc.Wait()
}
```

This check is intended to avoid double-wait, but:
1. If the cleanup goroutine has already called cmd.Wait() and returned, ProcessState is set
2. Close() skips Wait() (correct)
3. But Close() still calls Kill() at line 210 on an already-dead process

While killing an already-dead process returns an error but is harmless, this logic is fragile. If the timing is such that cmd.Wait() is currently blocked in the goroutine while Close() checks ProcessState (which is nil), Close() will call Wait() creating a double-wait situation.

**Severity:** MEDIUM

---

### BUG 6 (MEDIUM): Goroutine Holds procMu During cmd.Wait() - Potential Deadlock

**File:** cmd/capture/ipc.go  
**Lines:** 71-73

**Description:**  
The cleanup goroutine holds `procMu` while calling `cmd.Wait()`:
```go
go func() {
    cmd.Wait()  // Blocks waiting for process exit
    c.procMu.Lock()
    defer c.procMu.Unlock()
    ...
}()
```

If the process refuses to exit (e.g., zombie state, system issue), `cmd.Wait()` blocks indefinitely while holding `procMu`. If another goroutine tries to call EnsureHelperRunning (which needs procMu), it will deadlock waiting for procMu which is held by the blocked goroutine.

**Scenario:**
1. Helper process becomes zombie (exited but not reaped)
2. cmd.Wait() in goroutine blocks forever waiting for zombie
3. Goroutine holds procMu
4. Any call to EnsureHelperRunning blocks on procMu

Note: This is unlikely in practice because Kill() sends SIGKILL which should terminate any process. But the code structure allows this deadlock possibility.

**Severity:** MEDIUM

---

### BUG 7 (LOW): EnsureHelperRunning Failure Leaves Orphaned Process Reference

**File:** cmd/capture/ipc.go  
**Lines:** 60-68, 82-86

**Description:**  
When EnsureHelperRunning succeeds in starting the helper (cmd.Start() succeeds) but waitForSocket fails, the code:
1. Kills the process (line 83)
2. Sets c.proc = nil (line 84)
3. Removes socket (line 85)
4. Returns error

The cleanup goroutine at line 70 is still pending with its reference to `cmd`. When it eventually runs, cmd.Wait() returns immediately (process killed) and it tries to remove socket - but this is harmless since socket is already removed.

However, if Close() is called before the goroutine runs, c.proc is already nil, so Close() skips process cleanup entirely. The goroutine's cmd.Wait() still works (returns immediately), so no leak occurs, but the cleanup logic is fragile.

**Severity:** LOW

---

## Patterns Identified

### Concurrency Model
- Two mutexes: `mu` (protects conn) and `procMu` (protects proc)
- Cleanup goroutine spawned without synchronization mechanism
- No explicit waiting for cleanup goroutine to complete in Close()

### Socket Lifecycle
- Socket created by helper process
- Socket removal happens in 3 places with race conditions
- No atomic "create/cleanup" of socket lifecycle

### Process Lifecycle  
- Process reference stored in c.proc
- Cleanup goroutine waits on process
- Close() also waits on process - double-wait pattern
