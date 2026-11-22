import AppKit
import SwiftUI

/// One recovery message: what happened, the cause in a sentence and at most
/// two actions. Camera and call problems share it.
struct Feedback: Equatable {
    enum Tone: Equatable { case problem, unavailable }
    enum Action: Equatable {
        case allowCamera, openCameraSettings, chooseWebcam, startCamera, retryHelper
        case openSetupGuide, showSettingsFolder, retryConversation, startNewConversation
        case checkConversationRoute, testLocalConversation, retryRelay, openInstallGuide
        case reconnectCallConnector, retryConversationClose

        var title: String {
            switch self {
            case .allowCamera: return "Allow Camera"
            case .openCameraSettings: return "Open Camera Settings"
            case .chooseWebcam: return "Choose Webcam…"
            case .startCamera: return "Try Again"
            case .retryHelper: return "Reconnect Camera Service"
            case .openSetupGuide: return "Open Setup Guide"
            case .showSettingsFolder: return "Show Settings Folder"
            case .retryConversation: return "Try Again"
            case .startNewConversation: return "Start New Call"
            case .checkConversationRoute: return "Check Connection"
            case .testLocalConversation: return "Test on This Mac"
            case .retryRelay: return "Test Relay and Retry"
            case .openInstallGuide: return "Open Install Guide"
            case .reconnectCallConnector: return "Reconnect"
            case .retryConversationClose: return "Finish Closing Call"
            }
        }
    }
    let tone: Tone
    let title: String
    let message: String
    let actions: [Action]
    /// What VoiceOver announces when the message appears.
    var announcement: String { title + ". " + message }
}

extension CameraModel {
    static let setupGuide = URL(string: "https://github.com/goliatone/go-cam-stream/blob/main/macos/README.md#calls")!
    static let installGuide = URL(string: "https://github.com/goliatone/go-cam-stream/blob/main/macos/README.md#install-update-and-rollback")!

    /// All tabs share one service failure and recovery action.
    var helperFeedback: Feedback? {
        guard let failure = helperFailure else { return nil }
        let retry: [Feedback.Action] = canRetryHelper ? [.retryHelper] : []
        let title: String
        let message: String
        let actions: [Feedback.Action]
        switch failure {
        case .launch, .runtime, .incompatible:
            return Feedback(tone: .problem, title: "Camstream Camera needs repair",
                            message: "The camera service couldn’t run. Install a complete, current copy of Camstream Camera. Your settings are stored separately.",
                            actions: [.openInstallGuide])
        case .busy:
            title = "Another Camstream camera session is open"
            message = "Quit the other copy of Camstream Camera or stop the tool using these camera settings, then reconnect here."
            actions = retry
        case .settings:
            title = "The camera settings folder couldn’t be opened"
            message = "Check that your macOS account can access the private Camstream Camera settings folder, then reconnect."
            actions = retry + [.showSettingsFolder]
        case .startup:
            title = "The camera service couldn’t start"
            message = "Camstream Camera needs its camera service for streaming, sharing and calls. Reconnect to try again."
            actions = retry + [.openInstallGuide]
        case .timeout:
            title = "The camera service stopped responding"
            message = "Reconnect to use streaming, sharing and calls again."
            actions = retry
        case .disconnected:
            title = "The camera service disconnected"
            message = "Reconnect to use streaming, sharing and calls again. Saved settings are kept."
            actions = retry
        case .sleepInterrupted:
            title = "The camera service stopped during sleep"
            message = "Reconnect to use streaming, sharing and calls again."
            actions = retry
        }
        let waiting = canRetryHelper ? "" : " Waiting for the camera service to finish closing."
        return Feedback(tone: .problem, title: title, message: message + waiting, actions: actions)
    }

    /// The stream problem with the actions that can fix it now.
    var cameraFeedback: Feedback? {
        guard ready, capture.phase == .error, let problem = capture.problem else { return nil }
        var actions: [Feedback.Action] = []
        switch permission {
        case "authorized": break
        case "not_determined": actions.append(.allowCamera)
        default: actions.append(.openCameraSettings)
        }
        if canStartCamera { actions.append(.startCamera) }
        if permission == "authorized", canEditSettings { actions.append(.chooseWebcam) }
        return Feedback(tone: .problem, title: "Camera needs attention", message: problem, actions: Array(actions.prefix(2)))
    }

    /// Why the camera can't stream yet, before anything has failed.
    var cameraAccessFeedback: Feedback? {
        switch permission {
        case "authorized": return nil
        case "denied":
            return Feedback(tone: .unavailable, title: "Camstream needs camera access",
                            message: "Camera access is off. Turn on Camstream Camera in Privacy & Security → Camera to stream your webcam.",
                            actions: [.openCameraSettings])
        case "restricted":
            return Feedback(tone: .unavailable, title: "Camera access is restricted",
                            message: "Camera access is restricted on this Mac. Contact your device administrator.", actions: [])
        default:
            return Feedback(tone: .unavailable, title: "Camstream needs camera access",
                            message: "Allow access to stream your webcam to your server. Nothing is captured until you choose Start Camera.",
                            actions: [.allowCamera])
        }
    }

    /// Call setup and start problems, distinguishing an unsupported build,
    /// missing or invalid setup and a failure worth retrying.
    var conversationFeedback: Feedback? {
        if canRetryConversationClose {
            return Feedback(tone: .problem, title: "Couldn't close the previous call",
                            message: "Finish closing the call to open another invitation or start a new call.",
                            actions: [.retryConversationClose])
        }
        if ready, !conversationsSupported {
            return Feedback(tone: .unavailable, title: "Calls aren't available in this build",
                            message: "This version of the camera service can't host or join calls. Use a current Camstream Camera app.",
                            actions: [])
        }
        guard let notice = conversationNotice, notice.problem else { return nil }
        let host = notice.role != .guest
        switch conversationSetup {
        case .notConfigured?:
            return Feedback(tone: .problem, title: host ? "Calls aren't set up on this Mac" : "Joining isn't set up on this Mac",
                            message: notice.text, actions: [.openSetupGuide, .showSettingsFolder])
        case .invalidConfiguration?:
            return Feedback(tone: .problem, title: "Call settings need attention", message: notice.text,
                            actions: [.openSetupGuide, .showSettingsFolder])
        case .runtimeUnavailable?, .startFailed?:
            return Feedback(tone: .problem, title: "Calls couldn't start", message: notice.text,
                            actions: host ? [.retryConversation, .openSetupGuide] : [.openSetupGuide])
        case .listenerInUse?:
            let port = conversationSetupPort.map { "Call port \($0)" } ?? "The call port"
            return Feedback(tone: .problem, title: "\(port) is already in use", message: notice.text,
                            actions: [.retryConversation, .openSetupGuide])
        case .publicRouteUnreachable?:
            return Feedback(tone: .problem, title: "Guests can't reach this Mac", message: notice.text,
                            actions: supportsCallRecovery ? [.retryConversation, .testLocalConversation] : [.retryConversation, .openSetupGuide])
        case .callAssetsUnavailable?:
            return Feedback(tone: .problem, title: "Call files are missing from this app", message: notice.text,
                            actions: [.openInstallGuide])
        case .relayConfigurationInvalid?:
            return Feedback(tone: .problem, title: "Call relay settings need attention", message: notice.text,
                            actions: [.showSettingsFolder, .openSetupGuide])
        case .relayUnavailable?:
            return Feedback(tone: .problem, title: "The call relay is unavailable", message: notice.text,
                            actions: [.retryRelay, .showSettingsFolder])
        case .connectorUnavailable?:
            return Feedback(tone: .problem, title: "The call connector is unavailable", message: notice.text,
                            actions: [.reconnectCallConnector, .testLocalConversation])
        case .connectorConfigurationInvalid?:
            return Feedback(tone: .problem, title: "Call connector settings need attention", message: notice.text,
                            actions: [.showSettingsFolder, .openSetupGuide])
        case nil:
            if notice.opening {
                return Feedback(tone: .problem, title: host ? "Calls couldn't start" : "Couldn't join the call",
                                message: notice.text,
                                actions: host ? (canStartConversation ? [.retryConversation] : []) + [.openSetupGuide] : [])
            }
            return Feedback(tone: .problem, title: "The call stopped", message: notice.text,
                            actions: host && canStartConversation ? [.startNewConversation] : [])
        }
    }

    var conversationRouteFeedback: Feedback? {
        guard !conversationRouteReachable, conversationPrejoin?.admission.role == .host else { return nil }
        return Feedback(tone: .problem, title: "Guests can't reach this Mac",
                        message: "Check that the tunnel is running and forwards your call address to this Mac.",
                        actions: [.checkConversationRoute, .openSetupGuide])
    }

    var hasConversationDiagnostics: Bool {
        (conversationNotice?.problem == true && conversationSetup != nil) || conversationRouteFeedback != nil
    }

    func copyConversationDiagnostics() {
        guard hasConversationDiagnostics else { return }
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "unknown"
        let reason = conversationRouteFeedback != nil ? ConversationSetup.publicRouteUnreachable : conversationSetup
        var lines = ["Camstream Camera \(version)", "Call setup: \(reason?.rawValue ?? "unknown")"]
        if reason == .listenerInUse, let port = conversationSetupPort { lines.append("Port: \(port)") }
        writeClipboard(lines.joined(separator: "\n"))
        AccessibilityNotification.Announcement("Call diagnostics copied.").post()
    }

    func revealSettingsFolder() { NSWorkspace.shared.open(directory) }

    func perform(_ action: Feedback.Action) {
        switch action {
        case .allowCamera: requestPermission()
        case .openCameraSettings: openPrivacy()
        case .chooseWebcam: showSettings(.camera)
        case .startCamera: startCamera()
        case .retryHelper: retryHelper()
        case .openSetupGuide: openSetupGuide()
        case .showSettingsFolder: revealSettingsFolder()
        case .retryConversation, .retryRelay: retryConversation()
        case .retryConversationClose: retryConversationClose()
        // As the Call card does: the Settings → Calls choice, asking before
        // it stops a stream.
        case .startNewConversation: startCall()
        case .checkConversationRoute: checkConversationRoute()
        case .testLocalConversation: testConversationLocally()
        case .openInstallGuide: openCallInstallGuide()
        case .reconnectCallConnector: reconnectCallConnector()
        }
    }
}

/// Detail that matters only sometimes. Hover shows it as a tooltip, a click
/// opens it in a popover, and VoiceOver reads it as the button's hint.
struct InfoButton: View {
    let text: String
    var label = "More information"
    @State private var showing = false

    var body: some View {
        Button { showing.toggle() } label: {
            Image(systemName: "info.circle").foregroundStyle(.secondary)
        }
        .buttonStyle(.borderless)
        .help(text)
        .accessibilityLabel(label)
        .accessibilityHint(text)
        .popover(isPresented: $showing, arrowEdge: .bottom) {
            Text(text).font(.callout).fixedSize(horizontal: false, vertical: true)
                .frame(width: 280, alignment: .leading).padding(14)
        }
    }
}
