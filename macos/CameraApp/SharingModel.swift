import AppKit
import Combine

// Native presentation of Camera Sharing. The helper owns invitations, admission,
// expiry and teardown; this model dispatches explicit operator actions and
// renders the projections it receives. It never starts capture, never shows
// helper-supplied text and keeps link material only long enough to copy it.
enum SharingEnd: Equatable { case revoked, expired, cameraStopped, interrupted }

enum SharingPhase: Equatable {
    case idle
    case pending
    case active
    case degraded
    case stopping
    case ended(SharingEnd)
    case failed
}

struct SharingIngressOption: Identifiable, Equatable {
    let mode: String
    let label: String
    var id: String { mode }
}

// Editable one-time setup. Relay credentials are write-only: the helper reports
// only whether one is saved, and these fields are cleared after a successful save.
struct SharingForm: Equatable {
    static let defaultListener = "127.0.0.1:8890"
    var ingressMode = "disabled"
    var publicBaseURL = ""
    var guestListen = SharingForm.defaultListener
    var relayMode = "cloudflare_turn"
    var relayURLs = ""
    var keyID = ""
    var apiToken = ""
    var sharedSecret = ""

    init() {}
    init(_ setup: NativeSharingSetup) {
        ingressMode = setup.ingressMode
        publicBaseURL = setup.saved?.publicBaseURL ?? ""
        guestListen = setup.saved?.guestListen ?? Self.defaultListener
        let relay = setup.saved?.relayMode ?? "disabled"
        // A never-configured installation proposes the managed relay default (D03).
        relayMode = relay == "disabled" && setup.ingressMode == "disabled" ? "cloudflare_turn" : relay
        relayURLs = (setup.saved?.relayURLs ?? []).joined(separator: "\n")
    }

    var settings: NativeSharingSettings {
        var ingress = NativeSharingSettings.Ingress(mode: ingressMode)
        if ingressMode == "external_https" {
            ingress.publicBaseURL = Self.trimmed(publicBaseURL)
            ingress.guestListen = Self.trimmed(guestListen)
        }
        var relay = NativeSharingSettings.Relay(mode: relayMode)
        if relayMode == "coturn" { relay.urls = relayURLList }
        return NativeSharingSettings(ingress: ingress, relay: relay)
    }
    var credentials: NativeSharingRequest.RelayCredentials? {
        switch relayMode {
        case "cloudflare_turn" where !keyID.isEmpty || !apiToken.isEmpty:
            return .init(keyID: Self.trimmed(keyID), apiToken: apiToken)
        case "coturn" where !sharedSecret.isEmpty:
            return .init(sharedSecret: sharedSecret)
        default: return nil
        }
    }
    var hasCredentialInput: Bool { credentials != nil }
    var relayURLList: [String] {
        relayURLs.split(whereSeparator: { $0 == "\n" || $0 == "," || $0 == " " }).map(String.init)
    }
    // Saved-setting comparison ignores write-only input and unused fields.
    var settingsKey: String {
        let value = settings
        return [value.ingress.mode, value.ingress.publicBaseURL ?? "", value.ingress.guestListen ?? "",
                value.relay.mode, (value.relay.urls ?? []).joined(separator: " ")].joined(separator: "\u{1f}")
    }

    func validation(credentialSaved: Bool) -> String? {
        if ingressMode == "external_https" {
            if !Self.validOrigin(publicBaseURL) {
                return "Enter the HTTPS address guests open, such as https://camera.example.com, without a path."
            }
            if !Self.validListener(guestListen) { return "Use a proxy target on this Mac, such as 127.0.0.1:8890." }
        }
        switch relayMode {
        case "cloudflare_turn":
            if keyID.isEmpty && apiToken.isEmpty {
                if !credentialSaved { return "Enter the Cloudflare TURN key ID and API token for the connection relay." }
            } else if Self.trimmed(keyID).range(of: "^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$", options: .regularExpression) == nil {
                return "Enter the Cloudflare key ID: letters, numbers, dashes or underscores."
            } else if !Self.validSecret(apiToken) { return "Enter the Cloudflare API token without spaces." }
        case "coturn":
            let urls = relayURLList
            if urls.isEmpty || urls.count > 8 || !urls.allSatisfy(Self.validTurnURL) {
                return "Enter 1–8 TURN URLs, such as turns:relay.example.com:5349?transport=tcp."
            }
            if sharedSecret.isEmpty {
                if !credentialSaved { return "Enter the coturn shared secret." }
            } else if !Self.validSecret(sharedSecret) { return "Enter the shared secret without spaces." }
        default: break
        }
        return nil
    }

    static func trimmed(_ value: String) -> String { value.trimmingCharacters(in: .whitespacesAndNewlines) }
    // Light checks for immediate guidance; the helper applies the strict
    // browser-compatible origin and listener validation.
    static func validOrigin(_ raw: String) -> Bool {
        let value = trimmed(raw)
        guard !value.contains("?"), !value.contains("#"), let parts = URLComponents(string: value),
              parts.scheme == "https", let host = parts.host, !host.isEmpty,
              parts.user == nil, parts.password == nil, parts.path.isEmpty || parts.path == "/" else { return false }
        return true
    }
    static func validListener(_ raw: String) -> Bool {
        let value = trimmed(raw)
        guard value.range(of: "^(127\\.[0-9]{1,3}\\.[0-9]{1,3}\\.[0-9]{1,3}|\\[::1\\]):[0-9]{1,5}$", options: .regularExpression) != nil,
              let port = Int(value.split(separator: ":").last ?? "") else { return false }
        return (1...65535).contains(port)
    }
    static func validTurnURL(_ value: String) -> Bool {
        value.range(of: "^turns?:[^\\s/?#]+(\\?transport=(tcp|udp))?$", options: .regularExpression) != nil
    }
    static func validSecret(_ value: String) -> Bool {
        !value.isEmpty && value.utf8.count <= 4096 && value.unicodeScalars.allSatisfy { (0x21...0x7e).contains($0.value) }
    }
}

final class SharingModel: ObservableObject {
    static let cameraID = "local-camera"

    @Published private(set) var connected = false
    @Published private(set) var helperLost = false
    @Published private(set) var supported = false
    @Published private(set) var captureLive = false

    @Published private(set) var setup: NativeSharingSetup?
    @Published var form = SharingForm()
    @Published private(set) var savedForm: SharingForm?
    @Published private(set) var setupWorking = false
    @Published private(set) var testingRelay = false
    @Published private(set) var setupProblem: String?
    @Published private(set) var relayProblem: String?
    @Published private(set) var setupNotice: String?

    @Published var duration: SharingDuration = .oneHour
    @Published var viewerLimit = 1
    @Published private(set) var phase: SharingPhase = .idle
    @Published private(set) var share: NativeSharingView?
    @Published private(set) var deadline: Date?
    @Published private(set) var copying = false
    @Published private(set) var problem: String?
    @Published private(set) var notice: String?

    var didChange: (() -> Void)?
    var clock: () -> Date = Date.init
    var locale = Locale.current
    var timeZone = TimeZone.current
    // Link material goes straight to the pasteboard and is not retained here.
    var writeLink: (String) -> Void = { url in
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(url, forType: .string)
    }

    private let helper: SharingHelper?
    private var epoch = 0
    private var pendingRevoke: String?
    private var endingWithCamera = false
    var observesHostPower = true
    private var hostSleeping = false

    init(helper: SharingHelper?) { self.helper = helper }

    // MARK: Helper lifecycle

    func connect() {
        epoch += 1
        connected = helper != nil
        helperLost = false
        supported = (helper?.sharingVersion ?? 0) >= 1
        guard connected, supported, let helper = helper else { changed(); return }
        helper.onSharingEvent = { [weak self] reply in self?.receive(reply) }
        changed()
        loadSetup()
        refreshStatus()
    }

    // Owner loss ends every share; relaunch never restores one.
    func disconnect() {
        guard connected else { return }
        epoch += 1
        connected = false; helperLost = true
        setupWorking = false; testingRelay = false; copying = false; pendingRevoke = nil
        if hasShare || phase == .pending { end(.interrupted) }
        changed()
    }

    func pauseForSleep() {
        hostSleeping = true
        if hasShare || phase == .pending { end(.interrupted) }
        changed()
    }

    func revokeForSleep(completion: @escaping (Bool) -> Void) {
        guard connected, supported, let helper else { completion(true); return }
        // Even a pending create must be revoked. The owner pipe serializes this
        // request after the create, so the reply confirms authorization ended.
        helper.requestSharing(.hostSleep, arguments: nil) { reply in
            completion(reply["error"] == nil)
        }
    }

    func resume() {
        hostSleeping = false
        changed()
    }

    func observe(captureLive live: Bool, captureStopped stopped: Bool) {
        var updated = false
        if captureLive != live { captureLive = live; updated = true }
        if !live, hasShare || phase == .pending {
            // The helper ends the share with capture; Stop Camera or a capture
            // failure may complete before the status event arrives. A link still
            // being created stays pending: the owner pipe answers the create
            // before the stop, and adopting it without capture moves it to stopping.
            if stopped { end(.cameraStopped); updated = true }
            else if phase != .stopping, phase != .pending { phase = .stopping; endingWithCamera = true; updated = true }
        }
        if updated { changed() }
    }

    // A deadline that passes without an event still ends the local view.
    func tick() {
        guard hasShare, let deadline = deadline, clock() >= deadline else { return }
        end(.expired)
        changed()
    }

    // MARK: Routine sharing

    var hasShare: Bool {
        share?.shareID != nil && [SharingPhase.active, .degraded, .stopping].contains(phase)
    }
    var ingressReady: Bool { setup?.ingressState == .ready }
    var canShare: Bool { shareBlockedReason == nil && !hasShare && phase != .pending }
    var canEditOptions: Bool { connected && supported && !hostSleeping && !hasShare && phase != .pending }
    var canCopyLink: Bool { connected && !copying && share?.shareID != nil && (phase == .active || phase == .degraded) }
    var canStopSharing: Bool { connected && share?.shareID != nil && (phase == .active || phase == .degraded) }
    /// Copy Link and Stop Sharing replace the Share button while a link exists.
    var showsLinkControls: Bool { canCopyLink || canStopSharing || phase == .stopping }
    var shareButtonTitle: String {
        if case .ended = phase { return "Share Again" }
        return phase == .failed ? "Share Again" : "Share Camera"
    }
    var shareBlockedReason: String? {
        if helperLost { return "Reconnect the camera service to share again." }
        if !connected { return "Waiting for the camera service." }
        if !supported { return "Sharing isn't available in this version of the camera service. Install a complete, current Camstream Camera to share." }
        if !observesHostPower { return "Restart Camstream Camera to enable sharing. The app couldn't monitor system sleep." }
        if hostSleeping { return "Sharing pauses while the Mac is going to sleep." }
        if !ingressReady { return "Set up the guest page in Sharing settings first." }
        if !captureLive { return "Start the camera to share it. Sharing never starts the camera." }
        return nil
    }

    func shareCamera() {
        guard canShare, let helper = helper else { return }
        problem = nil; notice = nil; share = nil; deadline = nil; pendingRevoke = nil; endingWithCamera = false
        phase = .pending
        changed()
        let epoch = self.epoch
        let options = NativeSharingOptions(durationSeconds: duration, viewerLimit: viewerLimit)
        helper.requestSharing(.create, arguments: .init(cameraID: Self.cameraID, options: options)) { [weak self] reply in
            guard let self = self, self.epoch == epoch, self.phase == .pending else { return }
            // Create returns link material; Copy Link retrieves it again on demand.
            if let created: NativeSharingCreated = Self.result(reply), created.share.shareID != nil {
                self.adopt(created.share)
                if self.phase == .active { self.notice = "Link ready. Choose Copy Link to share it." }
            } else {
                let code = Self.errorCode(reply)
                self.phase = .failed
                self.problem = Self.shareMessage(code)
                if code == "share_already_active" { self.refreshStatus() }
            }
            self.changed()
        }
    }

    func copyLink() {
        guard canCopyLink, let helper = helper, let id = share?.shareID else { return }
        copying = true; problem = nil; notice = nil
        changed()
        let epoch = self.epoch
        helper.requestSharing(.copyLink, arguments: .init(shareID: id)) { [weak self] reply in
            guard let self = self, self.epoch == epoch else { return }
            self.copying = false
            // A share that ended meanwhile must not put a dead link on the pasteboard.
            guard self.share?.shareID == id, self.phase == .active || self.phase == .degraded else { self.changed(); return }
            if let link: NativeSharingLink = Self.result(reply), Self.validLink(link.url, shareID: id) {
                self.writeLink(link.url)
                self.notice = "Link copied. " + self.audienceNotice
            } else {
                switch Self.errorCode(reply) {
                case "expired": self.end(.expired)
                case "revoked", "invalid_invitation": self.end(self.endReason(fallback: .revoked))
                case let code: self.problem = Self.shareMessage(code)
                }
            }
            self.changed()
        }
    }

    // Stop Sharing needs no confirmation; Share Again always creates a new link.
    func stopSharing() {
        guard canStopSharing, let helper = helper, let id = share?.shareID else { return }
        problem = nil; notice = nil
        phase = .stopping; pendingRevoke = id; endingWithCamera = false
        changed()
        let epoch = self.epoch
        helper.requestSharing(.revoke, arguments: .init(shareID: id)) { [weak self] reply in
            guard let self = self, self.epoch == epoch, self.pendingRevoke == id else { return }
            self.pendingRevoke = nil
            if let view: NativeSharingView = Self.result(reply), view.shareID == id,
               view.state == .revoked || view.state == .expired {
                self.share = view
                self.end(view.state == .expired ? .expired : .revoked)
            } else {
                switch Self.errorCode(reply) {
                case "expired": self.end(.expired)
                case "revoked", "invalid_invitation": self.end(.revoked)
                case let code:
                    // Keep Stop Sharing available and read back the authoritative state.
                    if let view = self.share { self.adopt(view) }
                    self.problem = Self.shareMessage(code)
                    self.refreshStatus()
                }
            }
            self.changed()
        }
    }

    func refreshStatus() {
        guard connected, supported, let helper = helper else { return }
        let epoch = self.epoch
        helper.requestSharing(.status, arguments: .init(cameraID: Self.cameraID)) { [weak self] reply in
            guard let self = self, self.epoch == epoch, self.phase != .pending, self.pendingRevoke == nil,
                  let view: NativeSharingView = Self.result(reply) else { return }
            self.apply(view)
            self.changed()
        }
    }

    private func receive(_ reply: [String: Any]) {
        guard connected else { return }
        guard let view: NativeSharingView = Self.result(reply), view.cameraID == Self.cameraID else {
            refreshStatus(); return
        }
        // The create reply establishes a share first; a revoke in flight keeps
        // stale active projections from reviving the old link.
        if phase == .pending { return }
        if let revoking = pendingRevoke, view.shareID == revoking || view.shareID == nil { return }
        apply(view)
        changed()
    }

    private func apply(_ view: NativeSharingView) {
        guard !hostSleeping else { return }
        if view.shareID == nil || view.state == .stopped {
            if hasShare { end(endReason(fallback: .interrupted)) }
            return
        }
        if case .ended = phase, view.shareID == share?.shareID { return }
        adopt(view)
    }

    private func adopt(_ view: NativeSharingView) {
        share = view
        if let value = Self.date(view.expiresAt) { deadline = value }
        switch view.state {
        case .expired: end(.expired); return
        case .revoked: end(.revoked); return
        case .stopped: end(endReason(fallback: .interrupted)); return
        case .starting: phase = .pending; return
        case .degraded, .error: phase = .degraded
        case .available: phase = view.media == .unavailable ? .degraded : .active
        }
        if view.state == .error { problem = Self.shareMessage(view.error?.code) }
        if !captureLive { phase = .stopping; endingWithCamera = true }
    }

    private func end(_ reason: SharingEnd) {
        phase = .ended(reason)
        copying = false; pendingRevoke = nil; endingWithCamera = false; problem = nil
        if reason != .revoked { notice = nil }
    }

    private func endReason(fallback: SharingEnd) -> SharingEnd {
        if let deadline = deadline, clock().addingTimeInterval(1) >= deadline { return .expired }
        if !captureLive || endingWithCamera { return .cameraStopped }
        return fallback
    }

    // MARK: One-time setup

    var ingressOptions: [SharingIngressOption] {
        var options = [SharingIngressOption(mode: "disabled", label: "Off")]
        for capability in setup?.ingressModes ?? [] where capability.mode != "disabled" {
            options.append(SharingIngressOption(mode: capability.mode, label: Self.ingressLabel(capability)))
        }
        if !options.contains(where: { $0.mode == form.ingressMode }) {
            options.append(SharingIngressOption(mode: form.ingressMode, label: "Saved method — unavailable in this build"))
        }
        return options
    }
    var registeredModes: [NativeSharingSetup.IngressCapability] { setup?.ingressModes ?? [] }
    var selectedAudience: NativeSharingAudience? {
        registeredModes.first(where: { $0.mode == form.ingressMode })?.audience
    }
    var credentialSaved: Bool { setup?.saved?.relayCredentialSet == true && setup?.saved?.relayMode == form.relayMode }
    var setupDirty: Bool { form.settingsKey != (savedForm ?? SharingForm()).settingsKey || form.hasCredentialInput }
    var setupValidation: String? { form.validation(credentialSaved: credentialSaved) }
    var setupLockedByShare: Bool { hasShare || phase == .pending }
    var canEditSetup: Bool { connected && supported && !setupWorking && !testingRelay && !setupLockedByShare }
    var canSaveSetup: Bool { canEditSetup && setupDirty && setupValidation == nil }
    var savedRelayMode: String { setup?.saved?.relayMode ?? "disabled" }
    // A relay test holds the owner pipe for several seconds, so it waits until
    // no link exists; Stop Sharing must never queue behind it.
    var canTestRelay: Bool {
        connected && supported && !setupWorking && !testingRelay && !setupDirty && !setupLockedByShare
            && savedRelayMode != "disabled" && setup?.saved?.relayCredentialSet == true
    }

    var setupStatus: String {
        guard connected, supported, let setup = setup else { return connected && supported ? "Checking…" : "Unavailable" }
        if setup.ingressState == .error || setup.relayState == .error { return "Needs attention" }
        guard setup.ingressState == .ready else { return "Setup needed" }
        if savedRelayMode == "disabled" || setup.relayState == .ready { return "Ready" }
        return "Ready · relay not tested"
    }
    var relayStatus: String {
        if testingRelay { return "Testing the relay…" }
        if let problem = relayProblem { return problem }
        if form.relayMode == "disabled" && savedRelayMode == "disabled" {
            return "No relay. Viewers on restrictive networks may not connect."
        }
        if setupDirty { return "Save the settings, then choose Test Connection." }
        if setup?.relayState == .ready { return "Relay ready" }
        if setup?.saved?.relayCredentialSet == true { return "Not tested yet. Choose Test Connection." }
        return "Enter the relay credentials, then save."
    }
    var relayReady: Bool { setup?.relayState == .ready && savedRelayMode != "disabled" && !testingRelay && relayProblem == nil }

    func loadSetup() {
        guard connected, supported, let helper = helper, !setupWorking else { return }
        setupWorking = true
        changed()
        let epoch = self.epoch
        helper.requestSharing(.setup, arguments: nil) { [weak self] reply in
            guard let self = self, self.epoch == epoch else { return }
            self.setupWorking = false
            if let setup: NativeSharingSetup = Self.result(reply) { self.applySetup(setup, resetForm: false) }
            else { self.setupProblem = Self.setupMessage(Self.errorCode(reply)) }
            self.changed()
        }
    }

    func saveSetup() {
        guard canSaveSetup, let helper = helper else { return }
        let arguments = NativeSharingRequest.Arguments(settings: form.settings, relayCredentials: form.credentials)
        setupWorking = true; setupProblem = nil; relayProblem = nil; setupNotice = nil
        changed()
        let epoch = self.epoch
        helper.requestSharing(.configure, arguments: arguments) { [weak self] reply in
            guard let self = self, self.epoch == epoch else { return }
            self.setupWorking = false
            if let setup: NativeSharingSetup = Self.result(reply) {
                // Replacing the form clears write-only credential input.
                self.applySetup(setup, resetForm: true)
                self.setupNotice = setup.saved?.relayMode == "disabled" || setup.relayState == .ready
                    ? "Sharing settings saved."
                    : "Sharing settings saved. Choose Test Connection to check the relay."
            } else {
                self.setupProblem = Self.setupMessage(Self.errorCode(reply))
            }
            self.changed()
        }
    }

    func testRelay() {
        guard canTestRelay, let helper = helper else { return }
        testingRelay = true; relayProblem = nil; setupNotice = nil
        changed()
        let epoch = self.epoch
        helper.requestSharing(.testRelay, arguments: nil) { [weak self] reply in
            guard let self = self, self.epoch == epoch else { return }
            self.testingRelay = false
            if let setup: NativeSharingSetup = Self.result(reply), setup.relayState == .ready {
                self.applySetup(setup, resetForm: false)
            } else {
                self.relayProblem = Self.relayMessage(Self.errorCode(reply))
            }
            self.changed()
        }
    }

    private func applySetup(_ value: NativeSharingSetup, resetForm: Bool) {
        let keepEdits = !resetForm && setupDirty
        setup = value
        let saved = SharingForm(value)
        savedForm = saved
        if !keepEdits { form = saved }
    }

    // MARK: Presentation

    var headline: String {
        if !connected { return phase == .ended(.interrupted) ? "Sharing ended" : "Sharing unavailable" }
        if !supported { return "Sharing unavailable" }
        switch phase {
        case .idle: return "Not sharing"
        case .pending: return "Creating link…"
        case .active, .degraded: return untilLine
        case .stopping: return "Stopping sharing…"
        case .ended(.revoked): return "Sharing stopped"
        case .ended(.expired): return "Link expired"
        case .ended(.cameraStopped): return "Sharing ended with the camera"
        case .ended(.interrupted): return "Sharing ended"
        case .failed: return "Sharing needs attention"
        }
    }
    var detail: String? {
        if !connected, phase == .ended(.interrupted) { return "The camera service stopped, so the link no longer works." }
        guard connected, supported else { return shareBlockedReason }
        switch phase {
        case .idle: return shareBlockedReason
        case .pending: return captureLive ? "Preparing a new link. The camera keeps running." : "Sharing ends when the camera stops."
        case .active: return mediaDetail
        case .degraded:
            // The helper re-checks the guest page every few seconds and the app
            // follows its status, so there is no separate check to offer here.
            if share?.error?.code == "ingress_unavailable" {
                return "Viewers can't reach the guest page. Check your HTTPS address and proxy; Camstream checks again every few seconds. Stop Sharing ends the link."
            }
            return "Video isn't reaching viewers right now. Viewers can reload the page to try again; Stop Sharing ends the link."
        case .stopping: return endingWithCamera ? "Sharing ends when the camera stops." : "Ending every viewer's video."
        case .ended(.revoked): return "The link no longer works. Share Again creates a new link."
        case .ended(.expired):
            return deadline.map { "The link expired at \(timeText($0)). Share Again creates a new link." }
                ?? "The link expired. Share Again creates a new link."
        case .ended(.cameraStopped): return "The link no longer works. Start the camera, then Share Again for a new link."
        case .ended(.interrupted): return "The link no longer works. Share Again creates a new link."
        case .failed: return nil
        }
    }
    var mediaLabel: String? {
        guard phase == .active || phase == .degraded, let share = share else { return nil }
        if phase == .degraded { return "Connection problem" }
        switch share.media {
        case .live: return "Live"
        case .connecting: return "Connecting…"
        case .reconnecting: return "Reconnecting…"
        case .idle: return share.viewers == 0 ? "Waiting for a viewer" : "Viewer connected"
        case .unavailable: return "Connection problem"
        }
    }
    private var mediaDetail: String? {
        switch share?.media {
        case .live: return "Video is reaching a viewer."
        case .connecting: return "A viewer is connecting."
        case .reconnecting: return "A viewer is reconnecting. Their place is kept for 30 seconds."
        default: return (share?.viewers ?? 0) == 0 ? "Waiting for someone to open the link." : nil
        }
    }
    /// Who can open the link. It is separate from the private stream.
    var scopeLabel: String {
        switch share?.audience {
        case .publicInternet?: return "Shared publicly"
        case .privateTailnet?: return "Shared on your tailnet"
        case nil: return "Shared by link"
        }
    }
    /// A short audience badge for the share card.
    var scopeBadge: (title: String, symbol: String)? {
        guard hasShare else { return nil }
        switch share?.audience {
        case .publicInternet?: return ("Public link", "globe")
        case .privateTailnet?: return ("Tailnet link", "lock")
        case nil: return ("Link", "link")
        }
    }
    var untilLine: String {
        guard share != nil else { return "Sharing" }
        return deadline.map { "\(scopeLabel) until \(timeText($0))" } ?? scopeLabel
    }
    /// Current viewer sessions against the limit chosen for this link.
    var viewerLine: String? {
        guard phase == .active || phase == .degraded, let share = share else { return nil }
        let limit = share.viewerLimit == 1 ? "1 viewer" : "\(share.viewerLimit) viewers"
        return share.viewers == 0 ? "No one is watching yet · \(limit) at a time" : "\(share.viewers) of \(limit) watching"
    }
    var audienceNotice: String {
        let who = share?.audience == .privateTailnet ? "Anyone on your tailnet with this link" : "Anyone with this link"
        guard let deadline = deadline else { return "\(who) can watch until it expires." }
        return "\(who) can watch until \(timeText(deadline))."
    }
    var preShareNotice: String {
        // Share uses the saved method, not an unsaved choice in the settings.
        let saved = registeredModes.first(where: { $0.mode == setup?.ingressMode })?.audience
        let who = saved == .privateTailnet ? "Anyone on your tailnet with the link" : "Anyone with the link"
        let viewers = viewerLimit == 1 ? "one viewer" : "\(viewerLimit) viewers"
        return "\(who) can watch for \(Self.durationText(duration)), \(viewers) at a time. Forwarding the link forwards access; Stop Sharing ends it."
    }
    var showsHeaderStatus: Bool { [SharingPhase.pending, .active, .degraded, .stopping].contains(phase) && connected }
    /// The console banner dismisses the last share problem or result.
    func dismissMessage() {
        guard problem != nil || notice != nil else { return }
        problem = nil; notice = nil
        changed()
    }

    func timeText(_ date: Date) -> String {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = timeZone
        let formatter = DateFormatter()
        formatter.locale = locale
        formatter.timeZone = timeZone
        formatter.setLocalizedDateFormatFromTemplate(calendar.isDate(date, inSameDayAs: clock()) ? "jmm" : "MMMdjmm")
        return formatter.string(from: date)
    }

    static func durationText(_ duration: SharingDuration) -> String {
        switch duration {
        case .fifteenMinutes: return "15 minutes"
        case .oneHour: return "1 hour"
        case .eightHours: return "8 hours"
        }
    }

    static func ingressLabel(_ capability: NativeSharingSetup.IngressCapability) -> String {
        let audience = capability.audience == .privateTailnet ? "private tailnet" : "public internet"
        if capability.mode == "external_https" { return "Your HTTPS address (\(audience))" }
        let name = capability.mode.split(separator: "_").map { $0.prefix(1).uppercased() + $0.dropFirst() }.joined(separator: " ")
        return "\(name) (\(audience))"
    }

    // Only allowlisted local copy is shown; helper messages never reach the UI.
    static func shareMessage(_ code: String?) -> String {
        switch code {
        case "camera_unavailable": return "Start the camera, then share again."
        case "ingress_unavailable": return "The HTTPS guest page isn't ready. Check Sharing settings and your proxy, then try again."
        case "relay_unavailable": return "The connection relay isn't ready. Check it in Sharing settings, then try again."
        case "share_already_active": return "This camera already has an active link."
        case "expired": return "This link has expired. Share Again creates a new link."
        case "revoked", "invalid_invitation": return "This link no longer works. Share Again creates a new link."
        case "rate_limited": return "Wait a moment, then try again."
        case "media_unreachable": return "The camera video isn't reaching the sharing service. Try again."
        case "sharing_unsupported": return "Sharing isn't available in this version of the camera service. Install a complete, current Camstream Camera to share."
        case "invalid_request", "permission_denied":
            return "The camera service refused this sharing action. Quit and reopen Camstream Camera if it continues."
        default: return cameraMessage("transport")
        }
    }
    static func setupMessage(_ code: String?) -> String {
        switch code {
        case "sharing_settings_too_large": return "Sharing settings are too large. Shorten the addresses or remove relay URLs, then save again."
        case "invalid_request": return "Check the HTTPS address, guest listener and relay settings, then save again."
        case "share_already_active": return "Stop Sharing before changing sharing settings."
        case "relay_unavailable": return "The relay credentials couldn't be used. Enter them again, then save."
        case "ingress_unavailable": return "Sharing settings couldn't be applied. Check the HTTPS address and guest listener, then save again."
        case "sharing_unsupported": return shareMessage(code)
        default: return cameraMessage("transport")
        }
    }
    static func relayMessage(_ code: String?) -> String {
        guard code == nil || code == "relay_unavailable" else { return shareMessage(code) }
        return "The relay didn't respond. Check the relay credentials and this Mac's network, then choose Test Connection again."
    }

    static func validLink(_ value: String, shareID: String) -> Bool {
        guard let url = URLComponents(string: value), url.scheme == "https", url.host?.isEmpty == false,
              url.path == "/s/" + shareID, url.query == nil, let fragment = url.fragment, !fragment.isEmpty else { return false }
        return true
    }
    static func errorCode(_ reply: [String: Any]) -> String? { (reply["error"] as? [String: Any])?["code"] as? String }
    static func result<T: Decodable>(_ reply: [String: Any]) -> T? {
        guard reply["error"] == nil, let value = reply["result"], JSONSerialization.isValidJSONObject(value),
              let data = try? JSONSerialization.data(withJSONObject: value) else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }
    static func date(_ value: String?) -> Date? {
        guard let value = value else { return nil }
        let formatter = ISO8601DateFormatter()
        if let date = formatter.date(from: value) { return date }
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: value)
    }

    private func changed() { didChange?() }
}
