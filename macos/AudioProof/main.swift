import AppKit
import AVFoundation

// T01 isolated microphone proof. Uses the unchanged production lifetime helper through a proof-only argv adapter.
final class AudioProof: NSObject, NSApplicationDelegate {
    private var window: NSWindow!
    private let label = NSTextField(wrappingLabelWithString: "Stopped")
    private var helper: Process?
    private var input: FileHandle?
    private var output: FileHandle?
    private var pending = Data()
    private var sequence = 0
    private var startIntent = StartIntent()
    private var quitting = false
    private var active = false
    private var audioUID: String?
    private var timer: Timer?
    private let directory = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/Application Support/camstream-audio-native-proof")

    func applicationDidFinishLaunching(_ notification: Notification) {
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true,
                attributes: [.posixPermissions: 0o700])
        } catch { NSApp.terminate(nil); return }
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1000, height: 380),
            styleMask: [.titled, .closable, .miniaturizable], backing: .buffered, defer: false)
        window.title = "Camstream Audio Proof"
        window.isReleasedWhenClosed = false
        let stack = NSStackView()
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 14
        stack.edgeInsets = NSEdgeInsets(top: 20, left: 20, bottom: 20, right: 20)
        stack.addArrangedSubview(NSTextField(labelWithString: "Native permission and capture lifetime proof"))
        stack.addArrangedSubview(NSTextField(labelWithString: Bundle.main.bundleIdentifier ?? "Unknown identity"))
        label.maximumNumberOfLines = 5
        stack.addArrangedSubview(label)
        let buttons = NSStackView()
        for (title, action) in [("Camera Permission", #selector(permission)), ("Microphone Permission", #selector(microphonePermission)),
                                ("Start Audio", #selector(start)), ("Start Video Only", #selector(startVideo)),
                                ("Stop", #selector(stop)), ("Status", #selector(status)),
                                ("Probe Denial", #selector(probeDenial)), ("Quit", #selector(quit))] {
            buttons.addArrangedSubview(NSButton(title: title, target: self, action: action))
        }
        stack.addArrangedSubview(buttons)
        stack.addArrangedSubview(NSButton(title: "Probe Missing UID", target: self, action: #selector(probeMissing)))
        stack.addArrangedSubview(NSTextField(wrappingLabelWithString:
            "Proof only · loopback port 18557 · optional microphone · no preview or recording. If access is denied, enable Camstream Audio Proof in System Settings → Privacy & Security → Camera / Microphone, then relaunch. Closing this window keeps the app alive; Quit stops capture."))
        window.contentView = stack
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        record(["event": "launch", "pid": ProcessInfo.processInfo.processIdentifier,
                "bundle_id": Bundle.main.bundleIdentifier ?? "", "permission": permissionName()])
        label.stringValue = "Camera: \(permissionName()) · Microphone: \(microphoneName()). Capture is stopped."
        launchHelper()
    }

    private func permissionName() -> String {
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .notDetermined: return "not_determined"
        case .restricted: return "restricted"
        case .denied: return "denied"
        case .authorized: return "authorized"
        @unknown default: return "unknown"
        }
    }

    @objc private func permission() {
        AVCaptureDevice.requestAccess(for: .video) { [weak self] granted in
            DispatchQueue.main.async {
                guard let self = self else { return }
                self.record(["event": "permission", "granted": granted, "permission": self.permissionName()])
                self.label.stringValue = "Camera permission: \(self.permissionName()). Capture is stopped."
            }
        }
    }

    private func microphoneName() -> String {
        switch AVCaptureDevice.authorizationStatus(for: .audio) {
        case .authorized: return "authorized"
        case .denied: return "denied"
        case .restricted: return "restricted"
        default: return "not_determined"
        }
    }
    @objc private func microphonePermission() {
        guard !active else { return }
        AVCaptureDevice.requestAccess(for: .audio) { [weak self] granted in
            DispatchQueue.main.async {
                guard let self = self else { return }
                self.record(["event":"microphone_permission", "granted":granted])
                self.label.stringValue = "Camera: \(self.permissionName()) · Microphone: \(self.microphoneName()). Stopped."
            }
        }
    }
    private func selection(_ enabled: Bool, uid: String = "") -> Bool {
        guard let data = try? JSONSerialization.data(withJSONObject:["enabled":enabled,"uid":uid]) else { return false }
        let path = directory.appendingPathComponent("selection.json")
        do {
            try data.write(to:path, options:.atomic)
            try FileManager.default.setAttributes([.posixPermissions:0o600],ofItemAtPath:path.path)
            return true
        } catch { return false }
    }
    private func chooseMicrophone() -> String? {
        // Native metadata discovery does not construct an input or start a capture session.
        let candidates = AVCaptureDevice.devices(for: .audio).filter { $0.localizedName.lowercased().contains("macbook") }
        guard candidates.count == 1 else { return nil }
        return candidates[0].uniqueID
    }
    @objc private func startVideo() { beginCapture(audio:false, denial:false) }
    private func launchHelper() {
        guard selection(false) else { return }
        let process = Process()
        let stdinPipe = Pipe(), stdoutPipe = Pipe()
        process.executableURL = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/camstream-camera")
        process.arguments = ["app-session", "--dir", directory.path]
        process.environment = ["HOME": FileManager.default.homeDirectoryForCurrentUser.path,
                               "PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR": NSTemporaryDirectory(),
                               "CAMSTREAM_AUDIO_PROOF_SELECTION":directory.appendingPathComponent("selection.json").path]
        process.standardInput = stdinPipe
        process.standardOutput = stdoutPipe
        process.standardError = FileHandle.nullDevice
        stdoutPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            if data.isEmpty { handle.readabilityHandler = nil }
            DispatchQueue.main.async { self?.receive(data) }
        }
        process.terminationHandler = { [weak self] process in
            DispatchQueue.main.async {
                guard let self = self else { return }
                self.timer?.invalidate()
                self.startIntent.cancel()
                self.input = nil
                self.record(["event": "helper_exit", "status": process.terminationStatus])
                self.label.stringValue = "Helper exited. Capture cleanup is in progress; relaunch to retry."
                if self.quitting { NSApp.reply(toApplicationShouldTerminate: true) }
            }
        }
        do {
            try process.run()
            helper = process
            input = stdinPipe.fileHandleForWriting
            output = stdoutPipe.fileHandleForReading
            // Close parent copies of child pipe ends; otherwise EOF cannot signal owner loss.
            try stdinPipe.fileHandleForReading.close()
            try stdoutPipe.fileHandleForWriting.close()
            record(["event": "helper_launch", "pid": process.processIdentifier])
            timer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in self?.status() }
        } catch {
            label.stringValue = "Bundled helper could not start."
            record(["event": "helper_launch_failed"])
        }
    }

    @discardableResult
    private func send(_ method: String, config: [String: Any]? = nil) -> String? {
        guard let input = input, helper?.isRunning == true else { return nil }
        sequence += 1
        let id = "\(method)-\(sequence)"
        var request: [String: Any] = ["version": 1, "id": id, "method": method]
        if let config = config { request["config"] = config }
        do {
            var data = try JSONSerialization.data(withJSONObject: request, options: [.sortedKeys])
            data.append(10)
            try input.write(contentsOf: data)
            return id
        } catch {
            startIntent.cancel()
            label.stringValue = "Helper connection closed."
            try? input.close()
            self.input = nil
            return nil
        }
    }

    private func receive(_ data: Data) {
        pending.append(data)
        while let end = pending.firstIndex(of: 10) {
            let line = pending.prefix(upTo: end)
            pending.removeSubrange(...end)
            guard let response = try? JSONSerialization.jsonObject(with: line) as? [String: Any],
                  response["version"] as? Int == 1 else { continue }
            record(response)
            if response["event"] as? String == "capture_stopped" || response["error"] != nil { active = false }
            if let error = response["error"] as? [String: Any] {
                label.stringValue = error["message"] as? String ?? "Camera operation failed."
            } else if let result = response["result"] as? [String: Any], let state = result["state"] as? String {
                label.stringValue = "Camera: \(permissionName()) · Microphone: \(microphoneName()). \(state) (video status only) · frames: \(result["frames"] ?? 0)"
            }
            if startIntent.consume(response["id"] as? String, succeeded: response["error"] == nil) {
                send("start")
            }
        }
    }

    @objc private func start() { beginCapture(audio:true, denial:false) }
    private func beginCapture(audio: Bool, denial: Bool, missing: Bool = false) {
        guard !active else { return }
        guard AVCaptureDevice.authorizationStatus(for: .video) == .authorized else {
            label.stringValue = "Camera permission required."; return
        }
        if audio && !denial && AVCaptureDevice.authorizationStatus(for:.audio) != .authorized {
            label.stringValue = "Microphone permission required. Stopped."
            record(["event":"audio_start_refused", "permission":microphoneName()]); return
        }
        if audio {
            guard let uid = missing ? "camstream-proof-absent-uid" : (audioUID ?? chooseMicrophone()) else { label.stringValue = "Built-in microphone unavailable."; return }
            if !missing { audioUID = uid }
            guard selection(true,uid:uid) else { return }
        } else { guard selection(false) else { return } }
        active = true
        record(["event":"start_intent", "audio":audio, "denial_probe":denial, "missing_probe":missing])
        let id = send("configure", config: ["device":"auto", "listen":"127.0.0.1", "host":"localhost",
                    "port":18557, "path":"microphone-proof", "width":1280,"height":720,"fps":30])
        startIntent.begin(id)
    }

    @objc private func stop() {
        startIntent.cancel()
        active = false
        send("stop")
    }
    // Test-only: verify the OS also denies the bundled capture chain, rather than
    // proving only the native button guard. Requires previously saved settings.
    @objc private func probeDenial() {
        guard AVCaptureDevice.authorizationStatus(for:.audio) == .denied else {
            label.stringValue = "Denial probe requires denied microphone permission."; return
        }
        record(["event":"microphone_denial_probe"])
        beginCapture(audio:true, denial:true)
    }
    @objc private func probeMissing() { beginCapture(audio:true, denial:false, missing:true) }
    @objc private func status() { if !quitting { send("status") } }
    @objc private func quit() { NSApp.terminate(nil) }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        timer?.invalidate()
        startIntent.cancel()
        guard helper?.isRunning == true else { return .terminateNow }
        quitting = true
        record(["event": "quit"])
        send("quit")
        // EOF is the lifetime boundary even if the queued quit request is not processed.
        try? input?.close()
        input = nil
        return .terminateLater
    }
    private func record(_ value: [String: Any]) {
        var value = value
        value["time"] = ISO8601DateFormatter().string(from: Date())
        guard var data = try? JSONSerialization.data(withJSONObject: value, options: [.sortedKeys]) else { return }
        data.append(10)
        let url = directory.appendingPathComponent("proof.jsonl")
        if !FileManager.default.fileExists(atPath: url.path) {
            FileManager.default.createFile(atPath: url.path, contents: nil, attributes: [.posixPermissions: 0o600])
        }
        if let file = try? FileHandle(forWritingTo: url) {
            defer { try? file.close() }
            _ = try? file.seekToEnd()
            try? file.write(contentsOf: data)
        }
    }
}

let app = NSApplication.shared
let delegate = AudioProof()
app.setActivationPolicy(.regular)
app.delegate = delegate
let menu = NSMenu()
let item = NSMenuItem()
let appMenu = NSMenu()
appMenu.addItem(withTitle: "Quit Camstream Audio Proof", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
item.submenu = appMenu
menu.addItem(item)
app.mainMenu = menu
app.run()
