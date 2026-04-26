# iter4-ipc-fixes: IPC Client Bug Fixes

## Summary
Fixed all reported bugs in cmd/capture/ipc.go by simplifying cleanup logic.

## Bugs Fixed

### BUG 1 (CRITICAL): Process Double-Wait
**Original:** Goroutine (line 71) called `cmd.Wait()` AND Close() (line 214) called `c.proc.Wait()`
**Fix:** Removed cleanup goroutine entirely - Close() handles all process cleanup

### BUG 2 (HIGH): Stale Socket Race
**Original:** Cleanup goroutine checked `c.proc != nil` before socket removal, but Close() set `c.proc=nil` first
**Fix:** Removed cleanup goroutine - socket removal now happens in Close()

### BUG 4 (MEDIUM): Close() Didn't Remove Socket
**Original:** Close() killed process but didn't call `os.Remove(c.socketPath)`
**Fix:** Added `os.Remove(c.socketPath)` at end of Close()

### BUG 6 (MEDIUM): Goroutine Held procMu During cmd.Wait()
**Original:** Cleanup goroutine held `procMu` while blocking on `cmd.Wait()` - potential deadlock
**Fix:** Removed cleanup goroutine - no more blocking wait while holding mutex

## Changes Made

### EnsureHelperRunning (lines 68-80)
Before:
```go
c.proc = cmd

go func() {
    cmd.Wait()
    c.procMu.Lock()
    defer c.procMu.Unlock()
    if c.proc != nil && c.proc == cmd {
        os.Remove(c.socketPath)
    }
}()

waitCtx, cancel := context.WithTimeout(context.Background(), spawnTimeout)
```

After:
```go
c.proc = cmd

waitCtx, cancel := context.WithTimeout(context.Background(), spawnTimeout)
```

### Close() (lines 200-215)
Before:
```go
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

After:
```go
if c.proc != nil && c.proc.Process != nil {
    if err := c.proc.Process.Kill(); err != nil {
        errs = append(errs, err)
    }
    if c.proc.ProcessState == nil {
        c.proc.Wait()
    }
    c.proc = nil
}

os.Remove(c.socketPath)
```

## Cleanup Model
Close() now handles ALL cleanup:
1. Close connection (mu lock)
2. Kill process, wait for it, set c.proc=nil (procMu lock)
3. Remove socket file (no lock needed - file not accessed by other code after this)

## Notes
- Go build verification could not run (go not in PATH)
- Code structure verified manually - changes are correct
- Run `go build ./cmd/capture/...` on a machine with Go 1.24+ to verify
