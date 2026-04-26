# iter4-swift-bugs: cmd/capture/main.swift Bug Reports

## Bug Reports for cmd/capture/main.swift

---

### BUG 1: Audio Tap Not Removed Before Engine Stop

**File:** cmd/capture/main.swift  
**Lines:** 406 (install), 288-289 (stop)

**Severity:** CRITICAL

**Description:**
The tap installed at line 406 via `inputNode.installTap(onBus: 0, ...)` is never removed before the audio engine is stopped. AVAudioEngine documentation requires that installed taps be removed before stopping the engine. Calling `audioEngine?.stop()` at line 288 without first removing the tap can cause crashes, resource leaks, or undefined behavior.

```swift
// Line 406: Tap installed
inputNode.installTap(onBus: 0, bufferSize: config.bufferSize, format: nil) { [weak self] buffer, time in
    self?.recordingQueue.async {
        self?.processMicrophoneBuffer(buffer, time: time)
    }
}

// Lines 288-289: Tap still active when engine stops
audioEngine?.stop()
audioEngine = nil
```

**Fix required:** Call `inputNode.removeTap(onBus: 0)` before `audioEngine?.stop()`.

---

### BUG 2: Signal Handler Calls Engine Stop Asynchronously

**File:** cmd/capture/main.swift  
**Lines:** 544-553

**Severity:** CRITICAL

**Description:**
Signal handlers (SIGTERM/SIGINT) at lines 544-553 call `engine.stop()` directly from the signal handler context. This is dangerous because:

1. Signal handlers can interrupt any thread at any point
2. `engine.stop()` accesses `AVAudioEngine` which has thread affinity requirements
3. The `stop()` method accesses `startTime`, `pausedDuration`, `audioFile`, and `outputURL` properties without thread synchronization from the signal context
4. This can cause deadlocks, race conditions, or crashes

```swift
signal(SIGTERM) { _ in
    _ = try? engine.stop()  // DANGEROUS: called from signal context
    try? FileManager.default.removeItem(atPath: socketPathForSignal)
    exit(0)
}
signal(SIGINT) { _ in
    _ = try? engine.stop()  // DANGEROUS: called from signal context
    try? FileManager.default.removeItem(atPath: socketPathForSignal)
    exit(0)
}
```

**Fix required:** Use a signal handler that only sets a flag, then handle cleanup on a known safe thread.

---

### BUG 3: Socket Listener Not Cancelled in deinit

**File:** cmd/capture/main.swift  
**Lines:** 509-511, 497-507, 668-689

**Severity:** HIGH

**Description:**
`SocketServer.deinit` at lines 509-511 only removes the socket file from disk but does not cancel the `NWListenerSocket` (which wraps `NWListener`). The listener continues running and holding resources until the process exits.

```swift
deinit {
    try? FileManager.default.removeItem(atPath: socketPath)  // Only removes file, not listener
}
```

Additionally, `NWListenerSocket` class (lines 668-689) has no `cancel()` or `stop()` method to properly shut down the listener.

**Fix required:** 
1. Add a `cancel()` method to `NWListenerSocket` that calls `listener?.cancel()`
2. Call `listener?.cancel()` in `SocketServer.deinit` before removing the socket file

---

### BUG 4: Race Condition Between Tap Callback and stop()

**File:** cmd/capture/main.swift  
**Lines:** 406-410 (tap), 288-289 (stop), 425-430 (buffer write)

**Severity:** CRITICAL

**Description:**
The tap callback at lines 406-410 can be executing `processMicrophoneBuffer` on `recordingQueue` concurrently with `stop()` being called. The `stop()` method:

1. Calls `audioEngine?.stop()` which does not block waiting for tap callbacks to complete
2. Sets `audioFile = nil` at line 292

Meanwhile, the tap callback at line 425 checks `if let file = audioFile` and may still see a non-nil file, then call `try file.write(from: buffer)` at line 427 on a file that is being closed.

```swift
// processMicrophoneBuffer (runs on recordingQueue)
if let file = audioFile {  // May see non-nil
    do {
        try file.write(from: buffer)  // Crash if stop() set audioFile = nil
    } catch {
        print("Error writing audio buffer: \(error)")
    }
}

// stop() (can run concurrently)
audioEngine?.stop()  // Doesn't wait for tap to finish
audioFile = nil  // Happens while tap may be writing
```

**Fix required:** Add synchronization (e.g., dispatch group) to ensure tap callback completes before `stop()` proceeds.

---

### BUG 5: Concurrent Access to audioFile Without Synchronization

**File:** cmd/capture/main.swift  
**Lines:** 246-252 (write in start), 425-430 (write in tap callback), 292 (nil in stop)

**Severity:** HIGH

**Description:**
`audioFile` is accessed from multiple contexts without proper synchronization:

1. Written at line 252 during `start()`
2. Written by tap callback at line 427 via `processMicrophoneBuffer` on `recordingQueue`
3. Set to nil at line 292 during `stop()`

While `recordingQueue.async` is used for the tap callback, `start()` and `stop()` access `audioFile` on whatever thread calls them (could be socket server's main thread). Multiple threads can write to the same AVAudioFile concurrently, which is not safe.

---

### BUG 6: outputURL Force Unwrap in stop()

**File:** cmd/capture/main.swift  
**Lines:** 298-302

**Severity:** MEDIUM

**Description:**
At line 298, `outputURL` is force-unwrapped in an `if let` but then used without optional chaining:

```swift
if let url = outputURL {  // Line 298
    if let attrs = try? FileManager.default.attributesOfItem(atPath: url.path),
       let size = attrs[.size] as? Int64 {
        sizeBytes = size
    }
}
```

Actually this appears correct - `url` is unwrapped and used within the `if let` block. No bug here.

---

### BUG 7: NWListenerSocket Retain Cycle in stateUpdateHandler

**File:** cmd/capture/main.swift  
**Lines:** 678-680

**Severity:** MEDIUM

**Description:**
In `NWListenerSocket.init`, the `listener?.stateUpdateHandler` closure captures `self` strongly:

```swift
self.listener?.stateUpdateHandler = { [weak self] state in
    self?.stateUpdateHandler?(state)
}
```

Wait - this uses `[weak self]` so it should be fine. Let me re-check.

Actually the inner closure `{ [weak self] state in self?.stateUpdateHandler?(state) }` uses weak self, which is correct.

However, at lines 531-535 in `SocketServer.start()`:
```swift
newListener.stateUpdateHandler = { [weak self] state in
    if case .cancelled = state {
        self?.isRunning = false
    }
}
```

This is also using `[weak self]` correctly.

Let me reconsider - is there a retain cycle issue?

Actually, looking more carefully at `NWListenerSocket`:
- The `init` at line 675 creates `self.listener = try NWListener(...)`
- The listener stores the handlers which reference `self`
- But `[weak self]` breaks the cycle

No bug here with weak self.

---

### BUG 8: Request ID Not Thread-Safe

**File:** cmd/capture/main.swift  
**Lines:** 502, 647

**Severity:** MEDIUM

**Description:**
`requestId` at line 502 is incremented at line 647 without synchronization:

```swift
private var requestId = 1  // Line 502

// Line 647 in handleRequest:
return JSONRPCResponse(id: request.id, result: result, error: error)
```

Wait, the `request.id` comes from the incoming request, not from `requestId`. Let me check if `requestId` is actually used anywhere...

Looking at line 607: `var result: AnyCodable?` - no use of requestId

Looking at line 647: `return JSONRPCResponse(id: request.id, ...`

The `requestId` property at line 502 appears to be unused. The `id` in responses comes from `request.id` which is the client's request ID. This is not a bug but dead code.

---

### BUG 9: listener Property Never Cancelled

**File:** cmd/capture/main.swift  
**Lines:** 539, 497-507, 509-511

**Severity:** HIGH

**Description:**
The `listener` property at line 497 (SocketServer) stores the NWListenerSocket but there's no code path that ever cancels it. When `SocketServer` is deallocated (line 509), only the socket file is removed - the listener continues running.

This is the same issue as Bug 3 but more specific - the listener is never cancelled, only the socket file is deleted.

---

### BUG 10: isRunning Property Not Thread-Safe

**File:** cmd/capture/main.swift  
**Lines:** 501, 533, 540

**Severity:** MEDIUM

**Description:**
`isRunning` at line 501 is:
- Set to `true` at line 540 (on main thread in start())
- Set to `false` at line 533 (on main thread in stateUpdateHandler)
- Read at... (need to find where it's read)

Actually `isRunning` appears to be unused after setting. It's set but never read, so it's dead code. Not a functional bug.

---

## Summary

| Bug # | Severity | Issue |
|-------|----------|-------|
| 1 | CRITICAL | Audio tap not removed before engine stop |
| 2 | CRITICAL | Signal handler calls engine.stop() unsafely |
| 3 | HIGH | Socket listener not cancelled in deinit |
| 4 | CRITICAL | Race condition between tap callback and stop() |
| 5 | HIGH | Concurrent access to audioFile without synchronization |
| 6 | - | (No bug - code is correct) |
| 7 | - | (No bug - weak self used correctly) |
| 8 | - | (Dead code, not a bug) |
| 9 | HIGH | Listener never cancelled (related to #3) |
| 10 | - | (Dead code, not a bug) |

**Total: 5 functional bugs (3 CRITICAL, 2 HIGH)**
