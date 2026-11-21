import Foundation

// In-memory sharing peer with no subprocess, device or network access.
final class FixtureSharingHelper: SharingHelper {
    var sharingVersion = 1
    var onSharingEvent: (([String: Any]) -> Void)?
    var requests: [(method: SharingMethod, arguments: NativeSharingRequest.Arguments?)] = []
    var replies: [SharingMethod: [String: Any]] = [:]
    var hold: Set<SharingMethod> = []
    var pending: [([String: Any]) -> Void] = []
    var methods: [SharingMethod] { requests.map(\.method) }
    func requestSharing(_ method: SharingMethod, arguments: NativeSharingRequest.Arguments?, completion: @escaping ([String: Any]) -> Void) {
        requests.append((method, arguments))
        if hold.contains(method) { pending.append(completion); return }
        completion(replies[method] ?? ["error": ["code": "transport"]])
    }
    func deliver(_ reply: [String: Any]) { pending.removeFirst()(reply) }
    func emit(_ view: [String: Any]) { onSharingEvent?(["version": 1, "event": "sharing_changed", "result": view]) }
}

// Camera and sharing peer for CameraModel checks. Capture is a fixture state
// machine; Stop ends any share before it is acknowledged, as the helper does.
final class FixtureSharingCamera: CameraHelper, SharingHelper, ConversationHelper {
    var onReady: (() -> Void)?
    var onEvent: (([String: Any]) -> Void)?
    var onFailure: (() -> Void)?
    var onExit: (() -> Void)?
    var previewVersion = 0
    var audioVersion = 0
    var conversationVersion = 1
    var callRecoveryVersion = 1
    var callConnectorVersion = 0
    var incomingInvitations: [String] = []
    var conversationReply: [String: Any] = [:]
    var heldConversationMethods: Set<ConversationMethod> = []
    var pendingConversation: [ConversationMethod: ([String: Any]) -> Void] = [:]
    var idle: Bool { true }
    var methods: [String] = []
    var status: [String: Any] = ["state": "stopped"]
    let sharing = FixtureSharingHelper()
    var sharingVersion: Int { sharing.sharingVersion }
    var onSharingEvent: (([String: Any]) -> Void)? {
        get { sharing.onSharingEvent }
        set { sharing.onSharingEvent = newValue }
    }
    func launch(executable: URL, directory: URL) { onReady?() }
    func request(_ method: String, config: CameraConfig?, previewGeneration: String?, completion: @escaping ([String: Any]) -> Void) {
        methods.append(method)
        switch method {
        case "config": completion(["result": try! CameraConfig().helperObject(audioSupported: false)])
        case "devices": completion(["result": [["index": "0", "name": "Fixture Camera"]]])
        case "start": status = ["state": "starting"]; completion(["result": status])
        case "status": completion(["result": status])
        case "stop":
            status = ["state": "stopped"]
            sharing.emit(["camera_id": "local-camera", "state": "stopped", "media": "idle", "viewers": 0, "viewer_limit": 0])
            completion(["result": status])
        default: completion(["error": ["code": "preview_unavailable"]])
        }
    }
    func requestSharing(_ method: SharingMethod, arguments: NativeSharingRequest.Arguments?, completion: @escaping ([String: Any]) -> Void) {
        methods.append(method.rawValue)
        sharing.requestSharing(method, arguments: arguments, completion: completion)
    }
    func requestConversation(_ method: ConversationMethod, roomID: String?, invitation: String?, completion: @escaping ([String: Any]) -> Void) {
        methods.append(method.rawValue)
        if let invitation { incomingInvitations.append(invitation) }
        if heldConversationMethods.contains(method) {
            pendingConversation[method] = completion
            return
        }
        completion(conversationReply)
    }
    func shutdown() { onExit?() }
}
