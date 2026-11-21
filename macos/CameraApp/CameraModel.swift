import AppKit
import Combine
import CryptoKit

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
    // Sharing is a separate model on the same helper pipe. It follows capture
    // and helper state but never starts capture itself.
    let sharing: SharingModel
    private var sharingObserver: AnyCancellable?
    @Published private(set) var conversationCapture = false
    private var closeConversationCapture: (() -> Void)?
    private var conversationLease: UUID?
    private var captureReleaseWaiters: [() -> Void] = []
    private var confirmingRelease = false
    @Published private(set) var conversationPrejoin: ConversationPrejoin?
    @Published private(set) var conversationSurface: ConversationMediaSurface?
    /// The last call outcome: a problem with recovery, or a short notice.
    struct ConversationNotice: Equatable {
        let text: String
        var problem = false
        var role: ConversationRole?
        /// The problem stopped a start or invitation, not a call in progress.
        var opening = false
    }
    @Published private(set) var conversationNotice: ConversationNotice?
    /// Invitation and endpoint text goes straight to the pasteboard and is not retained.
    var writeClipboard: (String) -> Void = { text in
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }
    var conversationMessage: String? { conversationNotice?.text }
    @Published private(set) var openingConversation = false
    @Published private(set) var conversationProgress: ConversationSetupStage?
    @Published private(set) var conversationRouteReachable = true
    @Published private(set) var checkingConversationRoute = false
    @Published private(set) var callConnectorStatus: CallConnectorStatus = .external
    @Published private(set) var reconnectingCallConnector = false
    @Published private(set) var closingConversation = false
    @Published private(set) var hostInBrowser = false
    /// Sanitized cause of the last failed start or open, when the helper reports one.
    @Published private var conversationFault: ConversationFault?
    var conversationSetup: ConversationSetup? { conversationFault?.setup }
    var conversationSetupPort: UInt16? { conversationFault?.port }
    private struct CallReport { let surface: ObjectIdentifier; let state: ConversationCallState }
    @Published private var callReport: CallReport?
    @Published private(set) var browserRoom: ConversationRoomView?
    private var openingBrowserHost = false
    private var nextHostStatus = Date.distantPast
    private var hostStatusPending = false
    private var nextRouteCheck = Date.distantPast
    private var nextConnectorCheck = Date.distantPast
    private var connectorStatusPending = false
    private var browserCaptureLease: UUID?
    private var conversationCloseWaiters: [(Bool) -> Void] = []
    private var pendingConversationURL: String?
    private var openedInvitation: SHA256.Digest?
    private var invitationRevision = 0
    private var sleepAcknowledgement: (() -> Void)?
    private var sleepTimeout: DispatchWorkItem?
    private var windowVisible = false
    @Published var notice: String?
    /// Connection files were exported since the settings were last saved in
    /// this session. Older files may describe earlier settings.
    @Published private(set) var exportCurrent = false
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
    private let openBrowser: (URL) -> Bool
    private var timer: Timer?
    private var quitting = false
    private var exited = false
    var didChange: (() -> Void)?
    var didExit: (() -> Void)?
    // Separate from the diagnostic CLI/proof, stable across app replacements.
    let directory: URL
    init(client: CameraHelper = HelperClient(), permissions: CameraPermissions = SystemCameraPermissions(),
         directory: URL = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/camstream-camera-app"),
         helper: URL = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/camstream-camera"),
         openBrowser: @escaping (URL) -> Bool = { NSWorkspace.shared.open($0) }) {
        self.client = client; self.permissions = permissions; self.directory = directory; self.helper = helper
        self.openBrowser = openBrowser
        sharing = SharingModel(helper: client as? SharingHelper)
        sharingObserver = sharing.objectWillChange.sink { [weak self] _ in self?.objectWillChange.send() }
        sharing.didChange = { [weak self] in self?.didChange?() }
    }
    var canEditSettings: Bool { ready && !working && !requestingPermission && !capture.active && !conversationCapture && !quitting }

    /// Grants the call-device lease only after the helper confirmed that the
    /// stream released capture, including after a stream failure.
    func claimConversationCapture(stop: @escaping () -> Void) -> UUID? {
        guard canEditSettings, capture.releasedForCall else { return nil }
        let lease = UUID()
        conversationLease = lease
        closeConversationCapture = stop
        conversationCapture = true
        changed()
        return lease
    }

    /// The explicit call-device action. Released capture is granted at once.
    /// A Stop in flight is awaited, and an unresolved failure first asks the
    /// helper to confirm cleanup. An active stream is never stopped here.
    func requestConversationCapture(stop: @escaping () -> Void, completion: @escaping (UUID?) -> Void) {
        if let lease = claimConversationCapture(stop: stop) { completion(lease); return }
        guard ready, !quitting, !conversationCapture else { completion(nil); return }
        let retry = { [weak self] in completion(self?.claimConversationCapture(stop: stop)) }
        if capture.phase == .stopping {
            captureReleaseWaiters.append(retry)
        } else if capture.phase == .error, !capture.released {
            confirmCaptureRelease(then: retry)
        } else { completion(nil) }
    }

    /// Why the explicit call-device request was refused: a live stream the
    /// user must stop, or ownership the helper could not confirm.
    var conversationCaptureRefusal: String {
        capture.active ? "busy" : "unavailable"
    }

    private func confirmCaptureRelease(then done: @escaping () -> Void) {
        captureReleaseWaiters.append(done)
        guard !confirmingRelease else { return }
        confirmingRelease = true
        let token = capture.generation
        // Stop is idempotent in the helper: it reaps any owned run, then
        // acknowledges. It never applies while the app considers capture active.
        client.request("stop") { [weak self] reply in
            guard let self else { return }
            self.confirmingRelease = false
            if self.errorCode(reply) == nil, self.capture.generation == token { self.capture.confirmReleased() }
            self.changed()
            self.flushCaptureReleaseWaiters(force: true)
        }
    }

    private func flushCaptureReleaseWaiters(force: Bool = false) {
        guard !captureReleaseWaiters.isEmpty, force || (!capture.active && !confirmingRelease) else { return }
        let waiters = captureReleaseWaiters
        captureReleaseWaiters.removeAll()
        waiters.forEach { $0() }
    }

    func releaseConversationCapture(_ lease: UUID) {
        guard conversationLease == lease else { return }
        conversationLease = nil
        closeConversationCapture = nil
        conversationCapture = false
        changed()
    }

    func openConversationInvitation(_ value: String) {
        guard !quitting, !value.isEmpty, value.utf8.count <= 8192 else { return }
        section = 3
        let digest = SHA256.hash(data: Data(value.utf8))
        guard pendingConversationURL != value, openedInvitation != digest else { return }
        guard conversationPrejoin == nil, !openingConversation, !closingConversation else { return }
        pendingConversationURL = value
        redeemPendingConversation()
    }

    private func clearConversationPrejoin() {
        invitationRevision += 1
        pendingConversationURL = nil
        openedInvitation = nil
        conversationPrejoin = nil
        openingConversation = false
        conversationProgress = nil
        conversationRouteReachable = true
        checkingConversationRoute = false
        nextRouteCheck = .distantPast
        hostInBrowser = false
        nextHostStatus = .distantPast
        hostStatusPending = false
        callReport = nil
        browserRoom = nil
    }

    /// Accepts call activity only from the bound view of the open conversation.
    @discardableResult
    func reportConversationState(_ state: ConversationCallState, from surface: ConversationMediaSurface) -> Bool {
        guard let current = conversationSurface, current === surface, !closingConversation,
              state.role == conversationPrejoin?.admission.role else { return false }
        callReport = CallReport(surface: ObjectIdentifier(surface), state: state)
        changed()
        return true
    }

    /// One projection for the window, menu and accessibility labels.
    var conversationActivity: ConversationActivity? {
        guard let prejoin = conversationPrejoin, !closingConversation else { return nil }
        let role = prejoin.admission.role
        if hostInBrowser {
            return ConversationActivity(role: role, phase: browserRoom.map(Self.browserPhase) ?? .unknown,
                                        camera: nil, microphone: nil, hostedInBrowser: true)
        }
        guard let surface = conversationSurface else {
            return ConversationActivity(role: role, phase: .prejoin, camera: false, microphone: false)
        }
        // Without the lease WebKit denies capture, so a page cannot report
        // inputs the native shell never allowed.
        let leased = conversationCapture
        if let report = callReport, report.surface == ObjectIdentifier(surface) {
            let state = report.state
            return ConversationActivity(role: role, phase: ConversationActivity.Phase(rawValue: state.phase.rawValue) ?? .unknown,
                                        camera: leased && state.camera, microphone: leased && state.microphone,
                                        cameraLabel: leased && state.camera ? state.cameraLabel : nil,
                                        microphoneLabel: leased && state.microphone ? state.microphoneLabel : nil)
        }
        return ConversationActivity(role: role, phase: .unknown, camera: leased ? nil : false, microphone: leased ? nil : false)
    }

    private static func browserPhase(_ room: ConversationRoomView) -> ConversationActivity.Phase {
        guard room.participants.contains(where: { $0.role == .host && $0.joined }) else { return .prejoin }
        let guestJoined = room.participants.contains { $0.role == .guest && $0.joined }
        switch room.state {
        case .waiting: return .waiting
        case .joining: return guestJoined ? .connecting : .waiting
        case .connected: return .connected
        case .reconnecting: return .reconnecting
        case .ended, .expired, .failed: return .unknown
        }
    }

    /// Specific recovery text for a failed start or open. Older helpers send
    /// no setup classification and get neutral text.
    static func conversationProblem(code: String?, setup: ConversationSetup?, role: ConversationRole) -> String {
        switch setup {
        case .notConfigured:
            return role == .host
                ? "Add the call settings described in the setup guide, then try again."
                : "Follow the setup guide to trust the host's server, then open the invitation again."
        case .invalidConfiguration:
            return "Check the call settings against the setup guide, then try again."
        case .runtimeUnavailable:
            return "Try again. If it keeps failing, quit and reopen Camstream Camera."
        case .startFailed:
            return "Try again in a moment."
        case .listenerInUse:
            return "Quit the other Camstream instance or stop the test using this port, then try again."
        case .publicRouteUnreachable:
            return "Check that the tunnel is running and forwards your call address to this Mac, then try again."
        case .callAssetsUnavailable:
            return "Install a complete, current Camstream Camera app, then reopen it."
        case .relayConfigurationInvalid:
            return "Check the conversation relay settings and credentials, then try again."
        case .relayUnavailable:
            return "Check the relay provider and your network, then test again."
        case .connectorUnavailable:
            return "Reconnect the call connector, then try starting the conversation again."
        case .connectorConfigurationInvalid:
            return "Check the dedicated tunnel settings and token in the settings folder."
        case nil:
            break
        }
        if role == .host { return "Try again, or check the call setup guide." }
        switch code {
        case "room_full": return "This conversation already has a guest."
        case "expired", "ended": return "Ask the host for a new invitation."
        case "media_unreachable": return "The conversation server is unavailable. Try again."
        case "incompatible_client": return "This Camstream Camera build can't open this conversation. Use a current app."
        default: return "This invitation couldn't be opened. Check the link and the trusted server settings, or ask the host for a new one."
        }
    }

    private func redeemPendingConversation() {
        guard ready, let url = pendingConversationURL else { return }
        pendingConversationURL = nil
        presentConversation(.openLink, invitation: url)
    }

    /// The helper speaks the conversation contract; older helpers do not.
    var conversationsSupported: Bool { (client as? ConversationHelper)?.conversationVersion == 1 }
    var supportsCallRecovery: Bool { (client as? ConversationHelper)?.callRecoveryVersion == 1 }
    func openSetupGuide() { _ = openBrowser(Self.setupGuide) }
    func openCallInstallGuide() { _ = openBrowser(Self.installGuide) }
    var conversationAvailable: Bool {
        ready && !quitting && sleepAcknowledgement == nil && (client as? ConversationHelper)?.conversationVersion == 1
    }
    /// Only the host holds an invitation to share.
    var canCopyConversation: Bool {
        conversationRouteReachable && conversationPrejoin?.admission.role == .host && conversationPrejoin?.invitationURL != nil
    }
    var canStartConversation: Bool {
        conversationAvailable && !openingConversation && !closingConversation && conversationPrejoin == nil && !quitting
    }

    private enum HostStart { case muted, prejoin, browser }
    private var lastHostStart: (method: ConversationMethod, mode: HostStart) = (.create, .muted)

    func startConversation() { presentConversation(.create, invitation: nil, hostStart: .muted) }
    func prepareConversation() { presentConversation(.create, invitation: nil, hostStart: .prejoin) }
    func startConversationInBrowser() { presentConversation(.create, invitation: nil, hostStart: .browser) }
    func testConversationLocally() {
        guard supportsCallRecovery else { return }
        presentConversation(.createLocal, invitation: nil, hostStart: .muted)
    }
    func retryConversation() {
        presentConversation(lastHostStart.method, invitation: nil, hostStart: lastHostStart.mode)
    }

    private func presentConversation(_ method: ConversationMethod, invitation: String?, hostStart: HostStart = .prejoin) {
        guard conversationAvailable, let helper = client as? ConversationHelper else {
            conversationNotice = ConversationNotice(text: "This Camstream Camera build cannot open conversations.", problem: true)
            return
        }
        guard !openingConversation, !closingConversation, conversationPrejoin == nil else { return }
        let role: ConversationRole = method == .openLink ? .guest : .host
        if role == .host { lastHostStart = (method, hostStart) }
        openingConversation = true
        conversationProgress = role == .host ? .listener : nil
        conversationNotice = nil
        conversationFault = nil
        invitationRevision += 1
        let revision = invitationRevision
        helper.requestConversation(method, roomID: nil, invitation: invitation) { [weak self] reply in
            guard let self else { return }
            let prejoin: ConversationPrejoin? = self.decode(reply["result"])
            guard !self.quitting, self.invitationRevision == revision else {
                if let prejoin { self.releaseAdmission(prejoin) { _ in } }
                return
            }
            self.openingConversation = false
            self.conversationProgress = nil
            guard let prejoin, prejoin.admission.role == role,
                  ConversationMediaSurface.validPage(prejoin.pageURL, room: prejoin.admission.room.roomID) else {
                if let prejoin { self.releaseAdmission(prejoin) { _ in } }
                let fault: ConversationFault? = self.decode(reply["error"])
                self.conversationFault = fault
                self.conversationNotice = ConversationNotice(
                    text: Self.conversationProblem(code: fault?.code ?? self.errorCode(reply), setup: fault?.setup, role: role),
                    problem: true, role: role, opening: true)
                return
            }
            self.openedInvitation = invitation.map { SHA256.hash(data: Data($0.utf8)) }
            self.conversationPrejoin = prejoin
            self.nextRouteCheck = Date().addingTimeInterval(8)
            switch hostStart {
            case .muted: self.showConversation(startMuted: true)
            case .prejoin: self.joinConversationHere()
            case .browser: self.hostConversationInBrowser()
            }
        }
    }

    func joinConversationHere() { showConversation(startMuted: false) }

    private func showConversation(startMuted: Bool) {
        guard let prejoin = conversationPrejoin, conversationSurface == nil, !hostInBrowser else { return }
        let revision = invitationRevision
        conversationSurface = ConversationMediaSurface(prejoin: prejoin, camera: self, startMuted: startMuted) { [weak self] problem in
            guard let self, self.invitationRevision == revision else { return }
            self.closeConversation()
            self.conversationNotice = problem.map { ConversationNotice(text: $0, problem: true, role: prejoin.admission.role) }
                ?? ConversationNotice(text: "Conversation closed.")
        }
    }

    func hostConversationInBrowser() {
        guard let prejoin = conversationPrejoin, prejoin.admission.role == .host,
              !hostInBrowser, conversationSurface == nil,
              var handoff = URLComponents(url: prejoin.pageURL, resolvingAgainstBaseURL: false) else { return }
        handoff.fragment = "host=" + prejoin.admission.token
        guard let browserURL = handoff.url, !openingBrowserHost else { return }
        let revision = invitationRevision
        openingBrowserHost = true
        requestConversationCapture(stop: { [weak self] in self?.closeConversation() }) { [weak self] lease in
            guard let self else { return }
            self.openingBrowserHost = false
            guard self.invitationRevision == revision, self.conversationPrejoin != nil, !self.quitting else {
                if let lease { self.releaseConversationCapture(lease) }
                return
            }
            guard let lease else {
                self.conversationNotice = ConversationNotice(text: "Stop Camera before opening the host controls in your browser.",
                                                             problem: true, role: .host)
                return
            }
            if self.openBrowser(browserURL) {
                self.browserCaptureLease = lease
                self.hostInBrowser = true
            } else {
                self.releaseConversationCapture(lease)
                self.conversationNotice = ConversationNotice(text: "The default browser could not be opened. Join here or try again.",
                                                             problem: true, role: .host)
            }
        }
    }

    func closeConversation() {
        // Problems and notes about the conversation being closed no longer apply.
        if conversationSurface != nil || conversationPrejoin != nil { conversationNotice = nil }
        closeConversation { _ in }
    }

    private func releaseAdmission(_ prejoin: ConversationPrejoin, completion: @escaping (Bool) -> Void) {
        guard ready, let helper = client as? ConversationHelper, helper.conversationVersion == 1 else {
            completion(false)
            return
        }
        helper.requestConversation(prejoin.admission.role == .host ? .end : .leave,
                                   roomID: prejoin.admission.room.roomID, invitation: nil) {
            completion($0["error"] == nil)
        }
    }

    private func closeConversation(completion: @escaping (Bool) -> Void) {
        conversationCloseWaiters.append(completion)
        guard !closingConversation else { return }
        closingConversation = true
        let surface = conversationSurface
        let prejoin = conversationPrejoin
        let browserLease = browserCaptureLease
        browserCaptureLease = nil
        conversationSurface = nil
        clearConversationPrejoin()

        let group = DispatchGroup()
        var released = true
        if let surface {
            group.enter()
            surface.close { group.leave() }
        } else if browserLease == nil { closeConversationCapture?() }
        if let prejoin {
            group.enter()
            releaseAdmission(prejoin) { success in released = success; group.leave() }
        }
        let finish = { [weak self] in
            guard let self else { return }
            if let browserLease { self.releaseConversationCapture(browserLease) }
            self.closingConversation = false
            let waiters = self.conversationCloseWaiters
            self.conversationCloseWaiters.removeAll()
            waiters.forEach { $0(released) }
        }
        if group.wait(timeout: .now()) == .success { finish() }
        else { group.notify(queue: .main, execute: finish) }
    }

    private func refreshBrowserHost() {
        guard hostInBrowser, !hostStatusPending, Date() >= nextHostStatus, ready,
              let room = conversationPrejoin?.admission.room.roomID, let helper = client as? ConversationHelper else { return }
        let revision = invitationRevision
        nextHostStatus = Date().addingTimeInterval(2)
        hostStatusPending = true
        helper.requestConversation(.status, roomID: room, invitation: nil) { [weak self] reply in
            guard let self, self.invitationRevision == revision else { return }
            self.hostStatusPending = false
            let current: ConversationRoomView? = self.decode(reply["result"])
            if let current, current.state != .ended, current.state != .expired, current != self.browserRoom {
                self.browserRoom = current
                self.changed()
            }
            if current?.state == .ended || current?.state == .expired || self.errorCode(reply) == "invitation_invalid" {
                if let lease = self.browserCaptureLease { self.releaseConversationCapture(lease) }
                self.browserCaptureLease = nil
                self.clearConversationPrejoin()
                self.conversationNotice = ConversationNotice(text: current?.state == .expired ? "The conversation expired." : "Conversation closed.")
            }
        }
    }

    func checkConversationRoute() {
        guard supportsCallRecovery, ready, !checkingConversationRoute,
              let prejoin = conversationPrejoin, prejoin.admission.role == .host,
              prejoin.pageURL.scheme == "https", let helper = client as? ConversationHelper else { return }
        let revision = invitationRevision
        checkingConversationRoute = true
        helper.requestConversation(.checkRoute, roomID: prejoin.admission.room.roomID, invitation: nil) { [weak self] reply in
            guard let self, self.invitationRevision == revision else { return }
            self.checkingConversationRoute = false
            self.nextRouteCheck = Date().addingTimeInterval(8)
            let reachable = reply["result"] as? Bool == true
            guard reachable != self.conversationRouteReachable else { return }
            self.conversationRouteReachable = reachable
            self.conversationSurface?.updateInvitationRoute(reachable)
            self.changed()
        }
    }

    private func refreshCallConnector() {
        guard ready, section == 3, client.idle, !connectorStatusPending, !reconnectingCallConnector,
              Date() >= nextConnectorCheck, let helper = client as? ConversationHelper,
              helper.callConnectorVersion == 1 else { return }
        connectorStatusPending = true
        nextConnectorCheck = Date().addingTimeInterval(6)
        helper.requestConversation(.connectorStatus, roomID: nil, invitation: nil) { [weak self] reply in
            guard let self else { return }
            self.connectorStatusPending = false
            guard !self.reconnectingCallConnector, let state = reply["result"] as? String,
                  let status = CallConnectorStatus(rawValue: state), status != self.callConnectorStatus else { return }
            self.callConnectorStatus = status
        }
    }

    func reconnectCallConnector() {
        guard ready, !reconnectingCallConnector, let helper = client as? ConversationHelper,
              helper.callConnectorVersion == 1 else { return }
        reconnectingCallConnector = true
        if conversationPrejoin?.admission.role == .host, conversationPrejoin?.pageURL.scheme == "https" {
            conversationRouteReachable = false
            conversationSurface?.updateInvitationRoute(false)
        }
        helper.requestConversation(.reconnectConnector, roomID: nil, invitation: nil) { [weak self] reply in
            guard let self else { return }
            self.reconnectingCallConnector = false
            self.nextConnectorCheck = Date().addingTimeInterval(6)
            if let state = reply["result"] as? String, let status = CallConnectorStatus(rawValue: state) {
                self.callConnectorStatus = status
                if status == .connected, self.conversationSetup == .connectorUnavailable {
                    self.conversationFault = nil
                    self.conversationNotice = ConversationNotice(text: "Call connector connected. Start the conversation again.")
                }
                self.checkConversationRoute()
            } else {
                let fault: ConversationFault? = self.decode(reply["error"])
                self.callConnectorStatus = fault?.setup == .connectorConfigurationInvalid ? .setupRequired : .unavailable
            }
        }
    }

    func copyConversationInvitation() {
        guard copyInvitation() else { return }
        conversationNotice = ConversationNotice(text: "Invitation copied.")
    }

    /// The trusted host page asks for its invitation to be copied. The page
    /// learns only whether it was copied, never the link.
    func copyConversationInvitation(from surface: ConversationMediaSurface) -> Bool {
        guard conversationSurface === surface, conversationPrejoin?.admission.role == .host else { return false }
        return copyInvitation()
    }

    private func copyInvitation() -> Bool {
        guard canCopyConversation, let url = conversationPrejoin?.invitationURL else { return false }
        writeClipboard(url.absoluteString)
        return true
    }

    var config: CameraConfig {
        var value = draft
        value.port = Int(portText) ?? 0
        if value.listen == "127.0.0.1" { value.host = "localhost" }
        return value
    }
    var dirty: Bool { saved != config }
    /// The next step when Start Camera is unavailable, or nil when it can run.
    /// The helper state that blocks every camera and call action.
    var helperBlockedReason: String? {
        guard !ready else { return nil }
        return exited ? "The camera helper stopped. Quit and reopen Camstream Camera." : "Waiting for the camera helper…"
    }
    /// Why Start Conversation and Open Invitation are unavailable.
    var conversationBlockedReason: String? {
        if canStartConversation || quitting { return nil }
        if let helperBlockedReason { return helperBlockedReason }
        if !conversationsSupported { return "This Camstream Camera helper can't host or join conversations." }
        if openingConversation { return "Wait for the conversation to open." }
        if closingConversation { return "Wait for the previous conversation to close." }
        if conversationPrejoin != nil { return "End the current conversation first." }
        return nil
    }
    var startBlockedReason: String? {
        if canStart || capture.active || quitting { return nil }
        if let helperBlockedReason { return helperBlockedReason }
        if requestingPermission { return "Respond to the permission request first." }
        if working { return "Wait for the current change to finish." }
        switch permission {
        case "authorized": break
        case "denied": return "Turn on camera access in System Settings first."
        case "restricted": return "Camera access is restricted on this Mac."
        default: return "Allow camera access first."
        }
        if conversationCapture { return "End the conversation to use the camera for streaming." }
        if let validation { return validation }
        if saved == nil { return "Save your settings first." }
        if dirty { return "Save your changes first." }
        return nil
    }
    /// Why Save Settings is unavailable while there are unsaved changes.
    var saveBlockedReason: String? {
        guard dirty, !canSave else { return nil }
        if let lock = settingsLockReason { return lock }
        if permission != "authorized" { return startBlockedReason }
        return validation ?? startBlockedReason
    }
    var settingsLockReason: String? {
        if capture.active { return "Settings are locked while the camera is on. Stop Camera to change them." }
        if conversationCapture { return "Settings are locked while a conversation uses the camera. End the conversation to change them." }
        return nil
    }
    /// Stream failures name only causes that still apply. Camera permission
    /// is not suggested once access is allowed.
    func problemText(_ code: String) -> String {
        guard permission == "authorized" else { return cameraMessage(code) }
        switch code {
        case "capture_failed": return "The camera stopped. Close other apps using the webcam, then try again."
        case "device_unavailable": return "No available webcam was found. Reconnect the camera, then refresh the camera list."
        case "configuration_failed": return "Settings could not be saved. Refresh the camera list and try again."
        default: return cameraMessage(code)
        }
    }
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
        case "authorized": return capture.active ? "Microphone access allowed." : "Microphone access allowed. Audio starts only with Start Camera."
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
            self.sharing.connect()
            self.redeemPendingConversation()
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
                self.capture.captureEnded(self.errorCode(reply).map(self.problemText))
            } else if reply["event"] as? String == "conversation_setup_progress" {
                if self.openingConversation, let value = reply["result"] as? String {
                    self.conversationProgress = ConversationSetupStage(rawValue: value) ?? self.conversationProgress
                }
            } else { self.showError(reply) }
            self.changed()
        }
        client.onFailure = { [weak self] in
            guard let self = self, !self.quitting else { return }
            self.sharing.disconnect()
            self.ready = false; self.working = false
            self.closeConversation()
            if self.capture.problem == nil { self.capture.failed(self.problemText("transport")) }
            self.capture.ownershipLost()
            self.abandonCaptureRelease()
            self.changed()
        }
        client.onExit = { [weak self] in
            guard let self = self else { return }
            self.sharing.disconnect()
            self.ready = false; self.exited = true
            self.closeConversation()
            self.capture.ownershipLost()
            self.abandonCaptureRelease()
            self.acknowledgeSleep()
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
                self.saved = value; self.draft = value; self.portText = String(value.port); self.exportCurrent = false
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
            if let code = self.errorCode(reply) {
                // A refused Start ran nothing. Busy can mean this session still
                // owns a run, so it leaves cleanup to be confirmed.
                self.capture.failed(self.problemText(code), released: code != "busy")
            } else { self.capture.started(token) }
            self.changed()
        }
    }
    func stop() {
        guard canStop else { return }
        capture.stop(); changed()
        let token = capture.generation
        client.request("stop") { [weak self] reply in
            guard let self = self, self.capture.generation == token else { return }
            if let code = self.errorCode(reply) { self.capture.failed(self.problemText(code), released: false) }
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
                self.exportCurrent = true
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
        writeClipboard(saved.endpoint)
        notice = "RTSP address copied. The password is only in the exported .env file."
    }
    func quit() -> Bool {
        quitting = true
        syncPreview()
        sharing.disconnect()
        closeConversation()
        timer?.invalidate()
        capture.stop()
        if !exited { client.shutdown() }
        return exited
    }
    func prepareForSleep(acknowledge: @escaping () -> Void) {
        acknowledgeSleep()
        sleepAcknowledgement = acknowledge
        let timeout = DispatchWorkItem { [weak self] in self?.closeHelperForSleep() }
        sleepTimeout = timeout
        // Close the owner pipe if the request stalls. The helper's exit then
        // acknowledges sleep after its owned sharing media has been reaped.
        DispatchQueue.main.asyncAfter(deadline: .now() + 5, execute: timeout)
        let pendingAdmission = openingConversation
        closeConversation { [weak self] released in
            guard let self, self.sleepAcknowledgement != nil else { return }
            guard released, !pendingAdmission else { self.closeHelperForSleep(); return }
            self.sharing.suspend { [weak self] revoked in
                guard let self, self.sleepAcknowledgement != nil else { return }
                if revoked { self.acknowledgeSleep() }
                else { self.closeHelperForSleep() }
            }
        }
    }

    func wakeFromSleep() {
        sharing.resume()
    }

    private func closeHelperForSleep() {
        sharing.disconnect()
        ready = false
        client.shutdown()
    }

    private func acknowledgeSleep() {
        sleepTimeout?.cancel()
        sleepTimeout = nil
        let acknowledge = sleepAcknowledgement
        sleepAcknowledgement = nil
        acknowledge?()
    }

    func tick() {
        guard !quitting else { return }
        sharing.tick()
        refreshBrowserHost()
        let phase = conversationActivity?.phase
        if client.idle, Date() >= nextRouteCheck, phase == .waiting || phase == .prejoin {
            checkConversationRoute()
        }
        refreshCallConnector()
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
                let message = self.problemText(self.errorCode(reply) ?? "transport")
                self.stopForFailure(message)
            } else if let result = reply["result"] as? [String: Any], let state = result["state"] as? String {
                let audio: MicrophoneStatus? = self.decode(result["audio"])
                if (audio?.enabled == true && !self.capture.audioEnabled) || (self.capture.audioEnabled && state == "streaming" && audio?.live != true) {
                    self.stopForFailure(cameraMessage("audio_observer_failed"))
                } else { self.capture.status(state, frames: result["frames"] as? Int ?? 0, audio: audio) }
            } else { self.stopForFailure(self.problemText("transport")) }
            self.changed()
        }
    }
    private func stopForFailure(_ message: String) {
        capture.stop(); changed()
        let token = capture.generation
        client.request("stop") { [weak self] reply in
            guard let self = self, self.capture.generation == token else { return }
            // Only a matched Stop acknowledgement confirms cleanup.
            self.capture.failed(message, released: self.errorCode(reply) == nil); self.changed()
        }
    }
    private func abandonCaptureRelease() {
        confirmingRelease = false
        flushCaptureReleaseWaiters(force: true)
    }
    private func errorCode(_ reply: [String: Any]) -> String? { (reply["error"] as? [String: Any])?["code"] as? String }
    private func showError(_ reply: [String: Any]) { capture.failed(problemText(errorCode(reply) ?? "transport")) }
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
    private func changed() {
        syncPreview()
        sharing.observe(captureLive: capture.phase == .streaming, captureStopped: capture.phase == .stopped || capture.phase == .error)
        flushCaptureReleaseWaiters()
        didChange?()
    }
}
