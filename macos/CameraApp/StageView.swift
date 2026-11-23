import SwiftUI

/// The camera at 16:9 with one overlay per state, and that state's actions
/// under it. Start or Stop Camera is the window's one prominent button. A
/// call takes the stage while it opens, runs or ends, with End Call under it.
struct StageView: View {
    @ObservedObject var model: CameraModel
    @ObservedObject var preview: PreviewController
    /// Beside the cards, the call page takes the column's height. Stacked
    /// above them, it keeps a fixed shape.
    var fillsHeight = false

    var body: some View {
        let state = model.stage
        VStack(spacing: 12) {
            screen(state)
                .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                .accessibilityElement(children: .contain)
                .accessibilityLabel(Self.isCall(state) ? "Call" : "Camera")
                .accessibilityIdentifier(Self.isCall(state) ? "call-stage" : "camera-stage")
                .frame(maxWidth: .infinity)
                // The stage takes the column's height first; the controls keep theirs.
                .layoutPriority(1)
            controls(state)
        }
        .onAppear { announce(Self.announced(state)) }
        .onChange(of: Self.announced(state)) { _, new in announce(new) }
    }

    private static func isCall(_ state: StageState) -> Bool {
        if case .call = state { return true }
        return false
    }

    /// Failures are announced, as the recovery cards were; other states show
    /// in the status pill, which VoiceOver reads on change.
    private static func announced(_ state: StageState) -> Feedback? {
        switch state {
        case .serviceDown(let feedback), .problem(let feedback): return feedback
        default: return nil
        }
    }

    private func announce(_ feedback: Feedback?) {
        guard let feedback else { return }
        AccessibilityNotification.Announcement(feedback.announcement).post()
    }

    @ViewBuilder private func screen(_ state: StageState) -> some View {
        if case .call(.page) = state, let surface = model.conversationSurface {
            callPage(surface)
        } else {
            ZStack {
                Color.black
                overlay(state)
            }
            .aspectRatio(16 / 9, contentMode: .fit)
            .environment(\.colorScheme, .dark)
        }
    }

    /// The call page lays out its own video and controls.
    @ViewBuilder private func callPage(_ surface: ConversationMediaSurface) -> some View {
        let page = CallPageView(surface: surface).id(ObjectIdentifier(surface))
            .accessibilityIdentifier("conversation-media")
        if fillsHeight {
            page.frame(minHeight: 300, maxHeight: .infinity)
        } else {
            page.aspectRatio(4 / 3, contentMode: .fit)
        }
    }

    @ViewBuilder private func overlay(_ state: StageState) -> some View {
        switch state {
        case .serviceStarting(let text):
            message(progress: true, title: text)
                .accessibilityIdentifier("camera-service-progress")
        case .serviceDown(let feedback):
            message(symbol: "exclamationmark.triangle.fill", tint: .orange, title: feedback.title, detail: feedback.message)
                .accessibilityIdentifier("camera-service-error")
        case .needsAccess(let feedback):
            message(symbol: "video.slash", title: feedback.title, detail: feedback.message)
                .accessibilityIdentifier("camera-access")
        case .problem(let feedback):
            message(symbol: "exclamationmark.triangle.fill", tint: .orange, title: feedback.title, detail: feedback.message)
                .accessibilityIdentifier("camera-error")
        case .off(let webcam, let name):
            message(symbol: "video.slash", title: "Camera off", detail: "\(webcam) · \(name)")
        case .starting:
            message(progress: true, title: "Starting camera…")
        case .stopping:
            message(progress: true, title: "Stopping camera…")
        case .live:
            live
        case .call(let call):
            callOverlay(call)
        }
    }

    @ViewBuilder private func callOverlay(_ call: CallStage) -> some View {
        switch call {
        case .opening(let text):
            message(progress: true, title: text)
                .accessibilityIdentifier("call-progress")
        case .page:
            // The page replaces this as soon as its view exists.
            message(progress: true, title: "Opening the call…")
        case .ready(let host):
            message(symbol: "phone", title: host ? "Your call is ready" : "The call didn’t open here",
                    detail: host ? "Join it here, or host it from your browser." : "Join again to open it here.")
        case .browser(let status):
            message(symbol: "safari", title: status,
                    detail: "The host controls are open in your browser. Turn on your camera and mic there.")
                .accessibilityIdentifier("browser-room")
        case .ending:
            message(progress: true, title: "Ending the call…")
        }
    }

    private var live: some View {
        ZStack(alignment: .topLeading) {
            Group {
                if let image = preview.image {
                    Image(decorative: image, scale: 1).resizable().scaledToFit()
                        .accessibilityLabel("Live camera preview")
                } else {
                    message(progress: preview.phase == .connecting, symbol: preview.phase == .connecting ? nil : "video",
                            title: previewTitle, detail: previewDetail)
                }
            }
            // Fills the stage, so the badges sit in its corner rather than the message's.
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            HStack(spacing: 6) {
                badge("Live", symbol: "circle.fill", tint: .green)
                if model.capture.audioEnabled {
                    badge(model.capture.audioState == "live" ? "Mic" : "Mic starting…", symbol: "mic.fill", tint: .white)
                }
            }
            .padding(12)
            .accessibilityElement(children: .combine)
            .accessibilityLabel(previewStatus)
            .accessibilityIdentifier("preview-status")
        }
    }

    private var previewTitle: String {
        switch preview.phase {
        case .unavailable: return "Preview unavailable"
        case .idle: return "Preview paused"
        default: return "Waiting for video…"
        }
    }
    private var previewDetail: String? {
        switch preview.phase {
        case .unavailable: return "The server stream keeps running."
        case .idle: return "The preview pauses while this window is hidden. Streaming continues."
        default: return nil
        }
    }
    private var previewStatus: String {
        let mic = model.capture.audioEnabled ? (model.capture.audioState == "live" ? ", microphone live" : ", microphone starting") : ""
        switch preview.phase {
        case .live: return "Live" + mic
        default: return "Live, " + previewTitle.lowercased() + mic
        }
    }

    private func badge(_ text: String, symbol: String, tint: Color) -> some View {
        Label {
            Text(text)
        } icon: {
            Image(systemName: symbol).foregroundStyle(tint).imageScale(.small)
        }
        .font(.caption.weight(.semibold))
        .foregroundStyle(.white)
        .padding(.horizontal, 8).padding(.vertical, 4)
        .background(Capsule().fill(Color.black.opacity(0.55)))
    }

    private func message(progress: Bool = false, symbol: String? = nil, tint: Color = .white,
                         title: String, detail: String? = nil) -> some View {
        VStack(spacing: 10) {
            if progress {
                ProgressView().controlSize(.large)
            } else if let symbol {
                Image(systemName: symbol).font(.system(size: 34)).foregroundStyle(tint).accessibilityHidden(true)
            }
            Text(title).font(.title3.weight(.semibold)).foregroundStyle(.white)
            if let detail {
                Text(detail).foregroundStyle(.white.opacity(0.75)).fixedSize(horizontal: false, vertical: true)
            }
        }
        .multilineTextAlignment(.center)
        .frame(maxWidth: 440)
        .padding(24)
        .accessibilityElement(children: .combine)
    }

    // MARK: Controls under the stage

    @ViewBuilder private func controls(_ state: StageState) -> some View {
        HStack(alignment: .center, spacing: 10) {
            switch state {
            case .serviceStarting:
                EmptyView()
            case .serviceDown(let feedback), .needsAccess(let feedback), .problem(let feedback):
                actions(feedback.actions)
            case .off:
                startButton
                startReason
            case .starting:
                stopButton
            case .live:
                stopButton
                if preview.phase == .unavailable { Button("Retry Preview", action: preview.retry) }
            case .stopping:
                Button("Stop Camera") {}.buttonStyle(.borderedProminent).disabled(true)
            case .call(let call):
                callControls(call)
            }
        }
        .controlSize(.large)
    }

    private var stopButton: some View {
        Button("Stop Camera", action: model.stop).buttonStyle(.borderedProminent).disabled(!model.canStop)
            .help("Stop Camera (⇧⌘S)")
    }

    private var startButton: some View {
        Button("Start Camera", action: model.startCamera).buttonStyle(.borderedProminent)
            .disabled(!model.canStartCamera)
            .help(model.startCameraBlockedReason ?? "Start Camera (⇧⌘S)")
            .accessibilityHint(model.startCameraBlockedReason ?? "Starts the server stream. Changed settings apply first.")
    }

    @ViewBuilder private var startReason: some View {
        if model.working {
            ProgressView().controlSize(.small).accessibilityLabel("Applying settings")
        } else if let reason = model.startCameraBlockedReason {
            Label(reason, systemImage: "info.circle").font(.callout).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true).accessibilityIdentifier("start-reason")
            if let pane = model.startSettingsPane {
                Button("Open \(pane.title) Settings…") { model.showSettings(pane) }.buttonStyle(.link).controlSize(.regular)
            }
        }
    }

    /// End Call is the call's one prominent action; a call that isn't open
    /// yet offers Join Here first.
    @ViewBuilder private func callControls(_ call: CallStage) -> some View {
        switch call {
        case .opening:
            Button("Cancel", action: model.endCall)
        case .page, .browser:
            endButton.buttonStyle(.borderedProminent).tint(.red)
        case .ready(let host):
            Button("Join Here", action: model.joinConversationHere).buttonStyle(.borderedProminent)
            if host {
                Button("Open in Browser", action: model.hostConversationInBrowser)
                    .help("Opens the host controls in your default browser, which then uses your camera and mic.")
            }
            endButton
        case .ending:
            endButton.disabled(true)
        }
        // A stream that was already running keeps the camera from the call.
        if model.capture.active, call != .ending {
            Label("Your camera is streaming to your server. Stop Camera to use it in this call.", systemImage: "info.circle")
                .font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("call-camera-busy")
            Button("Stop Camera", action: model.stop).disabled(!model.canStop)
        }
    }

    private var endButton: some View {
        let guest = model.conversationActivity?.role == .guest
        return Button(guest ? "Leave Call" : "End Call", action: model.endCall)
            .help(guest ? "Leave the call. The host stays in it." : "End the call for everyone in it.")
    }

    @ViewBuilder private func actions(_ actions: [Feedback.Action]) -> some View {
        ForEach(Array(actions.enumerated()), id: \.offset) { index, action in
            if index == 0 {
                Button(action.title) { model.perform(action) }.buttonStyle(.borderedProminent)
            } else {
                Button(action.title) { model.perform(action) }
            }
        }
    }
}
