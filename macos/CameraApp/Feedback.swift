import AppKit
import SwiftUI

/// One recovery message: what happened, the cause in a sentence and at most
/// two actions. Camera and call problems share it.
struct Feedback: Equatable {
    enum Tone: Equatable { case problem, unavailable }
    enum Action: Equatable {
        case allowCamera, openCameraSettings, refreshCameras, startCamera, quit
        case openSetupGuide, showSettingsFolder, retryConversation, startNewConversation

        var title: String {
            switch self {
            case .allowCamera: return "Allow Camera"
            case .openCameraSettings: return "Open Camera Settings"
            case .refreshCameras: return "Refresh Cameras"
            case .startCamera: return "Try Again"
            case .quit: return "Quit Camstream Camera"
            case .openSetupGuide: return "Open Setup Guide"
            case .showSettingsFolder: return "Show Settings Folder"
            case .retryConversation: return "Try Again"
            case .startNewConversation: return "Start New Conversation"
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
    static let setupGuide = URL(string: "https://github.com/goliatone/go-cam-stream/blob/main/macos/README.md#conversations")!

    /// The stream problem with the actions that can fix it now.
    var cameraFeedback: Feedback? {
        guard capture.phase == .error, let problem = capture.problem else { return nil }
        guard ready else { return Feedback(tone: .problem, title: "Camera helper stopped", message: problem, actions: [.quit]) }
        var actions: [Feedback.Action] = []
        switch permission {
        case "authorized": break
        case "not_determined": actions.append(.allowCamera)
        default: actions.append(.openCameraSettings)
        }
        if canStart { actions.append(.startCamera) }
        if permission == "authorized", canEditSettings { actions.append(.refreshCameras) }
        return Feedback(tone: .problem, title: "Camera needs attention", message: problem, actions: Array(actions.prefix(2)))
    }

    /// Call setup and start problems, distinguishing an unsupported build,
    /// missing or invalid setup and a failure worth retrying.
    var conversationFeedback: Feedback? {
        if ready, !conversationsSupported {
            return Feedback(tone: .unavailable, title: "Calls aren't available in this build",
                            message: "This Camstream Camera helper can't host or join conversations. Use a current Camstream Camera app.",
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
        case nil:
            if notice.opening {
                return Feedback(tone: .problem, title: host ? "Calls couldn't start" : "Couldn't join the conversation",
                                message: notice.text,
                                actions: host ? (canStartConversation ? [.retryConversation] : []) + [.openSetupGuide] : [])
            }
            return Feedback(tone: .problem, title: "The conversation stopped", message: notice.text,
                            actions: host && canStartConversation ? [.startNewConversation] : [])
        }
    }

    func revealSettingsFolder() { NSWorkspace.shared.open(directory) }

    func perform(_ action: Feedback.Action) {
        switch action {
        case .allowCamera: requestPermission()
        case .openCameraSettings: openPrivacy()
        case .refreshCameras: refreshDevices()
        case .startCamera: start()
        case .quit: NSApp.terminate(nil)
        case .openSetupGuide: openSetupGuide()
        case .showSettingsFolder: revealSettingsFolder()
        case .retryConversation, .startNewConversation: startConversation()
        }
    }
}

/// Renders a `Feedback` at the top of the affected section and announces it.
struct FeedbackCard: View {
    let feedback: Feedback
    let perform: (Feedback.Action) -> Void
    var identifier = "feedback"

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: feedback.tone == .problem ? "exclamationmark.triangle.fill" : "info.circle.fill")
                .font(.title2).foregroundStyle(feedback.tone == .problem ? Color.orange : Color.secondary)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 6) {
                Text(feedback.title).font(.headline)
                Text(feedback.message).fixedSize(horizontal: false, vertical: true)
                if !feedback.actions.isEmpty {
                    HStack {
                        ForEach(Array(feedback.actions.enumerated()), id: \.offset) { index, action in
                            if index == 0 {
                                Button(action.title) { perform(action) }.buttonStyle(.borderedProminent)
                            } else {
                                Button(action.title) { perform(action) }
                            }
                        }
                    }.padding(.top, 2)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color.orange.opacity(feedback.tone == .problem ? 0.12 : 0)))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.secondary.opacity(0.3)))
        .accessibilityElement(children: .contain)
        .accessibilityLabel(feedback.title)
        .accessibilityIdentifier(identifier)
        .onAppear { announce() }
        .onChange(of: feedback) { announce() }
    }

    private func announce() {
        AccessibilityNotification.Announcement(feedback.announcement).post()
    }
}
