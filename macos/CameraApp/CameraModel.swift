import AppKit
import Combine

final class CameraModel: ObservableObject {
    @Published var capture = CaptureState()
    @Published var permission = "not_determined"
    @Published var microphonePermission = "not_determined"
    @Published private(set) var audioSupported = false
    @Published private(set) var microphones: [MicrophoneDevice] = []
    @Published private(set) var microphonesLoaded = false
    @Published private(set) var microphoneDiscoveryError: String?
    @Published private(set) var requestingPermission = false
    private var microphoneRevision = 0
    @Published var draft = CameraConfig()
    @Published var portText = "8554"
    @Published var saved: CameraConfig?
    @Published var devices: [CameraDevice] = []
    @Published var ready = false
    @Published var working = false
    @Published var section = 0 { didSet { syncPreview() } }
    let preview = PreviewController()
    private var windowVisible = false
    @Published var notice: String?
    // Discover existing private exports after launch; export history is not state.
    var exported: [URL] {
        guard saved != nil else { return [] }
        let files = ["camstream-camera.yaml", "camstream-camera.env"].map { directory.appendingPathComponent($0) }
        guard files.allSatisfy({ url in
            guard let attributes = try? FileManager.default.attributesOfItem(atPath: url.path) else { return false }
            return attributes[.type] as? FileAttributeType == .typeRegular
                && (attributes[.posixPermissions] as? NSNumber)?.intValue == 0o600
                && (attributes[.ownerAccountID] as? NSNumber)?.uint32Value == getuid()
        }) else { return [] }
        return files
    }
    private let client: CameraHelper
    private let permissions: CameraPermissions
    private let helper: URL
    private var timer: Timer?
    private var quitting = false
    private var exited = false
    var didChange: (() -> Void)?
    var didExit: (() -> Void)?
    // Separate from the diagnostic CLI/proof, stable across app replacements.
    let directory: URL
    init(client: CameraHelper = HelperClient(), permissions: CameraPermissions = SystemCameraPermissions(),
         directory: URL = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/camstream-camera-app"),
         helper: URL = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/camstream-camera")) {
        self.client = client; self.permissions = permissions; self.directory = directory; self.helper = helper
    }
    var canEditSettings: Bool { ready && !working && !requestingPermission && !capture.active && !quitting }

    var config: CameraConfig {
        var value = draft
        value.port = Int(portText) ?? 0
        if value.listen == "127.0.0.1" { value.host = "localhost" }
        return value
    }
    var dirty: Bool { saved != config }
    var canStart: Bool { canEditSettings && permission == "authorized" && saved != nil && !dirty && validation == nil }
    var canStop: Bool { ready && (capture.phase == .starting || capture.phase == .streaming) }
    var canSave: Bool { canEditSettings && permission == "authorized" && validation == nil && dirty }
    var canExport: Bool { ready && !working && !requestingPermission && !quitting && capture.phase != .starting && capture.phase != .stopping && saved != nil && !dirty && (!config.audioEnabled || audioSupported) }
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
        return microphoneValidation
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

    var microphoneValidation: String? {
        guard config.audioEnabled else { return nil }
        if !audioSupported { return cameraMessage("audio_runtime_unsupported") }
        if microphonePermission != "authorized" { return microphonePermissionText }
        if let error = microphoneDiscoveryError { return error }
        if !microphonesLoaded { return "Refresh the microphone list before saving or starting." }
        if !MicrophoneDevice.validID(config.audioDeviceID) { return "Choose a microphone before saving." }
        if !microphones.contains(where: { $0.matches(config.audioDeviceID) }) { return cameraMessage("audio_device_unavailable") }
        return nil
    }
    var microphonePermissionText: String {
        switch microphonePermission {
        case "authorized": return "Microphone access allowed. Audio starts only with Start Camera."
        case "denied": return "Microphone access is off. Enable Camstream Camera in Privacy & Security → Microphone, then return here."
        case "restricted": return "Microphone access is restricted on this Mac. Contact your device administrator, or disable microphone audio."
        default: return "Allow microphone access to include sound in the stream. Granting access does not start capture."
        }
    }
    var microphoneSummary: String {
        guard config.audioEnabled else { return "Microphone off" }
        if !audioSupported { return "Microphone unavailable — update the app" }
        if requestingPermission { return "Waiting for permission" }
        if microphonePermission == "denied" { return "Microphone access denied" }
        if microphonePermission == "restricted" { return "Microphone access restricted" }
        if microphonePermission != "authorized" { return "Microphone permission required" }
        if capture.phase == .error { return "Microphone needs attention" }
        switch capture.audioState {
        case "live" where capture.phase == .streaming: return "Microphone live"
        case "waiting" where capture.phase == .starting: return "Waiting for microphone audio…"
        case "stopping" where capture.phase == .stopping: return "Stopping microphone…"
        default: return "Microphone enabled · stopped"
        }
    }
    var statusSummary: String { title + " · " + microphoneSummary }

    func launch() {
        preview.request = { [weak self] method, generation, completion in
            self?.client.request(method, previewGeneration: generation, completion: completion)
        }
        refreshPermission()
        client.onReady = { [weak self] in
            guard let self = self else { return }
            self.audioSupported = self.client.audioVersion == 1
            self.ready = true; self.working = true
            self.client.request("config") { [weak self] reply in
                guard let self = self else { return }
                self.working = false
                if let value: CameraConfig = self.decode(reply["result"]) {
                    self.saved = value; self.draft = value; self.portText = String(value.port)
                } else if self.errorCode(reply) != "not_configured" { self.showError(reply) }
                if self.permission == "authorized" { self.refreshDevices() } else { self.refreshMicrophones() }
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
        client.launch(executable: helper, directory: directory)
        timer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in self?.tick() }
    }

    func refreshPermission() {
        permission = permissions.status(.video)
        let next = permissions.status(.audio)
        if next != microphonePermission {
            microphoneRevision += 1; microphones = []; microphonesLoaded = false; microphoneDiscoveryError = nil
        }
        microphonePermission = next
        changed()
    }
    func requestPermission() {
        guard canEditSettings, permission == "not_determined" else { return }
        requestingPermission = true; changed()
        permissions.request(.video) { [weak self] in
            guard let self = self else { return }
            self.requestingPermission = false; self.refreshPermission()
            if self.permission == "authorized" { self.refreshDevices() }
        }
    }
    func setMicrophoneEnabled(_ enabled: Bool) {
        guard canEditSettings, !enabled || audioSupported else { return }
        draft.audioEnabled = enabled
        microphoneRevision += 1; microphoneDiscoveryError = nil
        if enabled { refreshMicrophones() }
        changed()
    }
    func selectMicrophone(_ id: MicrophoneID) {
        guard canEditSettings else { return }
        draft.audioDeviceID = id.value
        changed()
    }
    func requestMicrophonePermission() {
        guard canEditSettings, config.audioEnabled, audioSupported, microphonePermission == "not_determined" else { return }
        requestingPermission = true; changed()
        permissions.request(.audio) { [weak self] in
            guard let self = self else { return }
            self.requestingPermission = false; self.refreshPermission()
            self.refreshMicrophones()
            // Permission completion never resumes an earlier Start intent.
        }
    }
    func openMicrophonePrivacy() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone") { NSWorkspace.shared.open(url) }
    }
    func refreshMicrophones() {
        guard canEditSettings, config.audioEnabled, audioSupported, microphonePermission == "authorized" else { return }
        working = true; microphonesLoaded = false; microphoneDiscoveryError = nil
        microphoneRevision += 1
        let revision = microphoneRevision
        changed()
        client.request("audio_devices") { [weak self] reply in
            guard let self = self else { return }
            self.working = false
            guard self.microphoneRevision == revision, !self.quitting, self.config.audioEnabled else { self.changed(); return }
            if let devices: [MicrophoneDevice] = self.decode(reply["result"]), MicrophoneDevice.validInventory(devices) {
                self.microphones = devices; self.microphonesLoaded = true
            } else {
                self.microphones = []
                self.microphoneDiscoveryError = cameraMessage(self.errorCode(reply) ?? "audio_discovery_failed")
            }
            // Never substitute the first device, even for an empty or missing ID.
            self.changed()
        }
    }
    func openPrivacy() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Camera") { NSWorkspace.shared.open(url) }
    }
    func refreshDevices() {
        guard canEditSettings, permission == "authorized" else { return }
        working = true; changed()
        client.request("devices") { [weak self] reply in
            guard let self = self else { return }
            self.working = false
            if let devices: [CameraDevice] = self.decode(reply["result"]) { self.devices = devices }
            else { self.showError(reply) }
            self.refreshMicrophones()
            self.changed()
        }
    }
    func save() {
        guard canSave else { return }
        working = true; notice = nil; changed()
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
        let token = capture.start(audioEnabled: config.audioEnabled); changed()
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
        let token = capture.generation
        client.request("stop") { [weak self] reply in
            guard let self = self, self.capture.generation == token else { return }
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
                self.notice = "Connection files exported privately. The .env file contains your password; share it only with your Camstream server."
            } else {
                // Export failure must not replace live capture state or detach preview.
                self.notice = cameraMessage(self.errorCode(reply) ?? "export_failed")
            }
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
        syncPreview()
        timer?.invalidate()
        capture.stop()
        if !exited { client.shutdown() }
        return exited
    }
    func tick() {
        guard !quitting else { return }
        let oldPermission = permission
        refreshPermission()
        if oldPermission != permission, permission == "authorized" { refreshDevices() }
        if config.audioEnabled, !microphonesLoaded, microphoneDiscoveryError == nil { refreshMicrophones() }
        if capture.active, capture.phase != .stopping, capture.audioEnabled, microphonePermission != "authorized" {
            stopForFailure(microphonePermissionText); return
        }
        guard ready, client.idle, capture.phase == .starting || capture.phase == .streaming else { return }
        let token = capture.generation
        client.request("status") { [weak self] reply in
            guard let self = self else { return }
            guard self.capture.generation == token, self.capture.phase == .starting || self.capture.phase == .streaming else { return }
            if self.errorCode(reply) != nil {
                let message = cameraMessage(self.errorCode(reply) ?? "transport")
                self.stopForFailure(message)
            } else if let result = reply["result"] as? [String: Any], let state = result["state"] as? String {
                let audio: MicrophoneStatus? = self.decode(result["audio"])
                if (audio?.enabled == true && !self.capture.audioEnabled) || (self.capture.audioEnabled && state == "streaming" && audio?.live != true) {
                    self.stopForFailure(cameraMessage("audio_observer_failed"))
                } else { self.capture.status(state, frames: result["frames"] as? Int ?? 0, audio: audio) }
            } else { self.stopForFailure(cameraMessage("transport")) }
            self.changed()
        }
    }
    private func stopForFailure(_ message: String) {
        capture.stop(); changed()
        let token = capture.generation
        client.request("stop") { [weak self] _ in
            guard let self = self, self.capture.generation == token else { return }
            self.capture.failed(message); self.changed()
        }
    }
    private func errorCode(_ reply: [String: Any]) -> String? { (reply["error"] as? [String: Any])?["code"] as? String }
    private func showError(_ reply: [String: Any]) { capture.failed(cameraMessage(errorCode(reply) ?? "transport")) }
    private func decode<T: Decodable>(_ value: Any?) -> T? {
        guard let value = value, JSONSerialization.isValidJSONObject(value),
              let data = try? JSONSerialization.data(withJSONObject: value) else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }
    func setWindowVisible(_ visible: Bool) { windowVisible = visible; syncPreview() }
    private func syncPreview() {
        preview.update(wanted: !quitting && windowVisible && section == 0 && capture.phase == .streaming,
                       ready: ready && !quitting, supported: client.previewVersion == 1, captureGeneration: capture.generation)
    }
    private func changed() { syncPreview(); didChange?() }
}
