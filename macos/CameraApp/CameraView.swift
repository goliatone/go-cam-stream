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
            ScrollView {
                if model.section == 0 { settings } else { connection }
            }
            if let notice = model.notice {
                Text(notice).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
            Divider()
            Text("Video only · No microphone or recording. Closing this window keeps the camera controls in the menu bar. Quit stops the camera.")
                .font(.footnote).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
        }.padding(24).frame(width: 600, height: 720)
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
                        Button("Allow Camera", action: model.requestPermission)
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
            }.disabled(model.capture.active || model.working)
            GroupBox {
                VStack(alignment: .leading, spacing: 12) {
                    Text("3. Choose who can connect").font(.headline)
                    Picker("Access", selection: $model.draft.listen) {
                        Text("Only this Mac").tag("127.0.0.1")
                        Text("Private network").tag("0.0.0.0")
                    }.pickerStyle(.radioGroup)
                    Text(model.draft.listen == "127.0.0.1" ? "Local testing only. A Camstream server on another computer cannot connect." : "Allows authenticated connections on this Mac’s network interfaces. Use a trusted LAN or VPN; do not expose this port to the internet.")
                        .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    if model.draft.listen == "0.0.0.0" {
                        LabeledContent("Mac address") { TextField("IP address or hostname", text: $model.draft.host).labelsHidden().accessibilityLabel("Mac IP address or hostname") }
                    }
                    LabeledContent("RTSP port") { TextField("RTSP port", text: $model.portText).labelsHidden().accessibilityLabel("RTSP port") }
                }.padding(8)
            }.disabled(model.capture.active || model.working)
            HStack {
                if model.working { ProgressView().controlSize(.small); Text("Working…").font(.callout) }
                else if let validation = model.validation { Text(validation).font(.caption).foregroundStyle(.secondary) }
                else if model.dirty { Text("Save changes before starting.").font(.caption).foregroundStyle(.secondary) }
                Spacer()
                Button("Save Settings", action: model.save).disabled(!model.canSave).keyboardShortcut("s", modifiers: .command)
            }
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
                Text(saved.endpoint).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                    .accessibilityLabel("Camera endpoint: \(saved.endpoint)")
                Button("Copy Endpoint", action: model.copyEndpoint)
                Divider()
                Text("Connect your Camstream server").font(.headline)
                Text("Export the camera configuration and private credentials file. Merge the camera and access grant into your server configuration, set the exported environment values, then restart the server.")
                    .fixedSize(horizontal: false, vertical: true)
                if saved.listen == "127.0.0.1" {
                    Label("This endpoint is local to this Mac. Choose Private network in Settings to connect from another computer.", systemImage: "info.circle")
                        .font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
                HStack {
                    Button("Export Connection Files", action: model.export).disabled(!model.canExport)
                    if !model.exported.isEmpty { Button("Show Files in Finder", action: model.revealExports) }
                }
                Text(model.capture.active ? "Stop the camera before exporting connection files." : "The .env file contains a password. Keep it private; the endpoint alone does not include credentials.")
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
