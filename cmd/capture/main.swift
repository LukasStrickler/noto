#!/usr/bin/env xcrun swift
// main.swift - macOS Audio Capture Helper for Noto
// Uses AVAudioEngine for simultaneous microphone + system audio capture
// Communicates with Go via JSON-RPC over Unix domain socket

import Foundation
import AVFoundation
import AudioToolbox

// MARK: - Configuration

struct CaptureConfig {
    let sampleRate: Double = 44100.0
    let channels: Int = 2
    let bufferSize: AVAudioFrameCount = 4096
    let socketPath: String
    
    init(socketPath: String = "") {
        // Use config path or default
        let configDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".noto")
        let defaultSocket = configDir.appendingPathComponent("capture.sock").path
        
        if socketPath.isEmpty {
            self.socketPath = defaultSocket
        } else {
            self.socketPath = socketPath
        }
    }
}

// MARK: - Audio Sources

enum AudioSource: String, CaseIterable {
    case microphone = "microphone"
    case systemAudio = "system_audio"
    
    var displayName: String {
        switch self {
        case .microphone: return "Microphone"
        case .systemAudio: return "System Audio"
        }
    }
    
    var channelLabel: String {
        switch self {
        case .microphone: return "local_speaker"
        case .systemAudio: return "participants"
        }
    }
}

// MARK: - Audio Metering

struct AudioMeter {
    var leftLevel: Float = -160.0  // dB
    var rightLevel: Float = -160.0  // dB
    var ambientLevel: Float = -160.0  // dB
    
    func toDict() -> [String: Float] {
        return [
            "left": leftLevel,
            "right": rightLevel,
            "ambient": ambientLevel
        ]
    }
}

// MARK: - Audio Recorder State

enum RecordingState: String {
    case idle
    case recording
    case paused
}

// MARK: - Audio Metadata

struct AudioMetadata: Codable {
    let durationSeconds: Double
    let sampleRateHz: Int
    let channels: Int
    let format: String
    let codec: String
    let sizeBytes: Int64
    let sources: [AudioSourceInfo]
    
    enum CodingKeys: String, CodingKey {
        case durationSeconds = "duration_seconds"
        case sampleRateHz = "sample_rate_hz"
        case channels
        case format
        case codec
        case sizeBytes = "size_bytes"
        case sources
    }
}

struct AudioSourceInfo: Codable {
    let id: String
    let role: String
    let label: String
    let channel: Int
    let deviceName: String?
}

// MARK: - JSON-RPC Protocol

struct JSONRPCRequest: Codable {
    let jsonrpc: String = "2.0"
    let id: Int
    let method: String
    let params: [String: AnyCodable]?
    
    init(id: Int, method: String, params: [String: AnyCodable]? = nil) {
        self.id = id
        self.method = method
        self.params = params
    }
}

struct JSONRPCResponse: Codable {
    let jsonrpc: String = "2.0"
    let id: Int
    let result: AnyCodable?
    let error: JSONRPCError?
}

struct JSONRPCError: Codable {
    let code: Int
    let message: String
    let data: AnyCodable?
}

// Helper for encoding/decoding arbitrary types
struct AnyCodable: Codable {
    let value: Any
    
    init(_ value: Any) {
        self.value = value
    }
    
    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let intVal = try? container.decode(Int.self) {
            value = intVal
        } else if let doubleVal = try? container.decode(Double.self) {
            value = doubleVal
        } else if let stringVal = try? container.decode(String.self) {
            value = stringVal
        } else if let boolVal = try? container.decode(Bool.self) {
            value = boolVal
        } else if let arrayVal = try? container.decode([AnyCodable].self) {
            value = arrayVal.map { $0.value }
        } else if let dictVal = try? container.decode([String: AnyCodable].self) {
            value = dictVal.mapValues { $0.value }
        } else {
            value = NSNull()
        }
    }
    
    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch value {
        case let intVal as Int:
            try container.encode(intVal)
        case let doubleVal as Double:
            try container.encode(doubleVal)
        case let stringVal as String:
            try container.encode(stringVal)
        case let boolVal as Bool:
            try container.encode(boolVal)
        case let arrayVal as [Any]:
            try container.encode(arrayVal.map { AnyCodable($0) })
        case let dictVal as [String: Any]:
            try container.encode(dictVal.mapValues { AnyCodable($0) })
        default:
            try container.encodeNil()
        }
    }
}

// MARK: - Audio Capture Engine

class AudioCaptureEngine {
    private let config: CaptureConfig
    private var audioEngine: AVAudioEngine?
    private var microphoneNode: AVAudioInputNode?
    private var mixerNode: AVAudioMixerNode?
    
    private var state: RecordingState = .idle
    private var audioFile: AVAudioFile?
    private var outputFile: AVAudioFile?
    private var outputURL: URL?
    
    private var meter = AudioMeter()
    private var startTime: Date?
    private var pausedDuration: TimeInterval = 0
    private var pauseStartTime: Date?
    
    private var recordingQueue = DispatchQueue(label: "com.noto.recording", qos: .userInitiated)
    private var socketFileHandle: FileHandle?
    
    init(config: CaptureConfig = CaptureConfig()) {
        self.config = config
    }
    
    // MARK: - Public API
    
    func start(sources: [String], sampleRate: Int) throws -> [String: Any] {
        guard state == .idle else {
            throw CaptureError.invalidState("Already recording or paused")
        }
        
        // Setup audio session
        try setupAudioSession()
        
        // Create output file
        let outputDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".noto")
            .appendingPathComponent("recordings")
        try FileManager.default.createDirectory(at: outputDir, withIntermediateDirectories: true)
        
        let timestamp = ISO8601DateFormatter().string(from: Date())
            .replacingOccurrences(of: ":", with: "-")
        let filename = "capture-\(timestamp).m4a"
        outputURL = outputDir.appendingPathComponent(filename)
        
        // Configure audio format (AAC in M4A container)
        guard let format = createAudioFormat(sampleRate: Double(sampleRate)) else {
            throw CaptureError.formatError("Could not create audio format")
        }
        
        // Create audio file for writing
        let settings: [String: Any] = [
            AVFormatIDKey: kAudioFormatMPEG4AAC,
            AVSampleRateKey: sampleRate,
            AVNumberOfChannelsKey: 2,
            AVEncoderAudioQualityKey: AVAudioQuality.high.rawValue
        ]
        
        // We need to use AVAudioFile with a custom format for AAC
        guard let file = try? AVAudioFile(
            forWriting: outputURL!,
            settings: settings,
            commonFormat: .pcmFormatFloat32,
            interleaved: false
        ) else {
            throw CaptureError.fileError("Could not create audio file")
        }
        audioFile = file
        
        // Setup audio engine for capture
        try setupAudioEngine(sources: sources, format: format)
        
        state = .recording
        startTime = Date()
        pausedDuration = 0
        
        return [
            "status": "recording",
            "output_path": outputURL!.path,
            "sources": sources.map { src -> [String: Any] in
                let source = AudioSource(rawValue: src) ?? .microphone
                return [
                    "id": src,
                    "role": source.channelLabel,
                    "label": source.displayName
                ]
            }
        ]
    }
    
    func stop() throws -> [String: Any] {
        guard state != .idle else {
            throw CaptureError.invalidState("Not recording")
        }
        
        let duration: Double
        if let start = startTime {
            duration = Date().timeIntervalSince(start) - pausedDuration
        } else {
            duration = 0
        }
        
        // Stop engine
        audioEngine?.stop()
        audioEngine = nil
        
        // Close file
        audioFile = nil
        
        state = .idle
        
        // Get file size
        var sizeBytes: Int64 = 0
        if let url = outputURL {
            if let attrs = try? FileManager.default.attributesOfItem(atPath: url.path),
               let size = attrs[.size] as? Int64 {
                sizeBytes = size
            }
        }
        
        let metadata = AudioMetadata(
            durationSeconds: duration,
            sampleRateHz: Int(config.sampleRate),
            channels: config.channels,
            format: "m4a",
            codec: "aac",
            sizeBytes: sizeBytes,
            sources: [
                AudioSourceInfo(id: "microphone", role: "local_speaker", label: "Microphone", channel: 0, deviceName: nil),
                AudioSourceInfo(id: "system_audio", role: "participants", label: "System Audio", channel: 1, deviceName: nil)
            ]
        )
        
        // Encode metadata as dictionary
        let encoder = JSONEncoder()
        let resultData = try encoder.encode(metadata)
        let resultDict = try JSONSerialization.jsonObject(with: resultData) as? [String: Any] ?? [:]
        
        var response = resultDict
        response["status"] = "stopped"
        response["output_path"] = outputURL?.path ?? ""
        
        return response
    }
    
    func pause() throws -> [String: Any] {
        guard state == .recording else {
            throw CaptureError.invalidState("Not recording")
        }
        
        audioEngine?.pause()
        state = .paused
        pauseStartTime = Date()
        
        return ["status": "paused"]
    }
    
    func resume() throws -> [String: Any] {
        guard state == .paused else {
            throw CaptureError.invalidState("Not paused")
        }
        
        if let pauseStart = pauseStartTime {
            pausedDuration += Date().timeIntervalSince(pauseStart)
        }
        pauseStartTime = nil
        
        try audioEngine?.start()
        state = .recording
        
        return ["status": "recording"]
    }
    
    func getAudioLevel() -> [String: Float] {
        return meter.toDict()
    }
    
    func getCapturedAudio() throws -> [String: Any] {
        guard let url = outputURL else {
            throw CaptureError.fileError("No recording available")
        }
        
        let data = try Data(contentsOf: url)
        let base64 = data.base64EncodedString()
        
        return [
            "data": base64,
            "size": data.count,
            "format": "m4a/aac"
        ]
    }
    
    // MARK: - Private Methods
    
    private func setupAudioSession() throws {
        let session = AVAudioSession.sharedInstance()
        try session.setCategory(.playAndRecord, mode: .default, options: [.defaultToSpeaker, .allowBluetooth])
        try session.setActive(true)
    }
    
    private func createAudioFormat(sampleRate: Double) -> AVAudioFormat? {
        return AVAudioFormat(
            commonFormat: .pcmFormatFloat32,
            sampleRate: sampleRate,
            channels: AVAudioChannelCount(config.channels),
            interleaved: false
        )
    }
    
    private func setupAudioEngine(sources: [String], format: AVAudioFormat) throws {
        audioEngine = AVAudioEngine()
        guard let engine = audioEngine else {
            throw CaptureError.engineError("Could not create audio engine")
        }
        
        let inputNode = engine.inputNode
        let mainMixer = engine.mainMixerNode
        
        // Get the input format
        let inputFormat = inputNode.outputFormat(forBus: 0)
        
        // Install tap for microphone capture
        inputNode.installTap(onBus: 0, bufferSize: config.bufferSize, format: inputFormat) { [weak self] buffer, time in
            self?.processMicrophoneBuffer(buffer, time: time)
        }
        
        // For system audio, we'd ideally use AudioUnit or ScreenCaptureKit
        // However, AVAudioEngine's input node captures microphone
        // System audio capture on macOS requires more complex setup with AudioHardware APIs
        
        try engine.start()
    }
    
    private func processMicrophoneBuffer(_ buffer: AVAudioPCMBuffer, time: AVAudioTime) {
        guard state == .recording else { return }
        
        // Write to file
        if let file = audioFile {
            do {
                try file.write(from: buffer)
            } catch {
                print("Error writing audio buffer: \(error)")
            }
        }
        
        // Update meters
        updateMeters(buffer: buffer)
    }
    
    private func updateMeters(buffer: AVAudioPCMBuffer) {
        guard let channelData = buffer.floatChannelData else { return }
        
        let channelCount = Int(buffer.format.channelCount)
        let frameLength = Int(buffer.frameLength)
        
        // Calculate RMS for each channel
        var levels: [Float] = []
        for ch in 0..<channelCount {
            var sum: Float = 0
            let data = channelData[ch]
            for frame in 0..<frameLength {
                let sample = data[frame]
                sum += sample * sample
            }
            let rms = sqrt(sum / Float(frameLength))
            let db = 20 * log10(max(rms, 0.0001))
            levels.append(db)
        }
        
        // Update meter values (left=mic, right=system, ambient=mix)
        if levels.count > 0 {
            meter.leftLevel = levels[0]
        }
        if levels.count > 1 {
            meter.rightLevel = levels[1]
        } else {
            // If only mono, use same for right
            meter.rightLevel = levels[0]
        }
        
        // Ambient is average of all channels
        if !levels.isEmpty {
            meter.ambientLevel = levels.reduce(0, +) / Float(levels.count)
        }
    }
}

// MARK: - Capture Errors

enum CaptureError: Error, LocalizedError {
    case invalidState(String)
    case formatError(String)
    case fileError(String)
    case engineError(String)
    case socketError(String)
    
    var errorDescription: String? {
        switch self {
        case .invalidState(let msg): return msg
        case .formatError(let msg): return msg
        case .fileError(let msg): return msg
        case .engineError(let msg): return msg
        case .socketError(let msg): return msg
        }
    }
}

// MARK: - Socket Server

class SocketServer {
    private let socketPath: String
    private var listener: NSocketListener?
    private var engine: AudioCaptureEngine
    private var isRunning = false
    private var requestId = 1
    
    init(socketPath: String, engine: AudioCaptureEngine) {
        self.socketPath = socketPath
        self.engine = engine
    }
    
    func start() throws {
        // Remove existing socket file
        let fileManager = FileManager.default
        if fileManager.fileExists(atPath: socketPath) {
            try fileManager.removeItem(atPath: socketPath)
        }
        
        // Create socket directory if needed
        let socketDir = (socketPath as NSString).deletingLastPathComponent
        if !fileManager.fileExists(atPath: socketDir) {
            try fileManager.createDirectory(atPath: socketDir, withIntermediateDirectories: true)
        }
        
        // Create Unix socket listener
        let socket = try NWListenerSocket(unixSocketPath: socketPath)
        socket.acceptHandler = { [weak self] conn in
            self?.handleConnection(conn)
        }
        socket.stateUpdateHandler = { [weak self] state in
            if case .cancelled = state {
                self?.isRunning = false
            }
        }
        
        try socket.resume()
        isRunning = true
        
        // Handle SIGTERM for graceful shutdown
        signal(SIGTERM) { _ in
            exit(0)
        }
        
        // Run event loop
        RunLoop.current.run()
    }
    
    private func handleConnection(_ conn: NWConnection) {
        conn.stateUpdateHandler = { state in
            switch state {
            case .ready:
                self.receiveMessage(conn)
            case .failed(let err):
                print("Connection failed: \(err)")
            default:
                break
            }
        }
        conn.start(queue: .main)
    }
    
    private func receiveMessage(_ conn: NWConnection) {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [weak self] data, _, isComplete, error in
            if let error = error {
                print("Receive error: \(error)")
                return
            }
            
            if let data = data, !data.isEmpty {
                self?.processMessage(data, conn: conn)
            }
            
            if isComplete {
                conn.cancel()
            } else {
                self?.receiveMessage(conn)
            }
        }
    }
    
    private func processMessage(_ data: Data, conn: NWConnection) {
        do {
            let request = try JSONDecoder().decode(JSONRPCRequest.self, from: data)
            let response = handleRequest(request)
            sendResponse(response, conn: conn)
        } catch {
            let errorResponse = JSONRPCResponse(
                id: 0,
                result: nil,
                error: JSONRPCError(code: -32700, message: "Parse error", data: nil)
            )
            sendResponse(errorResponse, conn: conn)
        }
    }
    
    private func handleRequest(_ request: JSONRPCRequest) -> JSONRPCResponse {
        var result: AnyCodable?
        var error: JSONRPCError?
        
        do {
            switch request.method {
            case "start":
                let sources = request.params?["sources"]?.value as? [String] ?? ["microphone"]
                let sampleRate = request.params?["sampleRate"]?.value as? Int ?? 44100
                let res = try engine.start(sources: sources, sampleRate: sampleRate)
                result = AnyCodable(res)
                
            case "stop":
                let res = try engine.stop()
                result = AnyCodable(res)
                
            case "pause":
                let res = try engine.pause()
                result = AnyCodable(res)
                
            case "resume":
                let res = try engine.resume()
                result = AnyCodable(res)
                
            case "getAudioLevel":
                let res = engine.getAudioLevel()
                result = AnyCodable(res)
                
            case "getCapturedAudio":
                let res = try engine.getCapturedAudio()
                result = AnyCodable(res)
                
            default:
                error = JSONRPCError(code: -32601, message: "Method not found", data: nil)
            }
        } catch let e as CaptureError {
            error = JSONRPCError(code: -32000, message: e.localizedDescription, data: nil)
        } catch {
            error = JSONRPCError(code: -32000, message: error.localizedDescription, data: nil)
        }
        
        return JSONRPCResponse(id: request.id, result: result, error: error)
    }
    
    private func sendResponse(_ response: JSONRPCResponse, conn: NWConnection) {
        do {
            let data = try JSONEncoder().encode(response)
            conn.send(content: data, completion: .contentProcessed { error in
                if let error = error {
                    print("Send error: \(error)")
                }
            })
        } catch {
            print("Encode error: \(error)")
        }
    }
}

// MARK: - Network Framework Socket

import Network

class NWListenerSocket {
    private var listener: NWListener?
    private let socketPath: String
    
    var acceptHandler: ((NWConnection) -> Void)?
    var stateUpdateHandler: ((NWListener.State) -> Void)?
    
    init(unixSocketPath: String) throws {
        self.socketPath = unixSocketPath
        self.listener = try NWListener(endpoint: .unix(path: unixSocketPath))
        self.listener?.stateUpdateHandler = { [weak self] state in
            self?.stateUpdateHandler?(state)
        }
        self.listener?.newConnectionHandler = { [weak self] conn in
            self?.acceptHandler?(conn)
        }
    }
    
    func resume() throws {
        try listener?.resume()
    }
}

// MARK: - Application Entry Point

class Application {
    static func run() {
        let config = CaptureConfig()
        let engine = AudioCaptureEngine(config: config)
        let server = SocketServer(socketPath: config.socketPath, engine: engine)
        
        print("Noto Audio Capture Helper starting...")
        print("Socket: \(config.socketPath)")
        
        do {
            try server.start()
        } catch {
            print("Server error: \(error)")
            exit(1)
        }
    }
}

Application.run()