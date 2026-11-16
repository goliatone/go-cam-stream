import SwiftUI

struct CameraView: View {
    @ObservedObject var model: CameraModel
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack(spacing: 14) {
                Image(systemName: model.capture.phase == .streaming ? "video.fill" : "video.slash")
                    .font(.system(size: 30)).foregroundStyle(model.capture.phase == .streaming ? Color.green : Color.accentColor)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 3) {
                    Text("Camstream Camera").font(.title2.bold())
                    Text(model.title).foregroundStyle(.secondary).accessibilityIdentifier("camera-status")
                    Text(model.microphoneSummary).font(.callout).foregroundStyle(model.capture.audioState == "live" ? Color.green : Color.secondary).accessibilityIdentifier("microphone-status")
                }
                Spacer()
                if model.capture.active {
                    Button("Stop Camera", action: model.stop).disabled(!model.canStop).keyboardShortcut("s", modifiers: [.command, .shift])
                } else {
                    Button("Start Camera", action: model.start).buttonStyle(.borderedProminent).disabled(!model.canStart)
                        .keyboardShortcut("s", modifiers: [.command, .shift])
                }
            }
            Picker("View", selection: $model.section) {
                Text("Setup & Settings").tag(0)
                Text("Connection Details").tag(1)
            }.pickerStyle(.segmented)
            if let problem = model.capture.problem {
                Label(problem, systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("camera-error")
            }
            if model.section == 0 {
                HStack(alignment: .top, spacing: 24) {
                    ScrollView { settings }.frame(maxWidth: .infinity)
                    PreviewView(preview: model.preview, streaming: model.capture.phase == .streaming)
                        .frame(width: 400)
                }.frame(maxHeight: .infinity, alignment: .top)
            } else { ScrollView { connection } }
            if let notice = model.notice {
                Text(notice).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
            Divider()
            Text("Microphone audio is optional. No recording or local audio playback. Closing this window keeps the stream running; Stop or Quit ends camera and microphone capture.")
                .font(.footnote).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
        }.padding(24).frame(minWidth: 900, minHeight: 640)
    }

    private var settings: some View {
        VStack(alignment: .leading, spacing: 16) {
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
                }.padding(8)
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
                        Button("Refresh", action: model.refreshDevices)
                            .disabled(!model.ready || model.working || model.capture.active || model.permission != "authorized")
                    }
                    LabeledContent("Camera name") { TextField("Camera name", text: $model.draft.path).labelsHidden().accessibilityLabel("Camera name") }
                    Text("This name is the stream ID. Use letters, numbers, dashes or underscores.")
                        .font(.caption).foregroundStyle(.secondary)
                }.padding(8)
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
                }.padding(8)
            }.disabled(!model.canEditSettings)
            HStack {
                if model.working { ProgressView().controlSize(.small); Text("Working…").font(.callout) }
                else if let validation = model.validation { Text(validation).font(.caption).foregroundStyle(.secondary) }
                else if model.dirty { Text("Save changes before starting.").font(.caption).foregroundStyle(.secondary) }
                Spacer()
                Button("Save Settings", action: model.save).disabled(!model.canSave).keyboardShortcut("s", modifiers: .command)
            }
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
            }.padding(8)
        }
    }
    private var permissionText: String {
        switch model.permission {
        case "authorized": return "Access allowed. Capture starts only when you choose Start Camera."
        case "denied": return "Access is off. Enable Camstream Camera in Privacy & Security → Camera, then return here."
        case "restricted": return "Camera access is restricted on this Mac. Contact your device administrator."
        default: return "Allow this app to use your webcam for a private live stream. Nothing is captured during setup."
        }
    }
    private var connection: some View {
        VStack(alignment: .leading, spacing: 18) {
            if let saved = model.saved {
                Text(saved.path).font(.headline)
                Text(saved.audioEnabled ? "Microphone audio enabled · Opus" : "Video only · Microphone off").font(.callout)
                Text(saved.endpoint).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                    .accessibilityLabel("Camera endpoint: \(saved.endpoint)")
                Button("Copy Endpoint", action: model.copyEndpoint)
                Divider()
                Text("Connect your Camstream server").font(.headline)
                Text("Export Connection Files → Show Files in Finder. In the web app, choose Add Camera → Import Mac files and select both camstream-camera.yaml and camstream-camera.env. Then Test connection → Save Camera → Watch Camera. Keep the camera running throughout.")
                    .fixedSize(horizontal: false, vertical: true)
                if saved.audioEnabled {
                    Text("Reload the updated audio-enabled profile on your server, then unmute the browser to hear microphone audio. Browser mute does not stop transmission; the local preview stays silent.")
                        .font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
                if saved.listen == "127.0.0.1" {
                    Label("This endpoint is local to this Mac. Choose Private network in Settings to connect from another computer.", systemImage: "info.circle")
                        .font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
                HStack {
                    Button("Export Connection Files", action: model.export).disabled(!model.canExport)
                    if !model.exported.isEmpty { Button("Show Files in Finder", action: model.revealExports) }
                }
                Text("Export and Show Files in Finder do not stop capture or change credentials. The .env file contains a password required even on this Mac; the endpoint alone is not enough. Keep the file private.")
                    .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                if model.dirty { Text("Save your settings changes before exporting.").font(.callout).foregroundStyle(.orange) }
            } else {
                Text("Save camera settings first").font(.headline)
                Text("Connection details will appear here after setup. Your camera stays stopped until you start it.")
                Button("Go to Setup") { model.section = 0 }
            }
        }.padding(.vertical, 8)
    }
}
