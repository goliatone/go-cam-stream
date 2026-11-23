import Darwin
import Foundation

enum HelperFailure: Equatable {
    case launch, runtime, busy, settings, startup, incompatible, timeout, disconnected, sleepInterrupted
}

// One in-flight request, correlated replies, and a bounded queue. No secrets or
// raw helper output are logged. Closing stdin is the authoritative owner boundary.
protocol CameraHelper: AnyObject {
    var onReady: (() -> Void)? { get set }
    var onEvent: (([String: Any]) -> Void)? { get set }
    var onFailure: ((HelperFailure) -> Void)? { get set }
    var onExit: (() -> Void)? { get set }
    var previewVersion: Int { get }
    var audioVersion: Int { get }
    var idle: Bool { get }
    func launch(executable: URL, directory: URL)
    func request(_ method: String, config: CameraConfig?, previewGeneration: String?, completion: @escaping ([String: Any]) -> Void)
    func reserveCallCapture(in directory: URL) -> (() -> Void)?
    func shutdown()
}
// Sharing is an additive capability of the same owner pipe. Requests are typed
// envelopes; sharing_changed events reach only the sharing model.
protocol SharingHelper: AnyObject {
    var sharingVersion: Int { get }
    var onSharingEvent: (([String: Any]) -> Void)? { get set }
    func requestSharing(_ method: SharingMethod, arguments: NativeSharingRequest.Arguments?, completion: @escaping ([String: Any]) -> Void)
}
extension CameraHelper {
    func request(_ method: String, config: CameraConfig? = nil, previewGeneration: String? = nil, completion: @escaping ([String: Any]) -> Void) {
        request(method, config: config, previewGeneration: previewGeneration, completion: completion)
    }

    // Use the supervisor's lock. The native call can outlive the helper pipe
    // while WebKit shuts down its devices, so the app keeps the descriptor.
    func reserveCallCapture(in directory: URL) -> (() -> Void)? {
        let path = directory.appendingPathComponent("instance.lock").path
        let fd = open(path, O_CREAT | O_RDWR | O_NOFOLLOW | O_CLOEXEC, 0o600)
        guard fd >= 0 else { return nil }
        let lock = FileHandle(fileDescriptor: fd, closeOnDealloc: true)
        guard flock(fd, LOCK_EX | LOCK_NB) == 0 else { return nil }
        return { try? lock.close() }
    }
}

final class HelperClient: CameraHelper, SharingHelper, ConversationHelper {
    typealias Reply = [String: Any]
    // Match the Go app-session scanner, including the newline delimiter.
    private static let maxFrameBytes = 16_384
    private struct Request {
        let id: String
        let data: Data
        let completion: (Reply) -> Void
    }
    var onReady: (() -> Void)?
    var onEvent: ((Reply) -> Void)?
    var onFailure: ((HelperFailure) -> Void)?
    var onExit: (() -> Void)?
    var onSharingEvent: ((Reply) -> Void)?
    private var process: Process?
    private var input: DispatchIO?
    private var output: FileHandle?
    private var buffer = Data()
    private var queue: [Request] = []
    private var current: Request?
    private var deadline: DispatchWorkItem?
    private var shutdownDeadline: DispatchWorkItem?
    private let responseTimeout: TimeInterval
    private let shutdownGrace: TimeInterval
    private let terminateGrace: TimeInterval
    private let makeInputPipe: () -> Pipe
    private var sequence = 0
    private var launchGeneration = 0
    private var closing = false
    private var ready = false
    private(set) var previewVersion = 0
    private(set) var audioVersion = 0
    private(set) var sharingVersion = 0
    private(set) var conversationVersion = 0
    private(set) var callRecoveryVersion = 0
    private(set) var callConnectorVersion = 0
    private(set) var guestAdmissionVersion = 0
    var idle: Bool { ready && current == nil && queue.isEmpty && !closing }

    init(responseTimeout: TimeInterval = 30, shutdownGrace: TimeInterval = 12, terminateGrace: TimeInterval = 3,
         makeInputPipe: @escaping () -> Pipe = { Pipe() }) {
        self.responseTimeout = responseTimeout
        self.shutdownGrace = shutdownGrace
        self.terminateGrace = terminateGrace
        self.makeInputPipe = makeInputPipe
    }

    func launch(executable: URL, directory: URL) {
        guard process == nil else { return }
        launchGeneration += 1
        let generation = launchGeneration
        closing = false
        buffer.removeAll()
        previewVersion = 0; audioVersion = 0; sharingVersion = 0
        conversationVersion = 0; callRecoveryVersion = 0; callConnectorVersion = 0; guestAdmissionVersion = 0
        let task = Process(), stdinPipe = makeInputPipe(), stdoutPipe = Pipe()
        let writer = stdinPipe.fileHandleForWriting
        task.executableURL = executable
        task.arguments = ["app-session", "--dir", directory.path]
        task.environment = ["HOME": FileManager.default.homeDirectoryForCurrentUser.path,
                            "PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR": NSTemporaryDirectory()]
        task.standardInput = stdinPipe
        task.standardOutput = stdoutPipe
        task.standardError = FileHandle.nullDevice
        stdoutPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let bytes = handle.availableData
            if bytes.isEmpty { handle.readabilityHandler = nil }
            DispatchQueue.main.async {
                guard let self, self.launchGeneration == generation else { return }
                if bytes.isEmpty { self.eof() } else { self.receive(bytes) }
            }
        }
        task.terminationHandler = { [weak self] task in
            DispatchQueue.main.async {
                guard let self, self.launchGeneration == generation else { return }
                let unexpected = !self.closing
                let failure = self.exitFailure(task)
                // Process exit can precede the queued stdout EOF callback.
                // Seal the transport before callbacks can issue another request.
                self.closing = true
                self.closePipes()
                self.process = nil
                if unexpected { self.onFailure?(failure) }
                self.onExit?()
            }
        }
        do {
            guard fcntl(writer.fileDescriptor, F_SETNOSIGPIPE, 1) != -1 else {
                throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
            }
            try task.run()
            process = task
            input = DispatchIO(type: .stream, fileDescriptor: writer.fileDescriptor, queue: .main) { _ in
                try? writer.close()
            }
            output = stdoutPipe.fileHandleForReading
            try? stdinPipe.fileHandleForReading.close()
            try? stdoutPipe.fileHandleForWriting.close()
            setDeadline()
        } catch {
            stdoutPipe.fileHandleForReading.readabilityHandler = nil
            closing = true
            onFailure?(.launch)
            onExit?()
        }
    }

    private func exitFailure(_ task: Process) -> HelperFailure {
        if ready { return .disconnected }
        guard task.terminationReason == .exit else { return .startup }
        // The CLI reserves these app-session startup statuses. Unlike stdout,
        // exit status is available even if termination wins the pipe-read race.
        switch task.terminationStatus {
        case 70: return .runtime
        case 71: return .busy
        case 72: return .settings
        default: return .startup
        }
    }

    func request(_ method: String, config: CameraConfig? = nil, previewGeneration: String? = nil, completion: @escaping (Reply) -> Void) {
        guard ready, !closing, queue.count < 8 else { fail(); return }
        if audioVersion != 1 && (method == "audio_devices" || config?.audioEnabled == true) {
            completion(["error": ["code": "audio_runtime_unsupported"]]); return
        }
        sequence += 1
        let id = "request-\(sequence)"
        var value: Reply = ["version": 1, "id": id, "method": method]
        if let generation = previewGeneration { value["preview_generation"] = generation }
        if let config = config {
            guard let object = try? config.helperObject(audioSupported: audioVersion == 1) else { fail(); return }
            value["config"] = object
        }
        guard let data = try? JSONSerialization.data(withJSONObject: value) else { fail(); return }
        enqueue(data, id: id, oversizedCode: "configuration_failed", completion: completion)
    }

    // Arguments may carry write-only relay credentials. They are encoded only
    // into the owner pipe and never logged or retained after the write.
    func requestSharing(_ method: SharingMethod, arguments: NativeSharingRequest.Arguments?, completion: @escaping (Reply) -> Void) {
        guard ready, !closing, queue.count < 8 else { fail(); return }
        guard sharingVersion >= 1 else { completion(["error": ["code": "sharing_unsupported"]]); return }
        sequence += 1
        let id = "request-\(sequence)"
        guard let data = try? JSONEncoder().encode(NativeSharingRequest(version: 1, id: id, method: method, sharing: arguments)) else { fail(); return }
        enqueue(data, id: id, oversizedCode: "sharing_settings_too_large", completion: completion)
    }

    func requestConversation(_ method: ConversationMethod, roomID: String?, invitation: String?, completion: @escaping (Reply) -> Void) {
        requestConversation(method, roomID: roomID, invitation: invitation, guestDeparture: nil, completion: completion)
    }

    func recoverGuestDeparture(_ guest: NativeGuestDeparture, roomID: String, completion: @escaping (Reply) -> Void) {
        requestConversation(.leave, roomID: roomID, invitation: nil, guestDeparture: guest, completion: completion)
    }

    func requestGuestAdmission(_ invitation: String, token: String, completion: @escaping (Reply) -> Void) {
        requestConversation(.openLink, roomID: nil, invitation: invitation, guestDeparture: nil, guestToken: token, completion: completion)
    }

    func recoverGuestAdmission(_ invitation: String, token: String, completion: @escaping (Reply) -> Void) {
        guard guestAdmissionVersion == 1 else { completion(["error": ["code": "incompatible_client"]]); return }
        requestConversation(.leave, roomID: nil, invitation: invitation, guestDeparture: nil, guestToken: token, completion: completion)
    }

    private func requestConversation(_ method: ConversationMethod, roomID: String?, invitation: String?,
                                     guestDeparture: NativeGuestDeparture?, guestToken: String? = nil,
                                     completion: @escaping (Reply) -> Void) {
        guard ready, !closing, queue.count < 8, conversationVersion == 1 else {
            completion(["error": ["code": "incompatible_client"]])
            return
        }
        guard invitation?.utf8.count ?? 0 <= 8192 else {
            completion(["error": ["code": "invitation_invalid"]])
            return
        }
        sequence += 1
        let id = "request-\(sequence)"
        let wantsProgress = callRecoveryVersion == 1 && (method == .create || method == .createLocal)
        let request = NativeConversationRequest(id: id, method: method, roomID: roomID, invitationURL: invitation,
                                                setupProgress: wantsProgress ? true : nil, guestDeparture: guestDeparture,
                                                guestToken: guestToken)
        guard let data = try? JSONEncoder().encode(request) else {
            completion(["error": ["code": "invitation_invalid"]])
            return
        }
        enqueue(data, id: id, oversizedCode: "invitation_invalid", completion: completion)
    }

    private func enqueue(_ payload: Data, id: String, oversizedCode: String, completion: @escaping (Reply) -> Void) {
        guard payload.count < Self.maxFrameBytes else {
            completion(["error": ["code": oversizedCode]])
            return
        }
        var data = payload
        data.append(10)
        queue.append(Request(id: id, data: data, completion: completion))
        pump()
    }

    private func pump() {
        guard !closing, current == nil, !queue.isEmpty, let input = input else { return }
        let request = queue.removeFirst()
        current = request
        let id = request.id
        setDeadline()
        let data = request.data.withUnsafeBytes { DispatchData(bytes: $0) }
        input.write(offset: 0, data: data, queue: .main) { [weak self] _, _, error in
            guard error != 0, let self, self.current?.id == id else { return }
            self.fail()
        }
    }

    private func setDeadline() {
        deadline?.cancel()
        let work = DispatchWorkItem { [weak self] in self?.fail(.timeout) }
        deadline = work
        DispatchQueue.main.asyncAfter(deadline: .now() + responseTimeout, execute: work)
    }

    private func receive(_ bytes: Data) {
        guard !closing else { return }
        buffer.append(bytes)
        guard buffer.count <= 262144 else { fail(); return }
        while !closing, let end = buffer.firstIndex(of: 10) {
            let line = Data(buffer.prefix(upTo: end))
            buffer.removeSubrange(...end)
            guard let reply = try? JSONSerialization.jsonObject(with: line) as? Reply,
                  reply["version"] as? Int == 1 else { fail(ready ? .disconnected : .incompatible); return }
            if let event = reply["event"] as? String {
                if event == "ready", !ready,
                   (reply["result"] as? Reply)?["protocol"] as? Int == 1 {
                    previewVersion = (reply["result"] as? Reply)?["preview"] as? Int ?? 0
                    audioVersion = (reply["result"] as? Reply)?["audio"] as? Int ?? 0
                    sharingVersion = (reply["result"] as? Reply)?["sharing"] as? Int ?? 0
                    conversationVersion = (reply["result"] as? Reply)?["conversations"] as? Int ?? 0
                    callRecoveryVersion = (reply["result"] as? Reply)?["call_recovery"] as? Int ?? 0
                    callConnectorVersion = (reply["result"] as? Reply)?["call_connector"] as? Int ?? 0
                    guestAdmissionVersion = (reply["result"] as? Reply)?["guest_admission"] as? Int ?? 0
                    deadline?.cancel(); ready = true; onReady?()
                } else if event == "session_error", !ready {
                    // Exit supplies the classification. Old helpers that only
                    // send this event fall back to a generic startup error.
                    continue
                } else if event == "sharing_changed", ready, sharingVersion >= 1, reply["result"] is Reply {
                    onSharingEvent?(reply)
                } else if event == "conversation_setup_progress", callRecoveryVersion == 1,
                          let pending = current, reply["id"] as? String == pending.id {
                    onEvent?(reply)
                } else if event == "capture_stopped" || event == "session_error" {
                    onEvent?(reply)
                } else { fail(ready ? .disconnected : .incompatible); return }
            } else {
                guard let pending = current, reply["id"] as? String == pending.id else { fail(); return }
                deadline?.cancel()
                current = nil
                pending.completion(reply)
                pump()
            }
        }
    }

    private func eof() {
        guard !closing else { return }
        if ready { fail(); return }
        // Give the startup process a chance to report its exit status. Close
        // stdin in case it closed stdout but is still waiting on the owner.
        closeInput()
    }
    private func fail(_ reason: HelperFailure = .disconnected) {
        guard !closing else { return }
        shutdown()
        onFailure?(reason)
    }
    private func closePipes() {
        deadline?.cancel()
        shutdownDeadline?.cancel()
        shutdownDeadline = nil
        output?.readabilityHandler = nil
        closeInput()
        try? output?.close(); output = nil
        queue.removeAll(); current = nil; ready = false
    }
    func shutdown() {
        guard !closing else { return }
        closing = true
        deadline?.cancel()
        closeInput()
        queue.removeAll(); current = nil; ready = false
        // Continue draining stdout until exit. Closing the reader here could
        // SIGPIPE the helper before its deferred capture cleanup finishes.
        if let process {
            scheduleShutdownSignal(SIGTERM, for: process, after: shutdownGrace)
        } else {
            closePipes()
            onExit?()
        }
    }

    private func closeInput() {
        input?.close(flags: .stop)
        input = nil
    }

    private func scheduleShutdownSignal(_ signal: Int32, for task: Process, after delay: TimeInterval) {
        let work = DispatchWorkItem { [weak self, weak task] in
            guard let self, let task, self.process === task, task.isRunning else { return }
            _ = kill(task.processIdentifier, signal)
            if signal == SIGTERM {
                self.scheduleShutdownSignal(SIGKILL, for: task, after: self.terminateGrace)
            }
        }
        shutdownDeadline = work
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
    }
}
