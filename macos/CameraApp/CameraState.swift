import Foundation
import CryptoKit

struct CameraConfig: Codable, Equatable {
    var device = "auto"
    var listen = "127.0.0.1"
    var host = "localhost"
    var port = 8554
    var path = "macbook"
    var width = 1280
    var height = 720
    var fps = 30
    var audioEnabled = false
    var audioDeviceID = ""
    enum CodingKeys: String, CodingKey {
        case device, listen, host, port, path, width, height, fps
        case audioEnabled = "audio_enabled", audioDeviceID = "audio_device_id"
    }
    init() {}
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        device = try c.decode(String.self, forKey: .device)
        listen = try c.decode(String.self, forKey: .listen)
        host = try c.decode(String.self, forKey: .host)
        port = try c.decode(Int.self, forKey: .port)
        path = try c.decode(String.self, forKey: .path)
        width = try c.decode(Int.self, forKey: .width)
        height = try c.decode(Int.self, forKey: .height)
        fps = try c.decode(Int.self, forKey: .fps)
        audioEnabled = try c.decodeIfPresent(Bool.self, forKey: .audioEnabled) ?? false
        audioDeviceID = try c.decodeIfPresent(String.self, forKey: .audioDeviceID) ?? ""
    }
    func helperObject(audioSupported: Bool) throws -> [String: Any] {
        var value = try JSONSerialization.jsonObject(with: JSONEncoder().encode(self)) as! [String: Any]
        if !audioSupported { value.removeValue(forKey: "audio_enabled"); value.removeValue(forKey: "audio_device_id") }
        return value
    }
    static func == (lhs: Self, rhs: Self) -> Bool {
        lhs.device == rhs.device && lhs.listen == rhs.listen && lhs.host == rhs.host && lhs.port == rhs.port &&
        lhs.path == rhs.path && lhs.width == rhs.width && lhs.height == rhs.height && lhs.fps == rhs.fps &&
        lhs.audioEnabled == rhs.audioEnabled && MicrophoneID(lhs.audioDeviceID) == MicrophoneID(rhs.audioDeviceID)
    }
    var endpoint: String { "rtsp://\(host):\(port)/\(path)" }
}

struct CameraDevice: Codable, Identifiable, Equatable {
    let index: String
    let name: String
    let selector: String?
    var id: String { selector ?? "index:" + index }

    enum CodingKeys: String, CodingKey { case index, name; case selector = "id" }

    func label(in devices: [Self]) -> String {
        guard devices.contains(where: { $0.id != id && $0.name == name }) else { return name }
        return "\(name) · Camera \((Int(index) ?? 0) + 1)"
    }
}

// Swift String equality folds canonical Unicode equivalents. Device identity
// is byte-exact, including in SwiftUI selection keys and dirty-state comparison.
struct MicrophoneID: Hashable {
    let value: String
    init(_ value: String) { self.value = value }
    static func == (lhs: Self, rhs: Self) -> Bool { lhs.value.utf8.elementsEqual(rhs.value.utf8) }
    func hash(into hasher: inout Hasher) { hasher.combine(Data(value.utf8)) }
}

struct MicrophoneDevice: Codable, Identifiable, Equatable {
    let id: String
    let name: String
    var selectionID: MicrophoneID { MicrophoneID(id) }
    static func == (lhs: Self, rhs: Self) -> Bool { lhs.selectionID == rhs.selectionID && lhs.name == rhs.name }
    func matches(_ value: String) -> Bool { selectionID == MicrophoneID(value) }
    static func validID(_ id: String) -> Bool {
        !id.isEmpty && id.utf8.count <= 1024 && !id.unicodeScalars.contains { $0.properties.generalCategory == .control }
    }
    static func validInventory(_ devices: [MicrophoneDevice]) -> Bool {
        Set(devices.map(\.selectionID)).count == devices.count && devices.allSatisfy { validID($0.id) && !$0.name.isEmpty }
    }
    func label(in devices: [MicrophoneDevice]) -> String {
        guard devices.filter({ $0.name == name }).count > 1 else { return name }
        let digest = SHA256.hash(data: Data(id.utf8)).map { String(format: "%02x", $0) }.joined()
        // Lengthen only if this inventory has a short-hash collision.
        let others = devices.filter { !$0.matches(id) }.map { SHA256.hash(data: Data($0.id.utf8)).map { String(format: "%02x", $0) }.joined() }
        var length = 8
        while length < digest.count && others.contains(where: { $0.prefix(length) == digest.prefix(length) }) { length += 4 }
        return "\(name) · \(digest.prefix(length))"
    }
}

struct MicrophoneStatus: Codable, Equatable {
    let enabled: Bool
    let state: String
    let packets: Int
    var live: Bool { enabled && state == "live" && packets > 0 }
}

struct CaptureState {
    enum Phase: String { case stopped, starting, streaming, stopping, error }
    private(set) var phase: Phase = .stopped
    private(set) var generation = 0
    private(set) var frames = 0
    private(set) var problem: String?
    private(set) var audioEnabled = false
    private(set) var audioState = "disabled"
    private(set) var audioPackets = 0
    /// A Stop or Configure acknowledgement confirms release. Once ownership
    /// is known, the current helper can also report that its run has ended.
    private(set) var released = false
    // At launch and after helper loss, an earlier supervisor may still own
    // capture. Only a lock-backed reply resolves that uncertainty.
    private var needsReleaseCheck = true
    var active: Bool { [.starting, .streaming, .stopping].contains(phase) }
    /// Call devices may be claimed only after confirmed cleanup. The stream
    /// error stays visible for recovery on the camera screen.
    var releasedForCall: Bool { (phase == .stopped || phase == .error) && released }

    mutating func start(audioEnabled: Bool = false) -> Int {
        self.audioEnabled = audioEnabled
        audioState = audioEnabled ? "waiting" : "disabled"
        audioPackets = 0
        generation += 1
        phase = .starting
        problem = nil
        frames = 0
        released = false
        return generation
    }
    mutating func started(_ token: Int) {
        // A late start acknowledgement cannot undo Stop or a capture failure.
        guard token == generation, phase == .starting else { return }
    }
    mutating func stop() {
        generation += 1
        phase = .stopping
        audioState = audioEnabled ? "stopping" : "disabled"
        problem = nil
    }
    mutating func stopped(confirmedRelease: Bool = true) {
        phase = .stopped; frames = 0; problem = nil; audioPackets = 0; audioState = audioEnabled ? "stopped" : "disabled"
        if confirmedRelease { confirmReleased() } else { released = false }
    }
    mutating func status(_ state: String, frames: Int, audio: MicrophoneStatus? = nil) {
        guard phase == .starting || phase == .streaming else { return }
        if audioEnabled, let audio = audio, audio.enabled {
            audioState = audio.state; audioPackets = audio.packets
        }
        if state == "streaming", frames > 0, !audioEnabled || audio?.live == true { phase = .streaming; self.frames = frames }
        // Starting may briefly poll stopped before the supervisor socket exists.
        // capture_stopped, not a transient poll, resolves this transition.
    }
    /// `released` records cleanup evidence that arrived with the failure. Nil
    /// keeps the previous evidence, as for a settings or discovery failure.
    mutating func failed(_ message: String, released confirmed: Bool? = nil) {
        generation += 1
        phase = .error
        audioState = audioEnabled ? "failed" : "disabled"
        audioPackets = 0
        frames = 0
        problem = message
        if let confirmed { released = confirmed && !needsReleaseCheck }
    }
    /// Only a run the app still considers starting or streaming can end here,
    /// so a stale event cannot release ownership during or after Stop.
    mutating func captureEnded(_ error: String?) {
        guard phase == .starting || phase == .streaming else { return }
        if let error = error { failed(error, released: true) }
        else { stopped(confirmedRelease: !needsReleaseCheck) }
    }
    /// A matched Stop acknowledgement after an unresolved failure confirms
    /// cleanup without hiding the original error.
    mutating func confirmReleased() {
        guard phase == .error || phase == .stopped else { return }
        needsReleaseCheck = false
        released = true
    }
    /// Helper loss leaves ownership unknown until the capture lock is checked.
    mutating func ownershipLost() {
        needsReleaseCheck = true
        released = false
    }
}

func cameraMessage(_ code: String) -> String {
    switch code {
    case "audio_selection_invalid": return "Choose an available microphone, or turn off Include microphone audio."
    case "audio_device_unavailable": return "The selected microphone is unavailable. Reconnect it, or choose another microphone in Camera settings."
    case "audio_discovery_failed": return "Microphones could not be listed. Check microphone access, then open Camera settings again to list them."
    case "audio_runtime_unsupported": return "Microphone audio isn't available in this version of the camera service. Install a complete, current Camstream Camera, or turn off microphone audio."
    case "audio_startup_timeout": return "No microphone audio arrived within 20 seconds. Check microphone access and the selected device, then choose Start Camera again."
    case "audio_stalled": return "Microphone audio stopped. Check or reconnect the selected microphone, then choose Start Camera again."
    case "audio_observer_failed": return "Microphone audio could not be verified. Capture stopped; quit and reopen the complete Camstream Camera app before retrying."
    case "audio_capture_failed": return "Capture stopped. Check camera and microphone access and device availability, then choose Start Camera again."
    case "busy": return "This camera is already in use. Stop the other Camstream session, then quit and reopen Camstream Camera."
    case "not_configured": return "Camera settings weren’t applied. Choose Try Again to apply them and start."
    case "configuration_failed": return "Settings could not be applied. Check camera permission and the selected webcam, then try again."
    case "device_ambiguous": return "More than one webcam has the saved name. Choose your webcam again in Camera settings."
    case "device_unavailable": return "No available webcam was found. Check camera permission and reconnect the camera, then try again."
    case "capture_format_unsupported": return "This webcam couldn’t open a supported video format. Choose another webcam in Camera settings."
    case "capture_no_frames": return "No video arrived from the selected webcam. Check that it’s connected and ready, then try Start Camera again."
    case "capture_failed": return "The camera stopped. Check camera permission and close other apps using the webcam, then try Start Camera again."
    case "export_failed": return "Connection files could not be exported. Check access to the app’s private folder and try again. Capture is unchanged."
    default: return "The camera service couldn’t complete this action. Try again."
    }
}
