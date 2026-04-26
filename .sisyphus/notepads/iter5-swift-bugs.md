# Iteration 5: Swift Bug Report - cmd/capture/main.swift

## Files Analyzed
- `cmd/capture/main.swift` (721 lines)

---

## Bug 1: Signal Handler Sets Global `shouldExit`, Not Instance Property

**Location:** `cmd/capture/main.swift:552,557`  
**Severity:** CRITICAL - Logic Bug  
**Category:** Signal Handling

### Description
The signal handlers (`SIGTERM`, `SIGINT`) set `shouldExit = true`, but this references the **global/module-level** `shouldExit` declared in `AudioCaptureEngine` (line 205), NOT the `SocketServer.shouldExit` instance property (line 509).

```swift
// Line 509 - SocketServer has its own shouldExit
private var shouldExit = false  // SocketServer instance property

// Lines 551-559 - Signal handlers in start()
signal(SIGTERM) { _ in
    shouldExit = true  // BUG: References undefined global, not SocketServer.shouldExit!
    CFRunLoopStop(CFRunLoopGetMain())
}
```

The `shouldExit` variable in the signal handler closure refers to a variable in the enclosing scope. The only `shouldExit` in scope is the global one at line 205 (`AudioCaptureEngine.shouldExit`), not `SocketServer.shouldExit` at line 509. This means `SocketServer.shouldExit` is never set by the signal handler.

### Code Snippet
```swift
// Line 205 - AudioCaptureEngine
private var shouldExit = false

// Lines 550-559 - Inside SocketServer.start()
let socketPathForSignal = socketPath
signal(SIGTERM) { _ in
    shouldExit = true  // References what? Global or SocketServer instance?
    CFRunLoopStop(CFRunLoopGetMain())
}
signal(SIGINT) { _ in
    shouldExit = true
    CFRunLoopStop(CFRunLoopGetMain())
}
```

### Impact
Signal handling does not properly communicate shutdown request to `SocketServer`. The `SocketServer.shouldExit` remains `false` even after receiving signals.

---

## Bug 2: `stop()` Nullifies `audioEngine` While Tap Callbacks May Still Execute

**Location:** `cmd/capture/main.swift:289-293`  
**Severity:** HIGH - Thread Safety / Resource Cleanup  
**Category:** Audio Engine Lifecycle

### Description
The `stop()` method nullifies `audioEngine` immediately after `recordingGroup.wait()` returns, but tap callbacks may still be executing (or about to execute) `processMicrophoneBuffer`. If a callback is currently executing and accesses `audioEngine` through `audioEngine?.pause()` in `pause()`, or if any future callback references the engine, it will see `nil`.

```swift
// Lines 289-293
audioEngine?.inputNode.removeTap(onBus: 0)
recordingGroup.wait()

audioEngine?.stop()  // Callback may still be running!
audioEngine = nil   // NULLIFIED while callbacks might access it
```

The tap callback at lines 409-416 captures `[weak self]`, but `processMicrophoneBuffer` accesses `audioFile` which references `outputURL` - not the engine. However, there's no guarantee future modifications won't add engine access.

### Code Snippet
```swift
// Lines 289-295
audioEngine?.inputNode.removeTap(onBus: 0)
recordingGroup.wait()

audioEngine?.stop()
audioEngine = nil  // Danger: callbacks may be in flight
```

### Impact
Potential for nil-dereference if tap callbacks attempt to access the engine after it's nullified.

---

## Bug 3: `getAudioLevel()` Reads `meter` Without Synchronization

**Location:** `cmd/capture/main.swift:365-367`  
**Severity:** MEDIUM - Thread Safety  
**Category:** Concurrent Access

### Description
`getAudioLevel()` reads from `meter` struct without any synchronization, while `updateMeters()` (called from tap callback on `recordingQueue`) writes to `meter`. This is a classic data race.

```swift
// Line 365-367 - Called from any thread
func getAudioLevel() -> [String: Float] {
    return meter.toDict()  // UNSYNCONIZED READ
}

// Line 443-477 - Called from recordingQueue.async
private func updateMeters(buffer: AVAudioPCMBuffer) {
    // ... writes to meter.leftLevel, meter.rightLevel, meter.ambientLevel
    meter.leftLevel = levels[0]
    meter.rightLevel = levels[1]
    meter.ambientLevel = levels.reduce(0, +) / Float(levels.count)
}
```

### Code Snippet
```swift
// Line 365-367
func getAudioLevel() -> [String: Float] {
    return meter.toDict()  // Data race: meter read without synchronization
}
```

### Impact
Concurrent read/write on `meter` causes undefined behavior. Go's RPC handler at line 637 calls this without synchronization.

---

## Bug 4: `processMicrophoneBuffer` Accesses `audioFile` Without Synchronization

**Location:** `cmd/capture/main.swift:431-437`  
**Severity:** HIGH - Thread Safety  
**Category:** Concurrent Access

### Description
The tap callback writes to `audioFile` on `recordingQueue`, but `stop()` sets `audioFile = nil` without any synchronization with the tap callback. The `recordingGroup.wait()` does not prevent the tap callback from mid-execution.

```swift
// Lines 431-437 - In processMicrophoneBuffer (runs on recordingQueue)
if let file = audioFile {  // Read without synchronization
    do {
        try file.write(from: buffer)
    } catch {
        print("Error writing audio buffer: \(error)")
    }
}

// Lines 295-296 - In stop() (main thread)
audioFile = nil  // No synchronization with tap callback
```

### Code Snippet
```swift
// processMicrophoneBuffer - runs on recordingQueue
private func processMicrophoneBuffer(_ buffer: AVAudioPCMBuffer, time: AVAudioTime) {
    var currentState: RecordingState = .idle
    stateQueue.sync { currentState = self.state }
    guard currentState == .recording else { return }
    
    // Write to file - audioFile access unsynchronized
    if let file = audioFile {  // Could be nil if stop() is in progress
        do {
            try file.write(from: buffer)  // Use-after-free possible
        } catch {
            print("Error writing audio buffer: \(error)")
        }
    }
}
```

### Impact
`stop()` can set `audioFile = nil` while a tap callback is writing to it, causing use-after-free or nil-dereference.

---

## Bug 5: `removeTap` Called BEFORE Waiting for In-Flight Callbacks

**Location:** `cmd/capture/main.swift:289-290`  
**Severity:** HIGH - Audio Engine Lifecycle  
**Category:** Thread Safety

### Description
`removeTap` is called before `recordingGroup.wait()`. If a tap callback is currently executing on `recordingQueue` when `removeTap` is called, the callback will NOT be joined by `wait()`. The callback may still be running after `removeTap` returns, and will continue until it calls `recordingGroup.leave()`.

```swift
// Lines 289-290
audioEngine?.inputNode.removeTap(onBus: 0)  // New callbacks blocked, in-flight continues
recordingGroup.wait()  // Waits for enter()/leave() pairs
```

The issue: if a callback has `recordingGroup.enter()` but hasn't yet called `recordingQueue.async`, or is mid-execution, `wait()` will block. But if the tap callback is currently running `processMicrophoneBuffer` and takes a long time, `wait()` blocks the main thread.

### Code Snippet
```swift
// Lines 409-416 - Tap callback
inputNode.installTap(onBus: 0, bufferSize: config.bufferSize, format: nil) { [weak self] buffer, time in
    guard let self = self else { return }
    self.recordingGroup.enter()  // Enter BEFORE async dispatch
    self.recordingQueue.async {
        self.processMicrophoneBuffer(buffer, time: time)
        self.recordingGroup.leave()  // Leave after processing
    }
}
```

### Impact
Long audio buffer processing can cause `stop()` to block for extended time. If the callback hangs, `stop()` deadlocks.

---

## Bug 6: No Timeout on `recordingGroup.wait()` - Potential Deadlock

**Location:** `cmd/capture/main.swift:290`  
**Severity:** MEDIUM - Reliability  
**Category:** Resource Cleanup

### Description
`recordingGroup.wait()` has no timeout. If a tap callback never executes `recordingGroup.leave()` (e.g., due to an exception in `processMicrophoneBuffer`), `stop()` will deadlock indefinitely.

### Code Snippet
```swift
// Line 290
recordingGroup.wait()  // NO TIMEOUT - deadlock possible
```

### Impact
Bug in `processMicrophoneBuffer` (e.g., exception, early return) causes permanent hang of the capture session.

---

## Bug 7: `pause()` / `resume()` State Transitions Are Not Atomic

**Location:** `cmd/capture/main.swift:333-363`  
**Severity:** MEDIUM - Logic Bug  
**Category:** State Management

### Description
Both `pause()` and `resume()` read the current state, then asynchronously update it. There's no lock preventing concurrent modifications. Two threads calling `pause()` simultaneously could cause issues.

```swift
// Lines 333-345 - pause()
func pause() throws -> [String: Any] {
    var currentState: RecordingState = .idle
    stateQueue.sync { currentState = self.state }  // Read
    guard currentState == .recording else {
        throw CaptureError.invalidState("Not recording")
    }
    
    audioEngine?.pause()
    stateQueue.async(flags: .barrier) { self.state = .paused }  // Write - not atomic with read
    pauseStartTime = Date()
    
    return ["status": "paused"]
}
```

### Impact
Race conditions between `pause()` and `resume()` or concurrent `pause()` calls could leave the engine in an inconsistent state.

---

## Bug 8: Tap Installed with `format: nil` - Undefined Behavior

**Location:** `cmd/capture/main.swift:409`  
**Severity:** MEDIUM - Correctness  
**Category:** Audio Configuration

### Description
The tap is installed with `format: nil`, which means AVAudioEngine will use the input node's default format. This is acceptable but can cause issues if the buffer processing assumes a specific format.

```swift
// Line 409
inputNode.installTap(onBus: 0, bufferSize: config.bufferSize, format: nil) { [weak self] buffer, time in
```

While `nil` is technically valid (AVAudioEngine uses the hardware format), it creates potential mismatch when writing to an `AVAudioFile` configured with a different format. The file is created with `settings` dict but the tap provides native hardware format buffers.

### Code Snippet
```swift
// Lines 248-254 - Audio file created with specific format
guard let file = try? AVAudioFile(
    forWriting: url,
    settings: settings  // Specific format: 44100Hz, 2ch, AAC
) else {

// Line 409 - Tap uses hardware format (may differ from file format!)
inputNode.installTap(onBus: 0, bufferSize: config.bufferSize, format: nil) {
```

### Impact
If hardware sample rate differs from the file's configured sample rate, audio will be recorded at wrong speed/pitch, or write operations may fail.

---

## Summary Table

| # | Location | Severity | Category | Issue |
|---|----------|----------|----------|-------|
| 1 | 552, 557 | CRITICAL | Signal Handling | Signal handler sets wrong `shouldExit` variable |
| 2 | 293 | HIGH | Thread Safety | `audioEngine = nil` while callbacks may run |
| 3 | 365-367 | MEDIUM | Thread Safety | `getAudioLevel()` data races on `meter` |
| 4 | 431-437 | HIGH | Thread Safety | `audioFile` access in tap callback unsynchronized with `stop()` |
| 5 | 289-290 | HIGH | Audio Lifecycle | `removeTap` order creates race window |
| 6 | 290 | MEDIUM | Reliability | No timeout on `recordingGroup.wait()` |
| 7 | 333-363 | MEDIUM | State Management | Non-atomic pause/resume state transitions |
| 8 | 409 | MEDIUM | Correctness | Tap format `nil` may mismatch audio file format |

---

## Prioritization

**Critical to fix:**
1. Bug 1 - Signal handling broken
2. Bug 4 - audioFile race between tap and stop
3. Bug 2 - audioEngine lifecycle issue

**High priority:**
4. Bug 5 - removeTap synchronization
5. Bug 3 - meter data race

**Medium priority:**
6. Bug 6 - deadlock potential
7. Bug 7 - state transition races
8. Bug 8 - format mismatch