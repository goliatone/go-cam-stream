import SwiftUI

/// The main window: the camera console, or the call while one is shown.
/// Its toolbar holds the status pill and the Settings button.
struct CameraView: View {
    @ObservedObject var model: CameraModel
    @ObservedObject var server: ServerMonitor

    var body: some View {
        Group {
            switch model.screen {
            case .camera:
                ConsoleView(model: model, server: server)
            case .call:
                if model.ready {
                    ConversationView(model: model)
                } else {
                    // Calls need the camera service, so the call view shows its
                    // state and fix instead of disabled controls.
                    ServiceStateView(feedback: model.helperFeedback, progress: model.helperBlockedReason ?? "",
                                     perform: model.perform)
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        .padding(20)
        .frame(minWidth: 640, minHeight: 520)
        .toolbar {
            ToolbarItem(placement: .navigation) {
                if model.screen == .call {
                    Button { model.screen = .camera } label: { Label("Camera", systemImage: "chevron.backward") }
                        .help("Back to the camera (⌘1)")
                }
            }
            ToolbarItem(placement: .principal) { StatusPillView(model: model) }
            ToolbarItem(placement: .primaryAction) {
                Button { model.showSettings(nil) } label: { Label("Settings", systemImage: "gearshape") }
                    .help("Settings (⌘,)")
            }
        }
        .onChange(of: model.activity) { old, new in Self.announce(from: old, to: new) }
    }

    private static func announce(from old: ActivitySummary, to new: ActivitySummary) {
        guard let text = announcement(from: old, to: new) else { return }
        AccessibilityNotification.Announcement(text).post()
    }

    /// New or changed status lines, plus a share or call that ended.
    static func announcement(from old: ActivitySummary, to new: ActivitySummary) -> String? {
        var changes = new.lines.filter { !old.lines.contains($0) }
        if old.share != nil, new.share == nil { changes.append("Share link ended") }
        if old.call != nil, new.call == nil { changes.append("Conversation ended") }
        return changes.isEmpty ? nil : changes.joined(separator: ". ")
    }
}

/// One step for connecting a Camstream server. `done` is nil when the app
/// cannot observe the step.
struct ConnectionStep: Equatable {
    let title: String
    let detail: String?
    let done: Bool?
}

extension CameraModel {
    /// The server checklist, export first. Only the export and a running camera
    /// are observable here; the server steps stay unmarked.
    var connectionSteps: [ConnectionStep] {
        guard let saved else { return [] }
        let export: String
        if exportCurrent {
            export = "Exported to the app's private folder."
        } else if !exported.isEmpty {
            export = "Files from an earlier export are in the private folder. Export again if your settings changed since."
        } else {
            export = "Creates camstream-camera.yaml and camstream-camera.env. The .env file holds the stream password; keep it private."
        }
        return [
            ConnectionStep(title: "Export the connection files", detail: export, done: exportCurrent),
            ConnectionStep(title: "On your server, choose Add Camera → Camstream Mac app",
                           detail: "Select both camstream-camera.yaml and camstream-camera.env.", done: nil),
            ConnectionStep(title: "Choose Continue, then Check connection, Save camera and Watch now",
                           detail: saved.audioEnabled
                               ? "Reload the audio-enabled profile on your server, then unmute the browser to hear the microphone. Browser mute doesn't stop transmission."
                               : nil,
                           done: nil),
            ConnectionStep(title: "Keep this camera running while you test and watch",
                           detail: capture.active ? "The camera is running." : "Choose Start Camera in the main window.",
                           done: capture.active),
        ]
    }
}
