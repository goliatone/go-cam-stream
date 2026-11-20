import Foundation

// One in-flight request, correlated replies, and a bounded queue. No secrets or
// raw helper output are logged. Closing stdin is the authoritative owner boundary.
protocol CameraHelper: AnyObject {
    var onReady: (() -> Void)? { get set }
    var onEvent: (([String: Any]) -> Void)? { get set }
    var onFailure: (() -> Void)? { get set }
    var onExit: (() -> Void)? { get set }
    var previewVersion: Int { get }
    var audioVersion: Int { get }
    var idle: Bool { get }
    func launch(executable: URL, directory: URL)
    func request(_ method: String, config: CameraConfig?, previewGeneration: String?, completion: @escaping ([String: Any]) -> Void)
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
}

final class HelperClient: CameraHelper, SharingHelper, ConversationHelper {
    typealias Reply = [String: Any]
    private struct Request {
        let id: String
        let data: Data
        let completion: (Reply) -> Void
    }
    var onReady: (() -> Void)?
    var onEvent: ((Reply) -> Void)?
    var onFailure: (() -> Void)?
    var onExit: (() -> Void)?
    var onSharingEvent: ((Reply) -> Void)?
    private var process: Process?
    private var input: FileHandle?
    private var output: FileHandle?
    private var buffer = Data()
    private var queue: [Request] = []
    private var current: Request?
    private var deadline: Timer?
    private var sequence = 0
    private var closing = false
    private var ready = false
    private(set) var previewVersion = 0
    private(set) var audioVersion = 0
    private(set) var sharingVersion = 0
    private(set) var conversationVersion = 0
    private(set) var callRecoveryVersion = 0
    var idle: Bool { ready && current == nil && queue.isEmpty && !closing }

    func launch(executable: URL, directory: URL) {
        let task = Process(), stdinPipe = Pipe(), stdoutPipe = Pipe()
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
                if bytes.isEmpty { self?.eof() } else { self?.receive(bytes) }
            }
        }
        task.terminationHandler = { [weak self] _ in
            DispatchQueue.main.async {
                guard let self = self else { return }
                let unexpected = !self.closing
                // Process exit can precede the queued stdout EOF callback.
                // Seal the transport before callbacks can issue another request.
                self.closing = true
                self.closePipes()
                self.process = nil
                if unexpected { self.onFailure?() }
                self.onExit?()
            }
        }
        do {
            try task.run()
            process = task
            input = stdinPipe.fileHandleForWriting
            output = stdoutPipe.fileHandleForReading
            try? stdinPipe.fileHandleForReading.close()
            try? stdoutPipe.fileHandleForWriting.close()
            deadline = Timer.scheduledTimer(withTimeInterval: 30, repeats: false) { [weak self] _ in self?.fail() }
        } catch {
            stdoutPipe.fileHandleForReading.readabilityHandler = nil
            onFailure?()
            onExit?()
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
        guard var data = try? JSONSerialization.data(withJSONObject: value) else { fail(); return }
        data.append(10)
        queue.append(Request(id: id, data: data, completion: completion))
        pump()
    }

    // Arguments may carry write-only relay credentials. They are encoded only
    // into the owner pipe and never logged or retained after the write.
    func requestSharing(_ method: SharingMethod, arguments: NativeSharingRequest.Arguments?, completion: @escaping (Reply) -> Void) {
        guard ready, !closing, queue.count < 8 else { fail(); return }
        guard sharingVersion >= 1 else { completion(["error": ["code": "sharing_unsupported"]]); return }
        sequence += 1
        let id = "request-\(sequence)"
        guard var data = try? JSONEncoder().encode(NativeSharingRequest(version: 1, id: id, method: method, sharing: arguments)) else { fail(); return }
        data.append(10)
        queue.append(Request(id: id, data: data, completion: completion))
        pump()
    }

    func requestConversation(_ method: ConversationMethod, roomID: String?, invitation: String?, completion: @escaping (Reply) -> Void) {
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
                                                setupProgress: wantsProgress ? true : nil)
        guard var data = try? JSONEncoder().encode(request), data.count < 16384 else {
            completion(["error": ["code": "invitation_invalid"]])
            return
        }
        data.append(10)
        queue.append(Request(id: id, data: data, completion: completion))
        pump()
    }

    private func pump() {
        guard !closing, current == nil, !queue.isEmpty, let input = input else { return }
        current = queue.removeFirst()
        do {
            try input.write(contentsOf: current!.data)
            deadline?.invalidate()
            deadline = Timer.scheduledTimer(withTimeInterval: 30, repeats: false) { [weak self] _ in self?.fail() }
        } catch { fail() }
    }

    private func receive(_ bytes: Data) {
        guard !closing else { return }
        buffer.append(bytes)
        guard buffer.count <= 262144 else { fail(); return }
        while let end = buffer.firstIndex(of: 10) {
            let line = Data(buffer.prefix(upTo: end))
            buffer.removeSubrange(...end)
            guard let reply = try? JSONSerialization.jsonObject(with: line) as? Reply,
                  reply["version"] as? Int == 1 else { fail(); return }
            if let event = reply["event"] as? String {
                if event == "ready", !ready,
                   (reply["result"] as? Reply)?["protocol"] as? Int == 1 {
                    previewVersion = (reply["result"] as? Reply)?["preview"] as? Int ?? 0
                    audioVersion = (reply["result"] as? Reply)?["audio"] as? Int ?? 0
                    sharingVersion = (reply["result"] as? Reply)?["sharing"] as? Int ?? 0
                    conversationVersion = (reply["result"] as? Reply)?["conversations"] as? Int ?? 0
                    callRecoveryVersion = (reply["result"] as? Reply)?["call_recovery"] as? Int ?? 0
                    deadline?.invalidate(); ready = true; onReady?()
                } else if event == "sharing_changed", ready, sharingVersion >= 1, reply["result"] is Reply {
                    onSharingEvent?(reply)
                } else if event == "conversation_setup_progress", callRecoveryVersion == 1,
                          let pending = current, reply["id"] as? String == pending.id {
                    onEvent?(reply)
                } else if event == "capture_stopped" || event == "session_error" {
                    onEvent?(reply)
                } else { fail(); return }
            } else {
                guard let pending = current, reply["id"] as? String == pending.id else { fail(); return }
                deadline?.invalidate()
                current = nil
                pending.completion(reply)
                pump()
            }
        }
    }

    private func eof() { if !closing { fail() } }
    private func fail() {
        guard !closing else { return }
        shutdown()
        onFailure?()
    }
    private func closePipes() {
        deadline?.invalidate()
        output?.readabilityHandler = nil
        try? input?.close(); try? output?.close()
        input = nil; output = nil
        queue.removeAll(); current = nil; ready = false
    }
    func shutdown() {
        closing = true
        deadline?.invalidate()
        try? input?.close(); input = nil
        queue.removeAll(); current = nil; ready = false
        // Continue draining stdout until exit. Closing the reader here could
        // SIGPIPE the helper before its deferred capture cleanup finishes.
        if process == nil { closePipes(); onExit?() }
    }
}
