import SwiftUI

struct CameraView: View {
    @ObservedObject var model: CameraModel
    /// Below this width, Setup shows the preview above the settings.
    static let twoColumnWidth: CGFloat = 860

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            streamHeader
            Picker("View", selection: $model.section) {
                Text("Setup & Settings").tag(0)
                Text("Connection Details").tag(1)
                Text("Sharing").tag(2)
                Text("Conversations").tag(3)
            }.pickerStyle(.segmented).labelsHidden()
            Group {
                switch model.section {
                case 0: setup
                case 1: scrolling { connection }
                case 2: scrolling { SharingView(sharing: model.sharing) }
                default: ConversationView(model: model)
                }
            }.frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            if model.section == 0 { actionBar }
            if model.section != 3, let notice = model.notice {
                Text(notice).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            if let footer = footer {
                Divider()
                Text(footer).font(.footnote).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(20)
        .frame(minWidth: 640, minHeight: 520)
        .onChange(of: model.activity) { old, new in Self.announce(from: old, to: new) }
    }

    /// Lifecycle guidance appears only when closing the window matters.
    private var footer: String? {
        if model.endConfirmation != nil { return "Closing this window asks before the conversation ends." }
        if model.capture.active || model.sharing.showsHeaderStatus {
            return "Closing this window keeps the camera stream and share running. Stop or Quit ends them."
        }
        return nil
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

    @ViewBuilder private var cameraFeedback: some View {
        if let feedback = model.cameraFeedback {
            FeedbackCard(feedback: feedback, perform: model.perform, identifier: "camera-error")
        }
    }

    private func scrolling<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                cameraFeedback
                content()
            }.frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var setup: some View {
        GeometryReader { proxy in
            if proxy.size.width >= Self.twoColumnWidth {
                HStack(alignment: .top, spacing: 24) {
                    ScrollView { settings.padding(.trailing, 6) }.frame(maxWidth: .infinity)
                    ScrollView {
                        VStack(alignment: .leading, spacing: 12) {
                            cameraFeedback
                            PreviewView(preview: model.preview, streaming: model.capture.phase == .streaming)
                        }
                    }.frame(width: 360)
                }
            } else {
                // Narrow windows lead with the live preview, or with setup while stopped.
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        cameraFeedback
                        if model.capture.phase == .streaming {
                            PreviewView(preview: model.preview, streaming: true)
                            settings
                        } else {
                            settings
                            PreviewView(preview: model.preview, streaming: false)
                        }
                    }.padding(.trailing, 6)
                }
            }
        }
    }

    /// Save and Start stay outside the scroll region with the reason Start is unavailable.
    private var actionBar: some View {
        HStack(spacing: 12) {
            if model.working {
                ProgressView().controlSize(.small)
                Text("Working…").font(.callout)
            } else if !model.capture.active, let reason = model.startBlockedReason {
                Label(reason, systemImage: "info.circle").font(.callout).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true).accessibilityIdentifier("start-reason")
            }
            Spacer(minLength: 8)
            Button("Save Settings", action: model.save).disabled(!model.canSave).help("Save Settings (⌘S)")
                .accessibilityHint(model.saveBlockedReason ?? (model.dirty ? "Saves your changes without starting the camera." : "There are no unsaved changes."))
            startStopButton
        }
        .padding(.top, 10)
        .overlay(alignment: .top) { Divider() }
    }

    @ViewBuilder private var startStopButton: some View {
        if model.capture.active {
            Button("Stop Camera", action: model.stop).disabled(!model.canStop).help("Stop Camera (⇧⌘S)")
        } else {
            Button("Start Camera", action: model.start).buttonStyle(.borderedProminent).disabled(!model.canStart)
                .help(model.startBlockedReason ?? "Start Camera (⇧⌘S)")
                .accessibilityHint(model.startBlockedReason ?? "Starts the camera stream with the saved settings.")
        }
    }

    private var streamHeader: some View {
        let activity = model.activity
        return HStack(spacing: 14) {
            headerIcon.font(.system(size: 26)).frame(width: 34).accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 3) {
                Text(activity.stream).font(.headline).accessibilityIdentifier("camera-status")
                if let share = activity.share {
                    Label(share, systemImage: "link").font(.callout).accessibilityIdentifier("sharing-summary")
                }
                if let call = activity.call {
                    Label(call, systemImage: "phone.fill").font(.callout).accessibilityIdentifier("call-summary")
                }
            }.fixedSize(horizontal: false, vertical: true)
            Spacer()
            // Setup keeps Start beside Save in its action bar.
            if model.section != 0 { startStopButton }
        }
    }

    /// Shape and color both change with state, so it never relies on color alone.
    @ViewBuilder private var headerIcon: some View {
        switch model.capture.phase {
        case .streaming: Image(systemName: "video.fill").foregroundStyle(.green)
        case .error: Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
        case .starting, .stopping: Image(systemName: "video").foregroundStyle(.secondary)
        case .stopped: Image(systemName: "video.slash").foregroundStyle(.secondary)
        }
    }

    private var settings: some View {
        VStack(alignment: .leading, spacing: 16) {
            if let lock = model.settingsLockReason {
                Label(lock, systemImage: "lock.fill").font(.callout).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true).accessibilityIdentifier("settings-locked")
            }
            GroupBox {
                HStack(alignment: .top) {
                    VStack(alignment: .leading, spacing: 5) {
                        Text("1. Allow camera access").font(.headline)
                        Text(permissionText).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    }
                    Spacer()
                    if model.permission == "not_determined" {
                        Button("Allow Camera", action: model.requestPermission).disabled(!model.canEditSettings)
                    } else if model.permission != "authorized" {
                        Button("Open Camera Settings", action: model.openPrivacy)
                    } else { Image(systemName: "checkmark.circle.fill").foregroundStyle(.green).accessibilityLabel("Camera access allowed") }
                }.padding(8).frame(maxWidth: .infinity, alignment: .leading)
            }
            GroupBox {
                VStack(alignment: .leading, spacing: 12) {
                    Text("2. Choose your camera").font(.headline)
                    HStack {
                        Picker("Webcam", selection: $model.draft.device) {
                            Text("Built-in camera (automatic)").tag("auto")
                            if model.draft.device != "auto", !model.devices.contains(where: { $0.name == model.draft.device }) {
                                Text("\(model.draft.device) — unavailable").tag(model.draft.device)
                            }
                            ForEach(model.devices) { device in Text(device.name).tag(device.name) }
                        }
                        Button("Refresh Cameras", action: model.refreshDevices)
                            .disabled(!model.ready || model.working || model.capture.active || model.permission != "authorized")
                    }
                    LabeledContent("Camera name") { TextField("Camera name", text: $model.draft.path).labelsHidden().accessibilityLabel("Camera name") }
                    Text("This name is the stream ID. Use letters, numbers, dashes or underscores.")
                        .font(.caption).foregroundStyle(.secondary)
                }.padding(8).frame(maxWidth: .infinity, alignment: .leading)
            }.disabled(!model.canEditSettings)
            microphoneSettings
            GroupBox {
                VStack(alignment: .leading, spacing: 12) {
                    Text("4. Choose who can connect").font(.headline)
                    Picker("Access", selection: $model.draft.listen) {
                        Text("Only this Mac").tag("127.0.0.1")
                        Text("Private network").tag("0.0.0.0")
                    }.pickerStyle(.radioGroup)
                    Text(model.draft.listen == "127.0.0.1" ? "Only a server on this Mac can connect. Credentials are still required; get both files in Connection Details, even while the camera is live." : "Allows authenticated connections on this Mac’s network interfaces. Get both files in Connection Details. Use a trusted LAN or VPN; do not expose this port to the internet.")
                        .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    if model.draft.listen == "0.0.0.0" {
                        LabeledContent("Mac address") { TextField("IP address or hostname", text: $model.draft.host).labelsHidden().accessibilityLabel("Mac IP address or hostname") }
                    }
                    LabeledContent("RTSP port") { TextField("RTSP port", text: $model.portText).labelsHidden().accessibilityLabel("RTSP port") }
                }.padding(8).frame(maxWidth: .infinity, alignment: .leading)
            }.disabled(!model.canEditSettings)
        }
    }
    private var microphoneSettings: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 12) {
                Text("3. Optional microphone audio").font(.headline)
                Toggle("Enable microphone", isOn: Binding(get: { model.draft.audioEnabled }, set: model.setMicrophoneEnabled))
                    .accessibilityIdentifier("enable-microphone")
                    .disabled(!model.canEditSettings || (!model.audioSupported && !model.draft.audioEnabled))
                if !model.audioSupported {
                    Text(model.ready ? "This helper does not support microphone audio. Use a complete current app to enable it. Video-only use remains available." : "Waiting for the camera helper to make microphone controls available.")
                        .font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                } else if model.draft.audioEnabled {
                    Text(model.microphonePermissionText).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    if model.microphonePermission == "not_determined" {
                        Button("Allow Microphone", action: model.requestMicrophonePermission)
                            .disabled(!model.canEditSettings).accessibilityIdentifier("allow-microphone")
                    } else if model.microphonePermission == "denied" {
                        Button("Open Microphone Settings", action: model.openMicrophonePrivacy).disabled(model.capture.active)
                    }
                    HStack {
                        Picker("Microphone", selection: Binding(get: { MicrophoneID(model.draft.audioDeviceID) }, set: model.selectMicrophone)) {
                            Text("Choose a microphone").tag(MicrophoneID(""))
                            if !model.draft.audioDeviceID.isEmpty, !model.microphones.contains(where: { $0.matches(model.draft.audioDeviceID) }) {
                                Text(model.microphonesLoaded ? "Saved microphone — unavailable" : "Saved microphone — refresh to check").tag(MicrophoneID(model.draft.audioDeviceID))
                            }
                            ForEach(model.microphones, id: \.selectionID) { device in Text(device.label(in: model.microphones)).tag(device.selectionID) }
                        }.accessibilityIdentifier("microphone-selection")
                            .disabled(!model.canEditSettings || model.microphonePermission != "authorized")
                        Button("Refresh Microphones", action: model.refreshMicrophones)
                            .disabled(!model.canEditSettings || model.microphonePermission != "authorized")
                    }
                    if model.microphonesLoaded && model.microphones.isEmpty {
                        Text("No microphones are available. Reconnect a microphone and refresh, or disable microphone audio.")
                            .font(.caption).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
                    }
                    if let problem = model.microphoneDiscoveryError {
                        Text(problem).font(.caption).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
                    }
                }
                Text("The local preview is silent. Browser mute changes playback only; disable microphone audio here while stopped to stop transmitting sound on the next stream.")
                    .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }.padding(8).frame(maxWidth: .infinity, alignment: .leading)
        }
    }
    private var permissionText: String {
        switch model.permission {
        case "authorized": return model.capture.active ? "Access allowed." : "Access allowed. Capture starts only when you choose Start Camera."
        case "denied": return "Access is off. Enable Camstream Camera in Privacy & Security → Camera, then return here."
        case "restricted": return "Camera access is restricted on this Mac. Contact your device administrator."
        default: return "Allow this app to use your webcam for a private live stream. Nothing is captured during setup."
        }
    }
    private var connection: some View {
        VStack(alignment: .leading, spacing: 18) {
            if let saved = model.saved {
                VStack(alignment: .leading, spacing: 4) {
                    Text(saved.path).font(.headline)
                    Text(saved.audioEnabled ? "Microphone audio enabled · Opus" : "Video only · Microphone off").font(.callout)
                }
                VStack(alignment: .leading, spacing: 12) {
                    Text("Connect your Camstream server").font(.headline)
                    ForEach(Array(model.connectionSteps.enumerated()), id: \.offset) { index, step in
                        connectionStep(index + 1, step) {
                            if index == 0 {
                                HStack {
                                    Button("Export Connection Files", action: model.export)
                                        .buttonStyle(.borderedProminent).disabled(!model.canExport)
                                        .accessibilityIdentifier("export-connection-files")
                                    if !model.exported.isEmpty { Button("Show Files in Finder", action: model.revealExports) }
                                }
                                if model.dirty { Text("Save your settings changes before exporting.").font(.callout).foregroundStyle(.orange) }
                            }
                        }
                    }
                    Text("Export and Show Files in Finder don't stop capture or change credentials.")
                        .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
                if saved.listen == "127.0.0.1" {
                    Label("This address works only on this Mac. Choose Private network in Settings to connect from another computer.", systemImage: "info.circle")
                        .font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
                Divider()
                VStack(alignment: .leading, spacing: 6) {
                    Text("RTSP address").font(.headline)
                    Text(saved.endpoint).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                        .accessibilityLabel("RTSP address: \(saved.endpoint)")
                    Button("Copy RTSP Address (needs the exported password)", action: model.copyEndpoint)
                    Text("Players also need the password from the exported .env file, even on this Mac.")
                        .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
            } else {
                Text("Save camera settings first").font(.headline)
                Text("Connection details will appear here after setup. Your camera stays stopped until you start it.")
                Button("Go to Setup") { model.section = 0 }
            }
        }.padding(.vertical, 8)
    }

    private func connectionStep<Actions: View>(_ number: Int, _ step: ConnectionStep,
                                               @ViewBuilder actions: () -> Actions) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: step.done == true ? "checkmark.circle.fill" : "\(number).circle")
                .font(.title3).foregroundStyle(step.done == true ? Color.green : Color.secondary)
                .accessibilityLabel(step.done == true ? "Step \(number), done" : "Step \(number)")
            VStack(alignment: .leading, spacing: 6) {
                Text(step.title).fixedSize(horizontal: false, vertical: true)
                if let detail = step.detail {
                    Text(detail).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
                actions()
            }
        }.accessibilityElement(children: .contain)
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
            ConnectionStep(title: "In the Camstream web app, choose Add Camera → Import Mac files",
                           detail: "Select both camstream-camera.yaml and camstream-camera.env.", done: nil),
            ConnectionStep(title: "Choose Test connection, then Save Camera and Watch Camera",
                           detail: saved.audioEnabled
                               ? "Reload the audio-enabled profile on your server, then unmute the browser to hear the microphone. Browser mute doesn't stop transmission."
                               : nil,
                           done: nil),
            ConnectionStep(title: "Keep this camera running while you test and watch",
                           detail: capture.active ? "The camera is running." : "Choose Start Camera in Setup & Settings.",
                           done: capture.active),
        ]
    }
}
