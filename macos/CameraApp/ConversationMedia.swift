import AppKit
import WebKit

private final class ConversationBridge: NSObject, WKScriptMessageHandlerWithReply {
    weak var surface: ConversationMediaSurface?

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage,
                               replyHandler: @escaping (Any?, String?) -> Void) {
        guard let surface else { replyHandler(nil, "Conversation closed"); return }
        surface.receive(message, reply: replyHandler)
    }
}

/// Owns one call WebKit view, an in-memory admission, and the exclusive device lease.
final class ConversationMediaSurface: NSObject, WKNavigationDelegate, WKUIDelegate {
    let view: WKWebView
    private weak var camera: CameraModel?
    private let page: URL
    private let room: String
    private let role: ConversationRole
    private let bridge = ConversationBridge()
    private let ended: (String?) -> Void
    private let startMuted: Bool
    private var admission: Data?
    private var loaded = false
    private var ready = false
    private var lease: UUID?
    private var closed = false
    private var stopped = false
    private var stopWaiters: [() -> Void] = []

    func updateInvitationRoute(_ reachable: Bool) {
        guard ready, !closed, role == .host else { return }
        view.evaluateJavaScript("window.dispatchEvent(new CustomEvent('camstream:invitation-route', { detail: \(reachable) }))") { _, _ in }
    }

    init?(prejoin: ConversationPrejoin, camera: CameraModel, startMuted: Bool = false, ended: @escaping (String?) -> Void) {
        let page = prejoin.pageURL
        guard Self.validPage(page, room: prejoin.admission.room.roomID),
              prejoin.admission.room.version == 1,
              let admission = try? JSONEncoder().encode(prejoin.admission) else { return nil }
        self.page = page
        self.room = prejoin.admission.room.roomID
        self.role = prejoin.admission.role
        self.camera = camera
        self.ended = ended
        self.startMuted = startMuted && prejoin.admission.role == .host
        self.admission = admission
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        configuration.mediaTypesRequiringUserActionForPlayback = []
        view = WKWebView(frame: .zero, configuration: configuration)
        super.init()
        bridge.surface = self
        configuration.userContentController.addScriptMessageHandler(bridge, contentWorld: .page, name: "conversation")
        view.uiDelegate = self
        view.navigationDelegate = self
    }

    static func validPage(_ url: URL, room: String) -> Bool {
        let local = url.scheme == "http" && url.host == "127.0.0.1" && url.port != nil
        return (url.scheme == "https" || local) && url.host != nil && url.user == nil && url.password == nil &&
            !room.isEmpty && url.path == "/r/" + room && url.query == nil && url.fragment == nil
    }

    func loadPrejoin() {
        guard !closed, !loaded else { return }
        loaded = true
        view.load(URLRequest(url: page, cachePolicy: .reloadIgnoringLocalCacheData))
    }

    private func trustedFrame(_ frame: WKFrameInfo) -> Bool {
        let origin = frame.securityOrigin
        return Self.trusted(page: page, mainFrame: frame.isMainFrame, url: frame.request.url,
                            scheme: origin.protocol, host: origin.host, port: origin.port)
    }

    /// Bridge messages and capture prompts come only from the main frame of
    /// the bound call page, at its exact origin and path.
    static func trusted(page: URL, mainFrame: Bool, url: URL?, scheme: String, host: String, port: Int) -> Bool {
        let standard = page.scheme == "https" ? 443 : 80
        return mainFrame && matches(url, page: page) && scheme == page.scheme && host == page.host &&
            (port == 0 ? standard : port) == (page.port ?? standard)
    }

    private func matches(_ url: URL?) -> Bool { Self.matches(url, page: page) }

    private static func matches(_ url: URL?, page: URL) -> Bool {
        guard let url else { return false }
        return url.scheme == page.scheme && url.host == page.host && url.port == page.port && url.path == page.path &&
            url.query == nil && url.fragment == nil && url.user == nil && url.password == nil
    }

    fileprivate func receive(_ message: WKScriptMessage, reply: @escaping (Any?, String?) -> Void) {
        guard !closed, message.webView === view, trustedFrame(message.frameInfo) else {
            reply(nil, "Conversation page rejected")
            return
        }
        if let payload = message.body as? [String: Any], payload.count == 1, let state = payload["state"] {
            // Activity reports are accepted only after admission, for this
            // view's room and role, and only while it is the current call.
            guard ready, let camera, let report = ConversationCallState(payload: state, room: room, role: role),
                  camera.reportConversationState(report, from: self) else {
                reply(nil, "Conversation state rejected")
                return
            }
            reply(true, nil)
            return
        }
        if ready, let payload = message.body as? [String: String], let reason = payload["closed"] {
            let problem: String?
            switch reason {
            case "ended": problem = text(guest: "The host ended the call.", host: "The call ended.")
            case "expired": problem = text(guest: "The invitation expired. Ask the host for a new one.",
                                           host: "The call expired. Start a new one to talk again.")
            case "failed": problem = text(guest: "The connection failed. Check your network and reopen the invitation.",
                                          host: "The connection failed. Check your network, then start a new call.")
            default: problem = nil
            }
            reply(true, nil)
            close(problem: problem)
            return
        }
        switch message.body as? String {
        case "ready":
            guard !ready, let admission, var payload = (try? JSONSerialization.jsonObject(with: admission)) as? [String: Any] else {
                reply(nil, "Conversation admission unavailable")
                return
            }
            self.admission = nil
            ready = true
            if startMuted { payload["start_muted"] = true }
            reply(payload, nil)
        case "capture":
            guard ready, let camera else { reply("unavailable", nil); return }
            if lease != nil { reply(true, nil); return }
            // The page asks only after an explicit device action. The model
            // waits for confirmed stream cleanup and never stops a live stream.
            camera.requestConversationCapture(stop: { [weak self] in self?.close() }) { [weak self, weak camera] granted in
                guard let self, !self.closed else {
                    if let granted { camera?.releaseConversationCapture(granted) }
                    reply("unavailable", nil)
                    return
                }
                if let granted {
                    if self.lease == nil { self.lease = granted } else { camera?.releaseConversationCapture(granted) }
                }
                // Older pages treat any value other than true as a refusal.
                reply(self.lease != nil ? true : camera?.conversationCaptureRefusal ?? "unavailable", nil)
            }
        case "copy_invitation":
            // Hosts copy their invitation through the app; the reply never carries it.
            reply(ready && role == .host && camera?.copyConversationInvitation(from: self) == true, nil)
        default:
            reply(nil, "Conversation action rejected")
        }
    }

    func close(problem: String? = nil, completion: (() -> Void)? = nil) {
        if let completion {
            if stopped { completion() } else { stopWaiters.append(completion) }
        }
        guard !closed else { return }
        closed = true
        admission = nil
        view.stopLoading()
        view.configuration.userContentController.removeScriptMessageHandler(forName: "conversation", contentWorld: .page)
        let lease = self.lease
        self.lease = nil
        guard loaded else { finishedStopping(problem); return }
        view.setCameraCaptureState(.none) { [self] in
            view.setMicrophoneCaptureState(.none) { [self] in
                view.navigationDelegate = nil
                view.uiDelegate = nil
                view.loadHTMLString("", baseURL: nil)
                if let lease { camera?.releaseConversationCapture(lease) }
                finishedStopping(problem)
            }
        }
    }

    private func finishedStopping(_ problem: String?) {
        stopped = true
        ended(problem)
        let waiters = stopWaiters
        stopWaiters.removeAll()
        waiters.forEach { $0() }
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        decisionHandler(!closed && !ready && navigationAction.targetFrame?.isMainFrame == true &&
                        matches(navigationAction.request.url) ? .allow : .cancel)
    }

    func webView(_ webView: WKWebView, requestMediaCapturePermissionFor origin: WKSecurityOrigin,
                 initiatedByFrame frame: WKFrameInfo, type: WKMediaCaptureType,
                 decisionHandler: @escaping (WKPermissionDecision) -> Void) {
        decisionHandler(!closed && ready && lease != nil && trustedFrame(frame) ? .prompt : .deny)
    }

    /// Guests recover with the invitation; hosts start a new call.
    private func text(guest: String, host: String) -> String { role == .host ? host : guest }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        close(problem: text(guest: "The call page couldn't load. Check the server and open the invitation again.",
                            host: "The call page couldn't load. Start a new call to try again."))
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        close(problem: text(guest: "The call page was interrupted. Open the invitation again to reconnect.",
                            host: "The call page was interrupted. Start a new call to try again."))
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        close(problem: text(guest: "The call page closed. Open the invitation again to reconnect.",
                            host: "The call page closed unexpectedly. Start a new call to try again."))
    }
}
