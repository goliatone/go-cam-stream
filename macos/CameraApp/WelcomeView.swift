import SwiftUI

/// The first run, while no settings are saved: Allow Camera, then the
/// webcam and its camera name (prefilled), then the optional microphone.
/// Done ends on the console at Start Camera; Set Up Later goes there now.
/// Network and server settings stay in Settings until you need them.
struct WelcomeView: View {
    @ObservedObject var model: CameraModel
    let step: CameraModel.WelcomeStep

    var body: some View {
        VStack(spacing: 18) {
            header
            WelcomeStepsView(current: step)
            // One height for every step, so Back and Continue stay put.
            Form { content }
                .formStyle(.grouped)
                .scrollContentBackground(.hidden)
                .frame(minHeight: 150, maxHeight: 260)
            footer
        }
        .frame(maxWidth: 560)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var header: some View {
        VStack(spacing: 8) {
            Image(nsImage: NSImage(named: NSImage.applicationIconName) ?? NSImage())
                .resizable().frame(width: 64, height: 64).accessibilityHidden(true)
            Text("Welcome to Camstream Camera").font(.title.weight(.semibold))
                .accessibilityAddTraits(.isHeader)
            Text("Set up your camera in three steps. Choose a webcam to preview it, then start streaming from the main window.")
                .foregroundStyle(.secondary).multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    @ViewBuilder private var content: some View {
        switch step {
        case .access:
            Section {
                LabeledContent("Camera access") {
                    HStack(spacing: 8) {
                        Text(CameraSettingsPane.accessText(model.permission)).foregroundStyle(.secondary)
                        if model.permission == "authorized" {
                            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green).accessibilityHidden(true)
                        }
                    }
                }
                .accessibilityIdentifier("welcome-access")
            } footer: {
                SettingsFootnote(accessDetail)
            }
        case .camera:
            Section {
                WebcamFields(model: model, preview: model.webcamPreview, surface: .welcome)
            } footer: {
                SettingsFootnote(WebcamFields.footnote)
            }
            .disabled(!model.settingsEditable)
            // Access may have been granted in System Settings since the list loaded.
            .onAppear { model.devicesChanged() }
        case .microphone:
            MicrophoneSection(model: model)
        }
        // A camera service reply that refused these settings.
        if model.capture.phase == .error, let problem = model.capture.problem {
            Section { FieldProblem(problem) }
        }
    }

    private var accessDetail: String {
        switch model.permission {
        case "authorized": return "Camera access is on. Continue to choose your webcam."
        case "denied": return "Camera access is off. Turn on Camstream Camera in Privacy & Security → Camera, then come back here."
        case "restricted": return "Camera access is restricted on this Mac. Contact your device administrator, or choose Set Up Later."
        default: return "macOS asks once. Camera access is used for previews, streams and calls."
        }
    }

    private var footer: some View {
        HStack(spacing: 10) {
            Button("Set Up Later", action: model.skipWelcome)
                .help("Opens the console now. Start Camera applies these choices.")
                .accessibilityIdentifier("welcome-later")
            Spacer()
            if step != .access {
                Button("Back", action: model.backInWelcome).accessibilityIdentifier("welcome-back")
            }
            primary
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .accessibilityIdentifier("welcome-continue")
        }
        .controlSize(.large)
        .disabled(model.requestingPermission)
    }

    /// One step forward: the step's own action, then Continue, then Done.
    @ViewBuilder private var primary: some View {
        switch step {
        case .access:
            switch model.permission {
            case "not_determined":
                Button("Allow Camera", action: model.requestPermission).disabled(!model.canEditSettings)
            case "denied":
                Button("Open Camera Settings", action: model.openPrivacy)
            case "authorized":
                Button("Continue", action: model.continueWelcome)
            default:
                Button("Continue") {}.disabled(true)
            }
        case .camera:
            Button("Continue", action: model.continueWelcome).disabled(model.nameValidation != nil)
        case .microphone:
            Button("Done", action: model.finishWelcome).disabled(model.microphoneValidation != nil)
        }
    }
}

extension CameraModel.WelcomeStep {
    var title: String {
        switch self {
        case .access: return "Camera access"
        case .camera: return "Webcam and name"
        case .microphone: return "Microphone"
        }
    }
}

/// The three steps, with the current one filled and earlier ones checked.
private struct WelcomeStepsView: View {
    let current: CameraModel.WelcomeStep

    var body: some View {
        HStack(spacing: 10) {
            ForEach(CameraModel.WelcomeStep.allCases, id: \.self) { step in
                if step != .access {
                    Capsule().fill(Color.secondary.opacity(0.3)).frame(width: 24, height: 2).accessibilityHidden(true)
                }
                HStack(spacing: 6) {
                    Image(systemName: symbol(step)).foregroundStyle(tint(step)).imageScale(.large)
                    Text(step.title)
                        .fontWeight(step == current ? .semibold : .regular)
                        .foregroundStyle(step == current ? Color.primary : Color.secondary)
                }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(label(step))
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("welcome-steps")
    }

    private func symbol(_ step: CameraModel.WelcomeStep) -> String {
        if step.rawValue < current.rawValue { return "checkmark.circle.fill" }
        return "\(step.rawValue + 1).circle" + (step == current ? ".fill" : "")
    }

    private func tint(_ step: CameraModel.WelcomeStep) -> Color {
        if step.rawValue < current.rawValue { return .green }
        return step == current ? .accentColor : .secondary
    }

    private func label(_ step: CameraModel.WelcomeStep) -> String {
        let state = step.rawValue < current.rawValue ? "done" : step == current ? "current step" : "next"
        return "Step \(step.rawValue + 1) of \(CameraModel.WelcomeStep.allCases.count), \(step.title), \(state)"
    }
}
