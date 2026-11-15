import Foundation

struct CameraConfig: Codable, Equatable {
    var device = "auto"
    var listen = "127.0.0.1"
    var host = "localhost"
    var port = 8554
    var path = "macbook"
    var width = 1280
    var height = 720
    var fps = 30
    var endpoint: String { "rtsp://\(host):\(port)/\(path)" }
}

struct CameraDevice: Codable, Identifiable {
    let index: String
    let name: String
    var id: String { name }
}

struct CaptureState {
    enum Phase: String { case stopped, starting, streaming, stopping, error }
    private(set) var phase: Phase = .stopped
    private(set) var generation = 0
    private(set) var frames = 0
    private(set) var problem: String?
    var active: Bool { [.starting, .streaming, .stopping].contains(phase) }

    mutating func start() -> Int {
        generation += 1
        phase = .starting
        problem = nil
        frames = 0
        return generation
    }
    mutating func started(_ token: Int) {
        // A late start acknowledgement cannot undo Stop or a capture failure.
        guard token == generation, phase == .starting else { return }
    }
    mutating func stop() {
        generation += 1
        phase = .stopping
        problem = nil
    }
    mutating func stopped() { phase = .stopped; frames = 0; problem = nil }
    mutating func status(_ state: String, frames: Int) {
        guard phase == .starting || phase == .streaming else { return }
        if state == "streaming", frames > 0 { phase = .streaming; self.frames = frames }
        // Starting may briefly poll stopped before the supervisor socket exists.
        // capture_stopped, not a transient poll, resolves this transition.
    }
    mutating func failed(_ message: String) {
        generation += 1
        phase = .error
        frames = 0
        problem = message
    }
    mutating func captureEnded(_ error: String?) {
        if phase == .stopping { return }
        if let error = error { failed(error) }
        else if phase != .error { stopped() }
    }
}

func cameraMessage(_ code: String) -> String {
    switch code {
    case "busy": return "This camera is already in use. Stop the other Camstream session, then quit and reopen Camstream Camera."
    case "not_configured": return "Save your camera settings before starting."
    case "configuration_failed": return "Settings could not be saved. Check camera permission, refresh the camera list, and try again."
    case "device_unavailable": return "No available webcam was found. Check camera permission and reconnect the camera, then refresh."
    case "capture_failed": return "The camera stopped. Check camera permission and close other apps using the webcam, then try Start Camera again."
    case "export_failed": return "Connection files could not be exported. Stop the camera and try again."
    default: return "The camera helper is unavailable. Quit and reopen Camstream Camera to retry."
    }
}
