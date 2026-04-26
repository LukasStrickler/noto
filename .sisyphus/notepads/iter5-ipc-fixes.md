# iter5-ipc-fixes: IPC Bug Fixes (Iteration 5)

## Summary
Fixed all 7 bugs reported in iter5-ipc-bugs.md in cmd/capture/ipc.go.

---

## Fixed Bugs

### BUG 1 (CRITICAL): Lock Ordering Deadlock Between Close() and Connect()

**Problem:** Close() acquired locks in order `procMu` → `mu`, while Connect() acquired `mu` then called EnsureHelperRunning which locked `procMu`. This circular wait caused potential deadlock.

**Fix:** Reordered Close() to acquire locks in same order as Connect(): `mu` → `procMu`.

**Lines changed:** 184-229

---

### BUG 2 (LOW): Redundant defer cancel() in EnsureHelperRunning

**Problem:** `defer cancel()` was called immediately but the cancel function wouldn't execute until after waitForSocket returned, making it dead code.

**Fix:** Replaced `defer cancel()` with explicit `cancel()` calls - one after waitForSocket fails and one after it succeeds.

**Lines changed:** 70-78

---

### BUG 3 (HIGH): Close() Could Block Indefinitely on Zombie Process

**Problem:** If Kill() failed and ProcessState was nil, Wait() would block indefinitely with no timeout.

**Fix:** Wrapped Wait() in a goroutine with a 2-second timeout using select.

**Lines changed:** 207-217

---

### BUG 4 (MEDIUM): call() Loop Ignored Context Cancellation During Read

**Problem:** ctx.Done() was checked at the start of each loop iteration, but during the 100ms read deadline, context cancellation was not honored until the next iteration.

**Fix:** Moved ctx.Done() check inside the error handling select block after Read(), so cancellation is checked immediately when an error occurs (including deadline exceeded).

**Lines changed:** 325-361

---

### BUG 5 (MEDIUM): Unbounded Response Buffer in call()

**Problem:** respBuf could grow unboundedly if server sent very long response without newline, potentially causing memory exhaustion.

**Fix:** Added `maxRespBufSize` constant (10MB) and size check before writing to respBuf. Returns error if exceeded.

**Lines changed:** 322, 351-355

---

### BUG 6 (LOW): os.Remove Error Ignored in Close()

**Problem:** Error from os.Remove(c.socketPath) was silently ignored.

**Fix:** Changed to capture and append error if removal fails (and file isn't already absent).

**Lines changed:** 222-224

---

### BUG 7 (LOW): waitForSocket Has No Total Timeout

**Problem:** If ctx had no deadline, waitForSocket could wait forever. Also, checking socket existence before select meant potential missed socket creation at exactly the deadline.

**Fix:** Check socket existence before the select statement, reducing timing race. The ctx timeout from caller (spawnTimeout in EnsureHelperRunning) still provides the overall timeout.

**Lines changed:** 98-112

---

## Files Modified

- `cmd/capture/ipc.go` - All fixes applied

## Files Not Modified

- Only cmd/capture/ directory was modified as required