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
    var onFailure: ((HelperFailure) -> Void)?
    var onExit: (() -> Void)?
    var previewVersion = 0
    var audioVersion = 0
    var conversationVersion = 1
    var callRecoveryVersion = 1
    var callConnectorVersion = 0
    var guestAdmissionVersion = 0
    var incomingInvitations: [String] = []
    private(set) var guestAdmissionTokens: [String] = []
    private(set) var recoveredGuestToken: String?
    private(set) var recoveredGuestInvitation: String?
    var conversationReply: [String: Any] = [:]
    var browserCapturePending: Bool? = false
    private var hostedRoom: [String: Any]?
    private var hostEnded = false
    private(set) var guestRecoveryRequests = 0
    var launchFailure: HelperFailure?
    var defersExit = false
    private(set) var shutdowns = 0
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
    func launch(executable: URL, directory: URL) {
        if let launchFailure { onFailure?(launchFailure); onExit?() }
        else { onReady?() }
    }
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
        let respond: ([String: Any]) -> Void = { [self] reply in
            if method == .create || method == .createLocal,
               let result = reply["result"] as? [String: Any], let admission = result["admission"] as? [String: Any] {
                hostedRoom = admission["room"] as? [String: Any]
                hostEnded = false
            }
            if method == .end, reply["error"] == nil { hostEnded = true }
            if method == .status {
                if var room = hostEnded ? hostedRoom : reply["result"] as? [String: Any] {
                    if hostEnded { room["state"] = "ended" }
                    room["browser_capture_pending"] = browserCapturePending
                    completion(["result": room])
                    return
                }
            }
            completion(reply)
        }
        if heldConversationMethods.contains(method) {
            pendingConversation[method] = respond
            return
        }
        respond(conversationReply)
    }
    func recoverGuestDeparture(_ guest: NativeGuestDeparture, roomID: String, completion: @escaping ([String: Any]) -> Void) {
        guestRecoveryRequests += 1
        requestConversation(.leave, roomID: roomID, invitation: nil, completion: completion)
    }
    func requestGuestAdmission(_ invitation: String, token: String, completion: @escaping ([String: Any]) -> Void) {
        guestAdmissionTokens.append(token)
        requestConversation(.openLink, roomID: nil, invitation: invitation, completion: completion)
    }
    func recoverGuestAdmission(_ invitation: String, token: String, completion: @escaping ([String: Any]) -> Void) {
        recoveredGuestToken = token
        recoveredGuestInvitation = invitation
        requestConversation(.leave, roomID: nil, invitation: nil, completion: completion)
    }
    func reserveCallCapture(in directory: URL) -> (() -> Void)? { {} }
    func shutdown() {
        shutdowns += 1
        if !defersExit { onExit?() }
    }
}
