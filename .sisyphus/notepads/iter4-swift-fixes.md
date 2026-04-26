# iter4-swift-fixes: Bug Fixes for cmd/capture/main.swift

## Bug Fixes Applied

---

### BUG 1 + BUG 4 (CRITICAL): Audio Tap Race Conditions

**Problem:** 
- AVAudioEngine tap not removed before engine stop
- Race between tap callback writing to audioFile and stop() setting it to nil

**Fix Applied:**
1. Added `recordingGroup: DispatchGroup` to AudioCaptureEngine (line 203)
2. In tap callback: added `recordingGroup.enter()` before async, `recordingGroup.leave()` at end
3. In stop(): call `audioEngine?.inputNode.removeTap(onBus: 0)` then `recordingGroup.wait()` before `audioEngine?.stop()`

**Files:** cmd/capture/main.swift

---

### BUG 2 (CRITICAL): Signal Handler Calls Engine Stop Unsafely

**Problem:** Signal handlers (SIGTERM/SIGINT) called engine.stop() directly from signal context, endangering thread safety.

**Fix Applied:**
1. Added `shouldExit = false` flag to AudioCaptureEngine
2. In signal handlers: only set `shouldExit = true` and call `CFRunLoopStop(CFRunLoopGetMain())` to stop the run loop gracefully
3. Removed `socketPathForSignal` variable (no longer needed since we don't remove socket in signal handler)

**Files:** cmd/capture/main.swift

---

### BUG 3 (HIGH): Socket Listener Not Cancelled in deinit

**Problem:** NWListenerSocket had no cancel() method, and SocketServer.deinit only removed socket file, not cancelled the listener.

**Fix Applied:**
1. Added `cancel()` method to NWListenerSocket class (calls `listener?.cancel()`)
2. Modified SocketServer.deinit to call `listener?.cancel()` before removing socket file

**Files:** cmd/capture/main.swift

---

### BUG 5 (HIGH): Concurrent audioFile Access

**Problem:** audioFile accessed from multiple contexts (start(), tap callback, stop()) without synchronization.

**Fix:** Fixed via BUG 1/4 changes - the recordingGroup.wait() ensures tap callbacks complete before audioFile is set to nil in stop().

---

## Summary of Changes

| Bug | Severity | Fix |
|-----|----------|-----|
| 1 | CRITICAL | Remove tap before stop + recordingGroup.wait() |
| 2 | CRITICAL | Signal handler only sets flag + stops runloop |
| 3 | HIGH | Added NWListenerSocket.cancel() + call in deinit |
| 4 | CRITICAL | recordingGroup.enter/leave in tap callback |
| 5 | HIGH | Fixed via BUG 1/4 synchronization |
