import Foundation

/// The Settings window's panes, in toolbar order. One-time setup lives
/// here; the main window keeps only what changes while you use the camera.
/// The raw value is the pane's tab index.
enum SettingsPane: Int, CaseIterable {
    case general, camera, network, server, sharing, calls

    var title: String {
        switch self {
        case .general: return "General"
        case .camera: return "Camera"
        case .network: return "Network"
        case .server: return "Server"
        case .sharing: return "Sharing"
        case .calls: return "Calls"
        }
    }
    var symbol: String {
        switch self {
        case .general: return "gearshape"
        case .camera: return "video"
        case .network: return "network"
        case .server: return "server.rack"
        case .sharing: return "link"
        case .calls: return "phone"
        }
    }
}

/// What the stage draws: one state at a time. States with a fix carry the
/// message and actions; the actions show under the stage. A call takes the
/// stage from the camera while it opens, runs or ends.
enum StageState: Equatable {
    case serviceStarting(String)
    case serviceDown(Feedback)
    case needsAccess(Feedback)
    case off(webcam: String, name: String)
    case starting, stopping, live
    case problem(Feedback)
    case call(CallStage)

    var feedback: Feedback? {
        switch self {
        case .serviceDown(let feedback), .needsAccess(let feedback), .problem(let feedback): return feedback
        default: return nil
        }
    }
}

/// The call on the stage.
enum CallStage: Equatable {
    /// Starting a call or opening an invitation, with what it waits for.
    case opening(String)
    /// The call page, with its own video and controls.
    case page
    /// Admitted, but the call isn't open here or in the browser yet.
    case ready(host: Bool)
    /// The host controls are open in the browser, with the room's state.
    case browser(String)
    /// Closing, or the camera is still leaving the call.
    case ending
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
/// stage instead, beside their fix; call problems bring their fixes here.
struct ConsoleBanner: Equatable {
    enum Source: Equatable { case camera, sharing, call }
    let source: Source
    let text: String
    let problem: Bool
    /// A problem's name, shown before its cause.
    var title: String?
    /// At most two fixes; the first is the banner's prominent button.
    var actions: [Feedback.Action] = []
    /// A condition that clears by itself, such as an unreachable guest link,
    /// stays until it does.
    var dismissible = true
}

extension CameraModel {
    var stage: StageState {
        guard ready else {
            if let feedback = helperFeedback { return .serviceDown(feedback) }
            return .serviceStarting(helperBlockedReason ?? "Starting the camera service…")
        }
        if let call = callStage { return .call(call) }
        switch capture.phase {
        case .streaming: return .live
        case .starting: return .starting
        case .stopping: return .stopping
        case .error:
            return .problem(cameraFeedback ?? Feedback(tone: .problem, title: "Camera needs attention",
                                                       message: capture.problem ?? cameraMessage("capture_failed"), actions: []))
        case .stopped:
            if let feedback = cameraAccessFeedback { return .needsAccess(feedback) }
            let selection = configurationProblem == nil ? config : saved ?? config
            let device = selection.device
            let webcam: String
            if device == "auto" || device.isEmpty { webcam = "Built-in camera" }
            else if let known = devices.first(where: { $0.selector == device }) { webcam = known.label(in: devices) }
            else { webcam = device.hasPrefix("uid:") ? "Selected webcam" : device }
            return .off(webcam: webcam, name: selection.path)
        }
    }

    /// The welcome guide's step while it shows. It needs the camera service,
    /// and gives way to a call, such as an invitation opened on first run.
    var welcomeShown: WelcomeStep? { ready && callStage == nil ? welcome : nil }

    /// The call on the stage, or nil while the stage shows the camera.
    var callStage: CallStage? {
        if closingConversation { return .ending }
        if openingConversation { return .opening(conversationProgress?.message ?? "Opening the invitation…") }
        guard let prejoin = conversationPrejoin else { return conversationCapture ? .ending : nil }
        if conversationSurface != nil { return .page }
        if hostInBrowser { return .browser(Self.browserStatus(conversationActivity?.phase)) }
        return .ready(host: prejoin.admission.role == .host)
    }

    /// A browser-hosted room's state, from the owner's status.
    static func browserStatus(_ phase: ConversationActivity.Phase?) -> String {
        switch phase {
        case .waiting?: return "Waiting for your guest"
        case .connecting?: return "Your guest is joining"
        case .connected?: return "Your guest is connected"
        case .reconnecting?: return "Reconnecting to your guest"
        default: return "Hosting in your browser"
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

    /// Problems first, then the last result. A call problem brings its fixes,
    /// since there is no separate call view to hold them.
    var banner: ConsoleBanner? {
        if let problem = sharing.problem { return ConsoleBanner(source: .sharing, text: problem, problem: true) }
        if let route = conversationRouteFeedback {
            return ConsoleBanner(source: .call, text: route.message, problem: true, title: route.title,
                                 actions: route.actions, dismissible: false)
        }
        if let callNotice = conversationNotice, callNotice.problem {
            // While a call is open its problems show as they are; they don't end it.
            if callStage == nil, let feedback = conversationFeedback, feedback.tone == .problem {
                return ConsoleBanner(source: .call, text: feedback.message, problem: true, title: feedback.title,
                                     actions: Array(feedback.actions.prefix(2)))
            }
            return ConsoleBanner(source: .call, text: callNotice.text, problem: true)
        }
        // The previous call couldn't close: its fix stays until it works.
        if let feedback = conversationFeedback, feedback.tone == .problem {
            return ConsoleBanner(source: .call, text: feedback.message, problem: true, title: feedback.title,
                                 actions: Array(feedback.actions.prefix(2)), dismissible: false)
        }
        if let notice { return ConsoleBanner(source: .camera, text: notice, problem: false) }
        if let notice = sharing.notice { return ConsoleBanner(source: .sharing, text: notice, problem: false) }
        if let callNotice = conversationNotice { return ConsoleBanner(source: .call, text: callNotice.text, problem: false) }
        return nil
    }

    func dismissBanner() {
        guard let banner, banner.dismissible else { return }
        switch banner.source {
        case .camera: notice = nil
        case .sharing: sharing.dismissMessage()
        case .call: dismissConversationNotice()
        }
    }

    /// The Call card's one state line.
    var callCardLine: String {
        if let call = conversationActivity { return Self.callLine(call) }
        if ready, !conversationsSupported { return "Calls aren't available in this build." }
        if openingConversation { return conversationProgress?.message ?? "Opening the invitation…" }
        if closingConversation { return "Ending the call…" }
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
