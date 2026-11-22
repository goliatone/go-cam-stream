import SwiftUI

/// Settings → Camera: access, the webcam, its camera name and the optional
/// microphone. Choices apply as soon as they're valid; text applies when
/// you press Return or leave the field.
struct CameraSettingsPane: View {
    @ObservedObject var model: CameraModel
    @FocusState private var nameFocused: Bool

    var body: some View {
        Form {
            SettingsStatusSection(model: model)
            Section {
                LabeledContent("Camera access") {
                    HStack(spacing: 8) {
                        Text(accessText).foregroundStyle(.secondary).multilineTextAlignment(.trailing)
                        if model.permission == "not_determined" {
                            Button("Allow Camera", action: model.requestPermission).disabled(!model.canEditSettings)
                        } else if model.permission != "authorized" {
                            Button("Open Camera Settings", action: model.openPrivacy)
                        } else {
                            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green).accessibilityHidden(true)
                        }
                    }
                }
            } footer: {
                SettingsFootnote("Nothing is captured until you choose Start Camera.")
            }
            Section {
                Picker("Webcam", selection: applying(\.device)) {
                    Text("Built-in camera (automatic)").tag("auto")
                    if model.draft.device != "auto", !model.devices.contains(where: { $0.name == model.draft.device }) {
                        Text("\(model.draft.device) — unavailable").tag(model.draft.device)
                    }
                    ForEach(model.devices) { device in Text(device.name).tag(device.name) }
                }
                TextField("Camera name", text: $model.draft.path, prompt: Text("macbook"))
                    .focused($nameFocused)
                    .onSubmit { model.applySettings() }
                if let problem = model.nameValidation { FieldProblem(problem) }
            } footer: {
                SettingsFootnote("The camera name ends the RTSP address: letters, numbers, dashes or underscores.")
            }
            .disabled(!model.settingsEditable)
            microphone
        }
        .onAppear { model.devicesChanged() }
        .onChange(of: nameFocused) { _, focused in if !focused { model.applySettings() } }
    }

    private var accessText: String {
        switch model.permission {
        case "authorized": return "Allowed"
        case "denied": return "Off in Privacy & Security"
        case "restricted": return "Restricted on this Mac"
        default: return "Not allowed yet"
        }
    }

    private var microphone: some View {
        Section {
            Toggle("Include microphone audio", isOn: Binding(get: { model.draft.audioEnabled }, set: { enabled in
                model.setMicrophoneEnabled(enabled)
                model.applySettings()
            }))
            .accessibilityIdentifier("enable-microphone")
            .disabled(!model.settingsEditable || (!model.audioSupported && !model.draft.audioEnabled))
            if !model.audioSupported {
                // Settings render only once the camera service is ready, so this is its answer.
                Text("This version of the camera service doesn’t support microphone audio. Install a complete, current Camstream Camera to use it; video-only streaming still works.")
                    .font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            } else if model.draft.audioEnabled {
                LabeledContent("Microphone access") {
                    HStack(spacing: 8) {
                        if model.microphonePermission == "not_determined" {
                            Button("Allow Microphone", action: model.requestMicrophonePermission)
                                .disabled(!model.canEditSettings).accessibilityIdentifier("allow-microphone")
                        } else if model.microphonePermission == "denied" {
                            Button("Open Microphone Settings", action: model.openMicrophonePrivacy).disabled(model.capture.active)
                        } else if model.microphonePermission == "authorized" {
                            Text("Allowed").foregroundStyle(.secondary)
                            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green).accessibilityHidden(true)
                        } else {
                            Text("Restricted on this Mac").foregroundStyle(.secondary)
                        }
                    }
                }
                Picker("Microphone", selection: Binding(get: { MicrophoneID(model.draft.audioDeviceID) }, set: { id in
                    model.selectMicrophone(id)
                    model.applySettings()
                })) {
                    Text("Choose a microphone").tag(MicrophoneID(""))
                    if !model.draft.audioDeviceID.isEmpty, !model.microphones.contains(where: { $0.matches(model.draft.audioDeviceID) }) {
                        Text(model.microphonesLoaded ? "Saved microphone — unavailable" : "Saved microphone").tag(MicrophoneID(model.draft.audioDeviceID))
                    }
                    ForEach(model.microphones, id: \.selectionID) { device in Text(device.label(in: model.microphones)).tag(device.selectionID) }
                }
                .accessibilityIdentifier("microphone-selection")
                .disabled(!model.settingsEditable || model.microphonePermission != "authorized")
                if model.microphonesLoaded && model.microphones.isEmpty {
                    FieldProblem("No microphones are available. Connect one, or turn off microphone audio.")
                } else if model.microphonePermission == "authorized", let problem = model.microphoneValidation {
                    FieldProblem(problem)
                }
            }
        } header: {
            Text("Microphone")
        } footer: {
            SettingsFootnote(model.draft.audioEnabled
                ? "The local preview is silent. Muting in a browser changes playback only; turn this off to stop sending sound."
                : "Streams are video only unless you include microphone audio.")
        }
        .disabled(!model.settingsEditable)
    }

    /// A choice that applies as soon as it's valid.
    private func applying(_ keyPath: WritableKeyPath<CameraConfig, String>) -> Binding<String> {
        Binding(get: { model.draft[keyPath: keyPath] }, set: { value in
            model.draft[keyPath: keyPath] = value
            model.applySettings()
        })
    }
}

/// Settings → Network: who can connect to the stream, and on which port.
struct NetworkSettingsPane: View {
    @ObservedObject var model: CameraModel
    private enum Field { case host, port }
    @FocusState private var focus: Field?

    var body: some View {
        Form {
            SettingsStatusSection(model: model)
            Section {
                Picker("Who can connect", selection: Binding(get: { model.draft.listen }, set: { value in
                    model.draft.listen = value
                    model.applySettings()
                })) {
                    Text("Only this Mac").tag("127.0.0.1")
                    Text("Private network").tag("0.0.0.0")
                }
                .pickerStyle(.radioGroup)
                if model.draft.listen == "0.0.0.0" {
                    TextField("Mac address", text: $model.draft.host, prompt: Text("IP address or hostname"))
                        .focused($focus, equals: .host)
                        .onSubmit { model.applySettings() }
                        .accessibilityLabel("Mac IP address or hostname")
                    if let problem = model.hostValidation { FieldProblem(problem) }
                }
                TextField("RTSP port", text: $model.portText, prompt: Text("8554"))
                    .focused($focus, equals: .port)
                    .onSubmit { model.applySettings() }
                if let problem = model.portValidation { FieldProblem(problem) }
            } footer: {
                SettingsFootnote(model.draft.listen == "127.0.0.1"
                    ? "Only a server on this Mac can connect. Either way, players need the stream password."
                    : "Servers on your network connect with the stream password. Use a trusted LAN or VPN; don’t expose this port to the internet.")
            }
            .disabled(!model.settingsEditable)
        }
        .onChange(of: focus) { old, new in if old != nil, old != new { model.applySettings() } }
    }
}

/// Settings → Server: the Camstream server's address and status, and the
/// files that connect it to this camera.
struct ServerSettingsPane: View {
    @ObservedObject var model: CameraModel
    @ObservedObject var server: ServerMonitor

    var body: some View {
        Form {
            Section("Server") {
                TextField("Server URL", text: $server.address, prompt: Text(ServerMonitor.defaultAddress))
                    .onSubmit(server.checkNow)
                    .accessibilityIdentifier("server-url")
                LabeledContent("Status") {
                    HStack(spacing: 10) {
                        Text(server.message).foregroundStyle(.secondary).multilineTextAlignment(.trailing)
                            .fixedSize(horizontal: false, vertical: true)
                            .accessibilityIdentifier("server-status")
                        // Once the server answers, its viewer opens from here.
                        if let url = server.viewerURL {
                            Link("Open in Browser", destination: url)
                                .help(url.absoluteString)
                                .accessibilityHint("Opens \(url.absoluteString) in your browser.")
                                .accessibilityIdentifier("open-server-viewer")
                        }
                    }
                }
                HStack {
                    Spacer()
                    Button("Check Now", action: server.checkNow).disabled(!server.canCheck)
                        .accessibilityIdentifier("check-server-now")
                }
            }
            Section {
                if model.saved == nil {
                    Text("Start Camera once to create the connection files.").foregroundStyle(.secondary)
                } else {
                    ForEach(Array(model.connectionSteps.enumerated()), id: \.offset) { index, step in
                        ConnectionStepRow(number: index + 1, step: step) {
                            if index == 0 { exportActions }
                        }
                    }
                }
            } header: {
                HStack(spacing: 6) {
                    Text("Connect your server")
                    InfoButton(text: "Exporting or showing the files doesn’t stop the camera or change the stream password.",
                               label: "About connection files")
                }
            }
            if let saved = model.saved {
                Section {
                    LabeledContent("RTSP address") {
                        Text(saved.endpoint).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                            .accessibilityLabel("RTSP address: \(saved.endpoint)")
                    }
                    HStack(spacing: 6) {
                        Spacer()
                        InfoButton(text: "The address has no password. Players also need the stream password from the exported .env file, even on this Mac.",
                                   label: "About the stream password")
                        Button("Copy Address", action: model.copyEndpoint)
                            .accessibilityHint("Copies the RTSP address, without the password.")
                            .accessibilityIdentifier("copy-rtsp-address")
                    }
                } footer: {
                    if saved.listen == "127.0.0.1" {
                        SettingsFootnote("This address works only on this Mac. Choose Private network in Network settings to connect from another computer.")
                    }
                }
            }
            if let notice = model.notice {
                Section {
                    Text(notice).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("camera-notice")
                }
            }
        }
    }

    @ViewBuilder private var exportActions: some View {
        HStack {
            Button("Export Connection Files", action: model.export)
                .buttonStyle(.borderedProminent).disabled(!model.canExport)
                .accessibilityIdentifier("export-connection-files")
            if !model.exported.isEmpty { Button("Show Files in Finder", action: model.revealExports) }
        }
        if model.dirty {
            FieldProblem(model.validation.map { "Fix this before exporting: \($0)" } ?? "Wait for your changes to apply before exporting.")
        }
    }
}

private struct ConnectionStepRow<Actions: View>: View {
    let number: Int
    let step: ConnectionStep
    @ViewBuilder var actions: () -> Actions

    var body: some View {
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
            Spacer(minLength: 0)
        }.accessibilityElement(children: .contain)
    }
}

/// Settings → Calls: the call connector, the setup guide and a local test.
/// Call settings themselves are a file in the settings folder for now.
struct CallSettingsPane: View {
    @ObservedObject var model: CameraModel
    let showCall: () -> Void

    var body: some View {
        Form {
            SettingsStatusSection(model: model)
            Section {
                HStack(alignment: .firstTextBaseline) {
                    if model.reconnectingCallConnector {
                        ProgressView("Reconnecting the call connector…").controlSize(.small)
                    } else {
                        Label(model.callConnectorStatus.title,
                              systemImage: model.callConnectorStatus == .connected ? "checkmark.circle" : "network")
                    }
                    Spacer()
                    if model.callConnectorStatus == .setupRequired {
                        Button("Show Settings Folder", action: model.revealSettingsFolder)
                    } else if model.callConnectorStatus != .external {
                        Button("Reconnect", action: model.reconnectCallConnector)
                            .disabled(model.reconnectingCallConnector || !model.ready || model.openingConversation)
                            .accessibilityHint("Restarts Camstream’s dedicated call connector.")
                    }
                }.accessibilityIdentifier("call-connector-status")
            } header: {
                Text("Call connector")
            } footer: {
                SettingsFootnote(model.callConnectorStatus == .external
                    ? "Guests reach this Mac through the tunnel or server named in your call settings."
                    : "Camstream runs a dedicated connector so guests can reach calls on this Mac.")
            }
            Section("Setup") {
                LabeledContent("Setup guide") { Button("Open Setup Guide", action: model.openSetupGuide) }
                LabeledContent("Call settings") { Button("Show Settings Folder", action: model.revealSettingsFolder) }
                if model.supportsCallRecovery {
                    LabeledContent("Try a call") {
                        Button("Test on This Mac") {
                            showCall()
                            model.testConversationLocally()
                        }
                        .disabled(!model.canStartConversation)
                        .help(model.conversationBlockedReason ?? "Starts a call that only this Mac can join.")
                    }
                }
                if model.hasConversationDiagnostics {
                    LabeledContent("Diagnostics") {
                        Button("Copy Diagnostics", action: model.copyConversationDiagnostics)
                            .accessibilityHint("Copies the app version and setup reason.")
                    }
                }
            }
        }
    }
}
