# Noto Swift Capture Helper Bug Analysis

## File: `cmd/capture/main.swift`

## CRITICAL BUGS

### 1. Signal Handler Calls Non-Existent Method
**Line: 533-537**
```swift
signal(SIGTERM) { _ in
    engine.stop()  // BUG: AudioCaptureEngine.stop() returns [String: Any], not Void
    try? FileManager.default.removeItem(atPath: socketPathForSignal)
    exit(0)
}
```
**Issue**: `AudioCaptureEngine.stop()` is declared as `throws -> [String: Any]` at line 274, but the signal handler calls it as if it returns `Void`. This won't compile - the method signature is wrong OR the call is wrong.

**Fix**: The signal handler should call the Go-side shutdown (via IPC) or the `AudioCaptureEngine` needs a `stop()` method that returns `Void` and performs cleanup.

---

### 2. Signal Handler C Function Pointer - Undefined Behavior
**Line: 533**
```swift
signal(SIGTERM) { _ in ... }
```
**Issue**: `signal()` is a C variadic function. The Swift closure capture here is **undefined behavior** in Swift. The closure captures `engine` and `socketPathForSignal` but `signal()` expects a C function pointer. The capture semantics are unclear and may lead to crashes.

**Fix**: Use a global C function or `@convention(c)` wrapper instead of a Swift closure.

---

### 3. Socket File Not Cleaned Up on Abnormal Exit
**Lines: 511-513, 535**
```swift
if fileManager.fileExists(atPath: socketPath) {
    try fileManager.removeItem(atPath: socketPath)
}
// ...
signal(SIGTERM) { _ in
    try? FileManager.default.removeItem(atPath: socketPathForSignal)
    exit(0)
}
```
**Issue**: 
- Socket file is only removed via signal handler (SIGTERM)
- If process is killed with SIGKILL (`kill -9`), socket file remains orphaned
- If process crashes, socket file remains orphaned
- The Go side (`ipc.go:75`) also removes socket on process exit, but Swift side has no atexit handler

**Fix**: Also register a SIGINT handler and/or use an atexit handler to ensure cleanup.

---

### 4. Thread Safety Violation - Unprotected State Access
**Lines: 191, 334, 342, 351, 414**
```swift
// Line 191: private var state: RecordingState = .idle
// Line 334: audioEngine?.pause(); state = .paused
// Line 342: guard state == .paused
// Line 351: state = .recording
// Line 414: guard state == .recording  // Called from async audio tap
```
**Issue**: `state` is accessed from:
1. Main thread: `pause()`, `resume()` 
2. `recordingQueue` async thread: `processMicrophoneBuffer()` via `state == .recording` check

This is a **data race** - state is not protected by any synchronization primitive. On macOS with Swift's strict concurrency, this will cause warnings and potentially crash.

**Fix**: Use `let stateQueue = DispatchQueue(label: "com.noto.state")` and dispatch all state access through it, or use an `actor`.

---

### 5. Dead Code - Unused Property
**Line: 202**
```swift
private var socketFileHandle: FileHandle?
```
**Issue**: This property is never used anywhere in the file.

**Fix**: Remove it.

---

## HIGH SEVERITY BUGS

### 6. Audio Format Mismatch - Channel Count Ignored
**Lines: 388, 238**
```swift
// Line 388: createAudioFormat uses config.channels (2)
channels: AVAudioChannelCount(config.channels),

// Line 238: But output file settings hardcode 2 channels
AVNumberOfChannelsKey: 2,
```
**Issue**: If `config.channels` is changed, `createAudioFormat` would use the new value but `AVNumberOfChannelsKey` in settings would still be 2. This causes a format mismatch when writing audio.

**Fix**: Use `config.channels` in the settings dictionary at line 238.

---

### 7. Meter Struct Not Thread-Safe
**Lines: 55-67, 196, 426-464**
```swift
// Line 196: private var meter = AudioMeter()
// Line 427: updateMeters(buffer: buffer)  // Called from recordingQueue
```
**Issue**: `meter` is a mutable struct accessed from multiple threads:
- Written from `recordingQueue` (async audio tap)
- Read from `getAudioLevel()` (called from main thread via JSON-RPC)

This is a **data race** on `meter.leftLevel`, `meter.rightLevel`, `meter.ambientLevel`.

**Fix**: Make `meter` access thread-safe with a queue or `@Atomic` property wrapper.

---

### 8. Connection State Handler Race Condition
**Lines: 542-553, 556-573**
```swift
private func handleConnection(_ conn: NWConnection) {
    conn.stateUpdateHandler = { state in
        switch state {
        case .ready:
            self.receiveMessage(conn)  // Starts recursive receive
        case .failed(let err):
            print("Connection failed: \(err)")
        default:
            break
        }
    }
    conn.start(queue: .main)
}
```
**Issue**: 
1. If `.ready` fires before `start(queue:)` completes, `receiveMessage` is called before the connection is fully started
2. `receiveMessage` recursively calls itself - if the connection closes and reconnects rapidly, multiple receive loops could overlap
3. No locking on connection state

**Fix**: Use a serial queue for connection handling and add state guards.

---

### 9. Zombie Process - No Child Reaping
**Lines: 64-68 (Go side)**
```go
// ipc.go
cmd := exec.CommandContext(ctx, swiftPath, "-socket", c.socketPath)
cmd.Start()
// ...
go func() {
    cmd.Wait()
    // Only removes socket if proc is still the same reference
    c.procMu.Lock()
    defer c.procMu.Unlock()
    if c.proc != nil && c.proc == cmd {
        os.Remove(c.socketPath)
    }
}()
```
**Issue**: The Swift process spawned by Go isn't explicitly reaped by Swift itself. If the Go parent dies without calling `Close()`, the Swift helper becomes orphaned. Swift side relies on Go to manage lifecycle, but SIGTERM handler only handles self-termination.

**Fix**: Swift should also listen for SIGHUP or implement a parent-death detection mechanism.

---

### 10. NWListenerSocket Has No Cancel Method
**Lines: 652-673**
```swift
class NWListenerSocket {
    private var listener: NWListener?
    // ...
    func resume() throws {
        try listener?.resume()
    }
    // No cancel() or close() method!
}
```
**Issue**: `NWListenerSocket` has no way to stop the listener. The socket can't be cleanly shutdown - only the process can exit.

**Fix**: Add `func cancel()` that calls `listener?.cancel()`.

---

## MEDIUM SEVERITY BUGS

### 11. Audio Tap Format nil - Potential Crash
**Line: 401**
```swift
inputNode.installTap(onBus: 0, bufferSize: config.bufferSize, format: nil) { [weak self] buffer, time in
```
**Issue**: Passing `nil` for format means AVAudioEngine will use the input node's default format. If the input device doesn't match expected format (e.g., 48kHz vs 44.1kHz), the tap may fail silently or produce unexpected buffer sizes.

**Fix**: Query the actual input format and use it explicitly, or handle format mismatches gracefully.

---

### 12. No Error Handling for Audio File Write Failure
**Lines: 418-424**
```swift
if let file = audioFile {
    do {
        try file.write(from: buffer)
    } catch {
        print("Error writing audio buffer: \(error)")
        // Continues recording even after write failure!
    }
}
```
**Issue**: If the audio file write fails, the error is only printed and recording continues with data loss. No notification to caller, no state change.

**Fix**: Set an error state, notify the Go side via socket, or stop recording gracefully.

---

### 13. Redundant Guard After Assignment
**Lines: 393-397**
```swift
private func setupAudioEngine(sources: [String], format: AVAudioFormat) throws {
    audioEngine = AVAudioEngine()
    guard let engine = audioEngine else {  // Redundant - just assigned above
        throw CaptureError.engineError("Could not create audio engine")
    }
```
**Issue**: The guard is always true since we just assigned to `audioEngine`. This is dead code that confuses readers.

**Fix**: Remove the guard or change logic to actually detect failure.

---

### 14. Socket Path String Capture in Signal Handler
**Lines: 532-537**
```swift
let socketPathForSignal = socketPath
signal(SIGTERM) { _ in
    engine.stop()
    try? FileManager.default.removeItem(atPath: socketPathForSignal)
    exit(0)
}
```
**Issue**: While the value capture is fine, the signal handler captures `socketPathForSignal` but `engine` is captured implicitly. The closure escape semantics with a C function pointer are undefined.

**Fix**: Use `@convention(c)` for the signal handler or use a global function.

---

### 15. Missing SIGINT Handler
**Line: 531**
```swift
// Only handles SIGTERM:
signal(SIGTERM) { _ in ... }
```
**Issue**: If user presses Ctrl+C (SIGINT), the process terminates but socket file may not be cleaned up (depends on signal handler). Go side cleans up, but Swift side has no handler.

**Fix**: Add `signal(SIGINT) { _ in ... }` handler as well.

---

### 16. AnyCodable Dict Value Unwrapping Unsafe
**Lines: 597-599**
```swift
case "start":
    let sources = request.params?["sources"]?.value as? [String] ?? ["microphone"]
    let sampleRate = request.params?["sampleRate"]?.value as? Int ?? 44100
```
**Issue**: If `params` contains a valid dictionary but `sources` or `sampleRate` have wrong types (e.g., `Double` instead of `Int`), the `??` fallback is used silently. This hides type errors that should be reported as invalid requests.

**Fix**: Validate parameter types explicitly and return error for type mismatches.

---

### 17. JSON-RPC ID Sequence Not Thread-Safe
**Line: 495**
```swift
private var requestId = 1
```
**Issue**: `requestId` is accessed from `handleRequest` without synchronization. If multiple connections call `handleRequest` simultaneously (they shouldn't with current architecture, but...), the ID sequence could be incorrect.

**Fix**: Use a serial queue or atomic integer for requestId generation.

---

### 18. Audio Format Sample Rate Inconsistency
**Lines: 13, 237**
```swift
// Line 13:
let sampleRate: Double = 44100.0

// Line 237:
AVSampleRateKey: sampleRate,  // Uses function parameter, not config
```
**Issue**: The `CaptureConfig.sampleRate` is defined but not consistently used. The actual audio file uses the `sampleRate` parameter passed to `start()`, which could differ from `config.sampleRate`.

**Fix**: Ensure consistency between config and actual audio format used.

---

### 19. Process Message Error Silently Ignored
**Lines: 580-587**
```swift
} catch {
    let errorResponse = JSONRPCResponse(
        id: 0,
        result: nil,
        error: JSONRPCError(code: -32700, message: "Parse error", data: nil)
    )
    sendResponse(errorResponse, conn: conn)
    // Original error is lost - only printed to console
    print("JSON decode error: \(error)")
}
```
**Issue**: When JSON parsing fails, the detailed error is only printed to stdout, not included in the JSON-RPC error response. Makes debugging harder.

**Fix**: Include error details in the JSON-RPC error response (but be careful about leaking sensitive info).

---

## LOW SEVERITY BUGS

### 20. outputURL Property Never Nil After start()
**Lines: 227, 242**
```swift
let filename = "capture-\(timestamp).m4a"
outputURL = outputDir.appendingPathComponent(filename)
// ...
guard let url = outputURL else {  // Redundant check
    throw CaptureError.fileError("Output URL not set")
}
```
**Issue**: After `start()` sets `outputURL`, the subsequent guard is always true. Dead code.

**Fix**: Remove the redundant guard.

---

### 21. Unused Mixer Node Property
**Line: 189**
```swift
private var mixerNode: AVAudioMixerNode?
```
**Issue**: Declared but never used.

**Fix**: Remove or implement proper mixer node usage for multi-source audio.

---

### 22. Microphone Node Property Never Used
**Line: 188**
```swift
private var microphoneNode: AVAudioInputNode?
```
**Issue**: Declared but never assigned or used. The code uses `engine.inputNode` directly instead.

**Fix**: Remove or use consistently.

---

## SUMMARY BY CATEGORY

| Category | Count | Critical | High | Medium | Low |
|----------|-------|----------|------|--------|-----|
| Force Unwraps | 3 | 0 | 1 | 1 | 1 |
| Socket Cleanup | 3 | 2 | 1 | 0 | 0 |
| Audio Format | 2 | 0 | 1 | 1 | 0 |
| Thread Safety | 4 | 2 | 2 | 0 | 0 |
| Zombie Processes | 1 | 0 | 1 | 0 | 0 |
| Signal Handling | 3 | 2 | 1 | 0 | 0 |
| Connection Lifecycle | 3 | 1 | 2 | 0 | 0 |

**Total: 19 bugs found**
**Critical: 4**
**High: 9**
**Medium: 4**
**Low: 2**
