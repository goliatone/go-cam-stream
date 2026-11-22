import Foundation

/// The Settings window's panes, in toolbar order. One-time setup lives
/// here; the main window keeps only what changes while you use the camera.
enum SettingsPane: Int, CaseIterable {
    case camera, network, server, sharing, calls

    var title: String {
        switch self {
        case .camera: return "Camera"
        case .network: return "Network"
        case .server: return "Server"
        case .sharing: return "Sharing"
        case .calls: return "Calls"
        }
    }
    var symbol: String {
        switch self {
        case .camera: return "video"
        case .network: return "network"
        case .server: return "server.rack"
        case .sharing: return "link"
        case .calls: return "phone"
        }
    }
}

/// What the stage draws: one state at a time. States with a fix carry the
/// message and actions; the actions show under the stage.
enum StageState: Equatable {
    case serviceStarting(String)
    case serviceDown(Feedback)
    case needsAccess(Feedback)
    case off(webcam: String, name: String)
    case starting, stopping, live
    case problem(Feedback)
    case inCall

    var feedback: Feedback? {
        switch self {
        case .serviceDown(let feedback), .needsAccess(let feedback), .problem(let feedback): return feedback
        default: return nil
        }
    }
}

/// The toolbar's one status: state, elapsed time and any share link.
struct StatusPill: Equatable {
    enum Tone: Equatable { case neutral, live, attention }
    let text: String
    let tone: Tone
    /// Elapsed time counts from here, for a live stream or a connected call.
    var since: Date?
    /// Further activity, for example "In call" or "2 viewers".
    var extras: [String] = []

    func label(at now: Date) -> String {
        var first = text
        if let since { first += " · " + Self.elapsed(now.timeIntervalSince(since)) }
        return ([first] + extras).joined(separator: " · ")
    }

    /// "4:05", or "1:02:09" past an hour.
    static func elapsed(_ interval: TimeInterval) -> String {
        let seconds = max(0, Int(interval))
        let (hours, minutes, rest) = (seconds / 3600, seconds / 60 % 60, seconds % 60)
        return hours > 0 ? String(format: "%d:%02d:%02d", hours, minutes, rest) : String(format: "%d:%02d", minutes, rest)
    }
}

/// The console's one message slot. Camera and service problems draw on the
/// stage instead, beside their fix.
struct ConsoleBanner: Equatable {
    enum Source: Equatable { case camera, sharing, call }
    let source: Source
    let text: String
    let problem: Bool
    /// The call view holds the full recovery for a call problem.
    var showsCall = false
}

extension CameraModel {
    var stage: StageState {
        guard ready else {
            if let feedback = helperFeedback { return .serviceDown(feedback) }
            return .serviceStarting(helperBlockedReason ?? "Starting the camera service…")
        }
        switch capture.phase {
        case .streaming: return .live
        case .starting: return .starting
        case .stopping: return .stopping
        case .error:
            return .problem(cameraFeedback ?? Feedback(tone: .problem, title: "Camera needs attention",
                                                       message: capture.problem ?? cameraMessage("capture_failed"), actions: []))
        case .stopped:
            if conversationCapture { return .inCall }
            if let feedback = cameraAccessFeedback { return .needsAccess(feedback) }
            let device = config.device
            return .off(webcam: device == "auto" || device.isEmpty ? "Built-in camera" : device, name: config.path)
        }
    }

    var statusPill: StatusPill {
        guard ready else {
            return helperFailure != nil ? StatusPill(text: "Needs attention", tone: .attention) : StatusPill(text: "Starting…", tone: .neutral)
        }
        var extras: [String] = []
        let call = conversationActivity
        var pill: StatusPill
        switch capture.phase {
        case .streaming:
            pill = StatusPill(text: "Live", tone: .live, since: streamingSince)
            if call != nil { extras.append("In call") }
        case .starting: pill = StatusPill(text: "Starting…", tone: .neutral)
        case .stopping: pill = StatusPill(text: "Stopping…", tone: .neutral)
        case .error: pill = StatusPill(text: "Needs attention", tone: .attention)
        case .stopped:
            if let call { pill = Self.callPill(call, since: callConnectedSince) }
            else if permission != "authorized" { pill = StatusPill(text: "Setup needed", tone: .attention) }
            else { pill = StatusPill(text: "Off", tone: .neutral) }
        }
        if let share = sharing.pillText { extras.append(share) }
        pill.extras = extras
        return pill
    }

    private static func callPill(_ call: ConversationActivity, since: Date?) -> StatusPill {
        switch call.phase {
        case .connected: return StatusPill(text: "In call", tone: .live, since: since)
        case .waiting: return StatusPill(text: call.role == .host ? "Waiting for guest" : "Waiting for host", tone: .neutral)
        case .connecting: return StatusPill(text: "Joining call…", tone: .neutral)
        case .reconnecting: return StatusPill(text: "Reconnecting call…", tone: .attention)
        case .prejoin: return StatusPill(text: "Call open", tone: .neutral)
        case .unknown: return StatusPill(text: "In call", tone: .neutral)
        }
    }

    /// Problems first, then the last result. The call screen shows its own
    /// call messages.
    var banner: ConsoleBanner? {
        if let problem = sharing.problem { return ConsoleBanner(source: .sharing, text: problem, problem: true) }
        let callNotice = screen == .camera ? conversationNotice : nil
        if let callNotice, callNotice.problem {
            return ConsoleBanner(source: .call, text: callNotice.text, problem: true, showsCall: true)
        }
        if let notice { return ConsoleBanner(source: .camera, text: notice, problem: false) }
        if let notice = sharing.notice { return ConsoleBanner(source: .sharing, text: notice, problem: false) }
        if let callNotice { return ConsoleBanner(source: .call, text: callNotice.text, problem: false) }
        return nil
    }

    func dismissBanner() {
        switch banner?.source {
        case .camera?: notice = nil
        case .sharing?: sharing.dismissMessage()
        case .call?: dismissConversationNotice()
        case nil: break
        }
    }

    /// The Call card's one state line.
    var callCardLine: String {
        if let call = conversationActivity { return Self.callLine(call) }
        if ready, !conversationsSupported { return "Calls aren't available in this build." }
        if openingConversation { return conversationProgress?.message ?? "Opening the call…" }
        return "No call. Start one, or join from an invitation."
    }
}

extension SharingModel {
    /// The share link's part of the status pill, while a link exists.
    var pillText: String? {
        guard showsHeaderStatus else { return nil }
        guard let share, phase == .active || phase == .degraded else { return "Sharing" }
        return share.viewers == 1 ? "1 viewer" : "\(share.viewers) viewers"
    }
}
