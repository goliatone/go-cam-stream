import AppKit
import AVFoundation
import Combine

final class CameraModel: ObservableObject {
    @Published var capture = CaptureState()
    @Published var permission = "not_determined"
    @Published var draft = CameraConfig()
    @Published var portText = "8554"
    @Published var saved: CameraConfig?
    @Published var devices: [CameraDevice] = []
    @Published var ready = false
    @Published var working = false
    @Published var section = 0
    @Published var notice: String?
    @Published var exported: [URL] = []
    private let client = HelperClient()
    private var timer: Timer?
    private var quitting = false
    private var exited = false
    var didChange: (() -> Void)?
    var didExit: (() -> Void)?
    // Separate from the diagnostic CLI/proof, stable across app replacements.
    let directory = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/Application Support/camstream-camera-app")

    var config: CameraConfig {
        var value = draft
        value.port = Int(portText) ?? 0
        if value.listen == "127.0.0.1" { value.host = "localhost" }
        return value
    }
    var dirty: Bool { saved != config }
    var canStart: Bool { ready && !working && !capture.active && permission == "authorized" && saved != nil && !dirty }
    var canStop: Bool { ready && (capture.phase == .starting || capture.phase == .streaming) }
    var canSave: Bool { ready && !working && !capture.active && permission == "authorized" && validation == nil && dirty }
    var canExport: Bool { ready && !working && !capture.active && saved != nil && !dirty }
    var validation: String? {
        if draft.path.range(of: "^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$", options: .regularExpression) == nil {
            return "Use 1–64 letters, numbers, dashes or underscores for the camera name."
        }
        if !(1024...65535).contains(Int(portText) ?? 0) { return "Choose a port from 1024 to 65535." }
        if draft.listen == "0.0.0.0" {
            let host = draft.host
            if host.range(of: "^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$", options: .regularExpression) == nil || host.contains("..") || ["localhost", "127.0.0.1", "0.0.0.0"].contains(host) {
                return "Enter this Mac’s private IP address or hostname, without rtsp:// or a port."
            }
        }
        return nil
    }
    var title: String {
        switch capture.phase {
        case .stopped: return "Camera stopped"
        case .starting: return "Starting camera…"
        case .streaming: return "Camera live"
        case .stopping: return "Stopping camera…"
        case .error: return "Camera needs attention"
        }
    }

    func launch() {
        refreshPermission()
        client.onReady = { [weak self] in
            guard let self = self else { return }
            self.ready = true
            self.client.request("config") { [weak self] reply in
                guard let self = self else { return }
                if let value: CameraConfig = self.decode(reply["result"]) {
                    self.saved = value; self.draft = value; self.portText = String(value.port)
                } else if self.errorCode(reply) != "not_configured" { self.showError(reply) }
                if self.permission == "authorized" { self.refreshDevices() }
                self.changed()
            }
        }
        client.onEvent = { [weak self] reply in
            guard let self = self else { return }
            if reply["event"] as? String == "capture_stopped" {
                self.capture.captureEnded(self.errorCode(reply).map(cameraMessage))
            } else { self.showError(reply) }
            self.changed()
        }
        client.onFailure = { [weak self] in
            guard let self = self, !self.quitting else { return }
            self.ready = false; self.working = false
            if self.capture.problem == nil { self.capture.failed(cameraMessage("transport")) }
            self.changed()
        }
        client.onExit = { [weak self] in
            guard let self = self else { return }
            self.ready = false; self.exited = true
            self.changed(); self.didExit?()
        }
        client.launch(executable: Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/camstream-camera"), directory: directory)
        timer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in self?.tick() }
    }

    func refreshPermission() {
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized: permission = "authorized"
        case .denied: permission = "denied"
        case .restricted: permission = "restricted"
        default: permission = "not_determined"
        }
        changed()
    }
    func requestPermission() {
        guard permission == "not_determined" else { return }
        AVCaptureDevice.requestAccess(for: .video) { [weak self] _ in
            DispatchQueue.main.async {
                self?.refreshPermission()
                if self?.permission == "authorized" { self?.refreshDevices() }
            }
        }
    }
    func openPrivacy() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Camera") { NSWorkspace.shared.open(url) }
    }
    func refreshDevices() {
        guard ready, !working, !capture.active, permission == "authorized" else { return }
        working = true; changed()
        client.request("devices") { [weak self] reply in
            guard let self = self else { return }
            self.working = false
            if let devices: [CameraDevice] = self.decode(reply["result"]) { self.devices = devices }
            else { self.showError(reply) }
            self.changed()
        }
    }
    func save() {
        guard canSave else { return }
        working = true; notice = nil; exported = []; changed()
        client.request("configure", config: config) { [weak self] reply in
            guard let self = self else { return }
            self.working = false
            if let value: CameraConfig = self.decode(reply["result"]) {
                self.saved = value; self.draft = value; self.portText = String(value.port)
                self.capture.stopped(); self.notice = "Settings saved. Choose Start Camera when you’re ready."
            } else { self.showError(reply) }
            self.changed()
        }
    }
    func start() {
        refreshPermission()
        guard canStart else { section = 0; changed(); return }
        notice = nil
        let token = capture.start(); changed()
        client.request("start") { [weak self] reply in
            guard let self = self, self.capture.generation == token else { return }
            if self.errorCode(reply) != nil { self.showError(reply) }
            else { self.capture.started(token) }
            self.changed()
        }
    }
    func stop() {
        guard canStop else { return }
        capture.stop(); changed()
        client.request("stop") { [weak self] reply in
            guard let self = self else { return }
            if self.errorCode(reply) != nil { self.showError(reply) }
            else { self.capture.stopped() }
            self.changed()
        }
    }
    func export() {
        guard canExport else { return }
        working = true; notice = nil; changed()
        client.request("export") { [weak self] reply in
            guard let self = self else { return }
            self.working = false
            if let result = reply["result"] as? [String: String],
               let config = result["configuration"], let env = result["environment"],
               config == self.directory.appendingPathComponent("camstream-camera.yaml").path,
               env == self.directory.appendingPathComponent("camstream-camera.env").path {
                self.exported = [URL(fileURLWithPath: config), URL(fileURLWithPath: env)]
                self.notice = "Connection files exported privately. The .env file contains your password; share it only with your Camstream server."
            } else { self.showError(reply) }
            self.changed()
        }
    }
    func revealExports() { if !exported.isEmpty { NSWorkspace.shared.activateFileViewerSelecting(exported) } }
    func copyEndpoint() {
        guard let saved = saved else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(saved.endpoint, forType: .string)
        notice = "Endpoint copied. Credentials are only in the private connection export."
    }
    func quit() -> Bool {
        quitting = true
        timer?.invalidate()
        capture.stop()
        if !exited { client.shutdown() }
        return exited
    }
    private func tick() {
        guard !quitting else { return }
        let oldPermission = permission
        refreshPermission()
        if oldPermission != permission, permission == "authorized" { refreshDevices() }
        guard ready, client.idle, capture.phase == .starting || capture.phase == .streaming else { return }
        client.request("status") { [weak self] reply in
            guard let self = self else { return }
            guard self.capture.phase == .starting || self.capture.phase == .streaming else { return }
            if self.errorCode(reply) != nil {
                let message = cameraMessage(self.errorCode(reply) ?? "transport")
                self.capture.stop()
                self.client.request("stop") { [weak self] _ in
                    self?.capture.failed(message); self?.changed()
                }
            } else if let result = reply["result"] as? [String: Any], let state = result["state"] as? String {
                self.capture.status(state, frames: result["frames"] as? Int ?? 0)
            }
            self.changed()
        }
    }
    private func errorCode(_ reply: [String: Any]) -> String? { (reply["error"] as? [String: Any])?["code"] as? String }
    private func showError(_ reply: [String: Any]) { capture.failed(cameraMessage(errorCode(reply) ?? "transport")) }
    private func decode<T: Decodable>(_ value: Any?) -> T? {
        guard let value = value, JSONSerialization.isValidJSONObject(value),
              let data = try? JSONSerialization.data(withJSONObject: value) else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }
    private func changed() { didChange?() }
}
