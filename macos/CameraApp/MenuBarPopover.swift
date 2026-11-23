import AppKit
import SwiftUI

/// A button in the menu-bar popover: what it does, its title, and how it
/// leaves the popover.
enum PopoverAction: Equatable {
    case startCamera, stopCamera, finishSetup, showCall, retryPreview
    /// A fix from the stage or the banner, run as the console runs it.
    case fix(Feedback.Action)
    case copyShareLink, stopSharing
    /// While the camera streams, Start Call asks before it stops the stream.
    case startCall(asks: Bool)
    case joinCall, copyInvitation, cancelCall
    case endCall(guest: Bool)
    case dismissBanner, openWindow, settings, quit

    /// Sheets, confirmations and the call stage live in the window, so the
    /// actions that need them bring it forward first. Actions that open
    /// another window or app close the popover; the rest keep it open, so
    /// their result shows there.
    enum Handoff: Equatable { case stay, close, window }

    var title: String {
        switch self {
        case .startCamera: return "Start Camera"
        case .stopCamera: return "Stop Camera"
        case .finishSetup: return "Finish Setup"
        case .showCall: return "Show Call"
        case .retryPreview: return "Retry Preview"
        case .fix(let action): return action.title
        case .copyShareLink: return "Copy Share Link"
        case .stopSharing: return "Stop Sharing"
        case .startCall(let asks): return asks ? "Start Call…" : "Start Call"
        case .joinCall: return "Join Call…"
        case .copyInvitation: return "Copy Invitation"
        case .cancelCall: return "Cancel Call"
        case .endCall(let guest): return guest ? "Leave Call…" : "End Call…"
        case .dismissBanner: return "Dismiss"
        case .openWindow: return "Open Camstream Camera"
        case .settings: return "Settings…"
        case .quit: return "Quit"
        }
    }

    var identifier: String {
        switch self {
        case .startCamera: return "popover-start-camera"
        case .stopCamera: return "popover-stop-camera"
        case .finishSetup: return "popover-finish-setup"
        case .showCall: return "popover-show-call"
        case .retryPreview: return "popover-retry-preview"
        case .fix: return "popover-fix"
        case .copyShareLink: return "popover-copy-link"
        case .stopSharing: return "popover-stop-sharing"
        case .startCall: return "popover-start-call"
        case .joinCall: return "popover-join-call"
        case .copyInvitation: return "popover-copy-invitation"
        case .cancelCall: return "popover-cancel-call"
        case .endCall: return "popover-end-call"
        case .dismissBanner: return "popover-dismiss-banner"
        case .openWindow: return "popover-open-window"
        case .settings: return "popover-settings"
        case .quit: return "popover-quit"
        }
    }

    var handoff: Handoff {
        switch self {
        case .startCamera, .stopCamera, .retryPreview, .copyShareLink, .stopSharing, .copyInvitation, .cancelCall, .dismissBanner:
            return .stay
        case .finishSetup, .showCall, .startCall, .joinCall, .endCall, .openWindow:
            return .window
        case .settings, .quit:
            return .close
        case .fix(let action):
            switch action {
            // These open a call, which takes the window's stage.
            case .startNewConversation, .retryConversation, .retryRelay, .testLocalConversation,
                 .retryConversationInvitation:
                return .window
            // These open Settings, System Settings, Finder or the browser.
            case .chooseWebcam, .openCameraSettings, .openSetupGuide, .openInstallGuide, .showSettingsFolder:
                return .close
            case .allowCamera, .startCamera, .retryHelper, .checkConversationRoute, .reconnectCallConnector, .retryConversationClose:
                return .stay
            }
        }
    }
}

struct PopoverButton: Equatable {
    let action: PopoverAction
    var enabled = true
}

/// What the menu-bar popover shows. It reads the console's projections, so
/// the popover, the window and the status menu agree.
struct PopoverContent: Equatable {
    /// The shared preview while streaming, otherwise the stage's own
    /// message. Call video stays in the window.
    enum Picture: Equatable {
        /// The live frames, with "Preview starting…" until the first one.
        case preview
        case previewUnavailable
        case progress(String)
        case message(symbol: String, title: String, detail: String?, attention: Bool)
    }

    let pill: StatusPill
    /// The stream, share and call lines.
    let lines: [String]
    /// The console's one message, with at most its first fix.
    let banner: ConsoleBanner?
    let picture: Picture
    /// The console's prominent action for this state.
    let primary: PopoverButton?
    /// Why Start Camera is unavailable, under it.
    let primaryNote: String?
    /// The share and call actions.
    let actions: [PopoverButton]
}

extension CameraModel {
    var popover: PopoverContent {
        let stage = self.stage
        var message = banner
        if let first = message?.actions.first { message?.actions = [first] }
        let starts = welcomeShown == nil && stage.isOff
        return PopoverContent(pill: statusPill, lines: activity.lines, banner: message, picture: popoverPicture(stage),
                              primary: popoverPrimary(stage), primaryNote: starts ? startCameraBlockedReason : nil,
                              actions: popoverActions)
    }

    private func popoverPicture(_ stage: StageState) -> PopoverContent.Picture {
        switch stage {
        case .serviceStarting(let text): return .progress(text)
        case .serviceDown(let feedback), .problem(let feedback):
            return .message(symbol: "exclamationmark.triangle.fill", title: feedback.title, detail: feedback.message, attention: true)
        case .needsAccess(let feedback):
            return .message(symbol: "video.slash", title: feedback.title, detail: feedback.message, attention: false)
        case .off(let webcam, let name):
            return .message(symbol: "video.slash", title: "Camera off", detail: "\(webcam) · \(name)", attention: false)
        case .starting: return .progress("Starting camera…")
        case .stopping: return .progress("Stopping camera…")
        case .live: return preview.phase == .unavailable ? .previewUnavailable : .preview
        case .call(let call):
            switch call {
            case .opening(let text): return .progress(text)
            case .page:
                return .message(symbol: "phone.fill", title: "In a call", detail: "Call video shows in the Camstream window.", attention: false)
            case .ready(let host):
                return .message(symbol: "phone", title: host ? "Your call is ready" : "The call didn’t open",
                                detail: host ? "Join it in the Camstream window, or host it from your browser."
                                             : "Join again in the Camstream window.", attention: false)
            case .browser(let status):
                return .message(symbol: "safari", title: status, detail: "The host controls are open in your browser.", attention: false)
            case .ending: return .progress("Ending the call…")
            }
        }
    }

    /// The window's prominent action: the guide while it shows, then the
    /// stage's fix, Start or Stop Camera, or the call.
    private func popoverPrimary(_ stage: StageState) -> PopoverButton? {
        if welcomeShown != nil { return PopoverButton(action: .finishSetup) }
        switch stage {
        case .serviceStarting: return nil
        case .serviceDown(let feedback), .needsAccess(let feedback), .problem(let feedback):
            return feedback.actions.first.map { PopoverButton(action: .fix($0)) }
        case .off: return PopoverButton(action: .startCamera, enabled: canStartCamera)
        case .starting, .live: return PopoverButton(action: .stopCamera, enabled: canStop)
        case .stopping: return PopoverButton(action: .stopCamera, enabled: false)
        case .call: return PopoverButton(action: .showCall)
        }
    }

    /// The status menu's share and call actions. A call on the stage offers
    /// its own ending instead of Start and Join.
    private var popoverActions: [PopoverButton] {
        var actions: [PopoverButton] = []
        if sharing.showsHeaderStatus {
            actions += [PopoverButton(action: .copyShareLink, enabled: sharing.canCopyLink),
                        PopoverButton(action: .stopSharing, enabled: sharing.canStopSharing)]
        }
        if let call = callStage {
            let guest = conversationActivity?.role == .guest
            // A stream that was already running keeps the camera from the call.
            if capture.active, call != .ending { actions.append(PopoverButton(action: .stopCamera, enabled: canStop)) }
            if canCopyConversation { actions.append(PopoverButton(action: .copyInvitation)) }
            switch call {
            case .opening: actions.append(PopoverButton(action: .cancelCall))
            case .ending: actions.append(PopoverButton(action: .endCall(guest: guest), enabled: false))
            default: actions.append(PopoverButton(action: .endCall(guest: guest), enabled: endConfirmation != nil))
            }
        } else if !ready || conversationsSupported {
            actions += [PopoverButton(action: .startCall(asks: canStop), enabled: canStartConversation),
                        PopoverButton(action: .joinCall, enabled: canStartConversation)]
        }
        return actions
    }

    /// Runs a popover action as the console and the status menu do. The
    /// popover has already closed, and the window come forward, when the
    /// action's hand-off asks for it. Quit belongs to the app.
    func perform(popover action: PopoverAction) {
        switch action {
        case .startCamera: startCamera()
        case .stopCamera: stop()
        // The window shows these.
        case .finishSetup, .showCall, .openWindow: break
        case .retryPreview: preview.retry()
        case .fix(let fix): perform(fix)
        case .copyShareLink: sharing.copyLink()
        case .stopSharing: sharing.stopSharing()
        case .startCall: startCall()
        case .joinCall: showingJoin = true
        case .copyInvitation: copyConversationInvitation()
        case .cancelCall, .endCall: endCall()
        case .dismissBanner: dismissBanner()
        case .settings: showSettings(nil)
        case .quit: break
        }
    }
}

private extension StageState {
    var isOff: Bool {
        if case .off = self { return true }
        return false
    }
}

/// The menu-bar popover: the status, the shared preview and the main
/// actions, so the camera can be checked without the window.
struct MenuBarPopoverView: View {
    static let width: CGFloat = 320
    @ObservedObject var model: CameraModel
    @ObservedObject var preview: PreviewController
    let perform: (PopoverAction) -> Void
    let close: () -> Void

    var body: some View {
        let content = model.popover
        VStack(alignment: .leading, spacing: 12) {
            // A long status, such as a live share with its viewers, moves
            // under the title rather than being cut short.
            ViewThatFits(in: .horizontal) {
                HStack(alignment: .center, spacing: 8) {
                    title
                    Spacer(minLength: 8)
                    pill
                }
                VStack(alignment: .leading, spacing: 6) {
                    title
                    pill
                }
            }
            picture(content.picture)
            activity(content.lines)
            if let banner = content.banner { PopoverBannerView(banner: banner, perform: perform) }
            if let primary = content.primary { primaryButton(primary, note: content.primaryNote) }
            if !content.actions.isEmpty { actionGrid(content.actions) }
            Divider()
            footer
        }
        .padding(16)
        .frame(width: Self.width)
        .fixedSize(horizontal: false, vertical: true)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Camstream Camera")
        .accessibilityIdentifier("menu-bar-popover")
        .onExitCommand(perform: close)
    }

    private var title: some View {
        Text("Camstream Camera").font(.headline).lineLimit(1).fixedSize().accessibilityAddTraits(.isHeader)
    }

    private var pill: some View {
        StatusPillView(model: model, identifier: "popover-status", showsTooltip: false).fixedSize()
    }

    // MARK: Picture

    private func picture(_ picture: PopoverContent.Picture) -> some View {
        ZStack {
            Color.black
            switch picture {
            case .preview:
                if let image = preview.image {
                    Image(decorative: image, scale: 1).resizable().scaledToFit()
                        .accessibilityLabel("Live camera preview")
                } else {
                    progress("Preview starting…")
                }
            case .previewUnavailable:
                VStack(spacing: 8) {
                    message(symbol: "video", title: "Preview unavailable", detail: "The server stream keeps running.", attention: false)
                    Button(PopoverAction.retryPreview.title) { perform(.retryPreview) }
                        .controlSize(.small)
                        .accessibilityIdentifier(PopoverAction.retryPreview.identifier)
                }
            case .progress(let text):
                progress(text)
            case .message(let symbol, let title, let detail, let attention):
                message(symbol: symbol, title: title, detail: detail, attention: attention)
            }
        }
        .aspectRatio(16 / 9, contentMode: .fit)
        .frame(maxWidth: .infinity)
        .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
        .environment(\.colorScheme, .dark)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Camera")
        .accessibilityIdentifier("popover-preview")
    }

    private func progress(_ text: String) -> some View {
        VStack(spacing: 8) {
            ProgressView().controlSize(.small)
            Text(text).font(.headline).foregroundStyle(.white)
        }
        .multilineTextAlignment(.center)
        .padding(12)
        .accessibilityElement(children: .combine)
    }

    private func message(symbol: String, title: String, detail: String?, attention: Bool) -> some View {
        VStack(spacing: 6) {
            Image(systemName: symbol).font(.system(size: 22)).foregroundStyle(attention ? Color.orange : Color.white)
                .accessibilityHidden(true)
            Text(title).font(.headline).foregroundStyle(.white)
            if let detail {
                Text(detail).font(.caption).foregroundStyle(.white.opacity(0.75)).lineLimit(4)
            }
        }
        .multilineTextAlignment(.center)
        .padding(12)
        .help(detail ?? "")
        .accessibilityElement(children: .combine)
    }

    // MARK: Status and actions

    private func activity(_ lines: [String]) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                Text(line).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("popover-activity")
    }

    private func primaryButton(_ primary: PopoverButton, note: String?) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Button { perform(primary.action) } label: {
                Text(primary.action.title).frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .keyboardShortcut(.defaultAction)
            .disabled(!primary.enabled)
            .accessibilityIdentifier("popover-primary")
            if let note {
                Label(note, systemImage: "info.circle").font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("popover-start-reason")
            }
        }
    }

    private func actionGrid(_ actions: [PopoverButton]) -> some View {
        LazyVGrid(columns: [GridItem(.flexible(), spacing: 8), GridItem(.flexible(), spacing: 8)], spacing: 8) {
            ForEach(Array(actions.enumerated()), id: \.offset) { _, button in
                Button { perform(button.action) } label: {
                    Text(button.action.title).lineLimit(1).frame(maxWidth: .infinity)
                }
                .disabled(!button.enabled)
                .accessibilityIdentifier(button.action.identifier)
            }
        }
    }

    private var footer: some View {
        HStack(spacing: 14) {
            footerButton(.openWindow)
            Spacer(minLength: 8)
            footerButton(.settings)
            footerButton(.quit).accessibilityLabel("Quit Camstream Camera")
        }
        .buttonStyle(.borderless)
    }

    private func footerButton(_ action: PopoverAction) -> some View {
        Button(action.title) { perform(action) }.lineLimit(1).fixedSize().accessibilityIdentifier(action.identifier)
    }
}

/// The console's message in the popover, with at most its first fix.
private struct PopoverBannerView: View {
    let banner: ConsoleBanner
    let perform: (PopoverAction) -> Void

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Image(systemName: banner.problem ? "exclamationmark.triangle.fill" : "info.circle")
                .foregroundStyle(banner.problem ? Color.orange : Color.secondary).accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 8) {
                message.font(.callout).fixedSize(horizontal: false, vertical: true)
                if let action = banner.actions.first {
                    Button(action.title) { perform(.fix(action)) }
                        .controlSize(.small)
                        .accessibilityIdentifier("popover-banner-action")
                }
            }
            Spacer(minLength: 4)
            if banner.dismissible {
                Button { perform(.dismissBanner) } label: { Image(systemName: "xmark") }
                    .buttonStyle(.borderless).help("Dismiss").accessibilityLabel("Dismiss")
                    .accessibilityIdentifier(PopoverAction.dismissBanner.identifier)
            }
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8).fill(banner.problem ? Color.orange.opacity(0.12) : Color.secondary.opacity(0.08)))
        .overlay(RoundedRectangle(cornerRadius: 8)
            .strokeBorder(banner.problem ? Color.orange.opacity(0.8) : Color.secondary.opacity(0.25),
                          style: StrokeStyle(lineWidth: 1, dash: banner.problem ? [5, 3] : [])))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("popover-banner")
    }

    /// A problem's name in bold, then its cause.
    private var message: Text {
        guard let title = banner.title else { return Text(banner.text) }
        return Text("\(Text(title + ".").bold()) \(banner.text)")
    }
}

/// Owns the menu-bar icon's popover. It closes when you click elsewhere or
/// press Escape. While it shows, the model counts it as a preview surface,
/// so the preview runs even with the window closed.
@MainActor
final class MenuBarPopover: NSObject, NSPopoverDelegate {
    private let model: CameraModel
    private let popover = NSPopover()
    /// The menu-bar icon the popover hangs from.
    weak var anchor: NSView?
    var quit: () -> Void = { NSApp.terminate(nil) }
    var isShown: Bool { popover.isShown }

    init(model: CameraModel) {
        self.model = model
        super.init()
        popover.behavior = .transient
        popover.animates = true
        popover.delegate = self
    }

    func toggle() {
        if popover.isShown { close() } else { show() }
    }

    func show() {
        guard let anchor, anchor.window != nil, !popover.isShown else { return }
        // The view exists only while the popover shows, so a closed popover
        // doesn't follow preview frames or tick its elapsed time.
        let view = MenuBarPopoverView(model: model, preview: model.preview,
                                      perform: { [weak self] in self?.perform($0) },
                                      close: { [weak self] in self?.close() })
        let hosting = NSHostingController(rootView: view)
        hosting.sizingOptions = [.preferredContentSize]
        popover.contentViewController = hosting
        model.refreshPermission()
        // The popover takes keyboard focus, and closes on a click elsewhere,
        // only while the app is active.
        NSApp.activate(ignoringOtherApps: true)
        popover.show(relativeTo: anchor.bounds, of: anchor, preferredEdge: .minY)
        popover.contentViewController?.view.window?.makeKey()
        model.setPreviewSurface(.popover, visible: popover.isShown)
    }

    func close() {
        guard popover.isShown else { return }
        popover.close()
        model.setPreviewSurface(.popover, visible: false)
    }

    private func perform(_ action: PopoverAction) {
        switch action.handoff {
        case .stay: break
        case .close: close()
        case .window: close(); model.showWindow()
        }
        if action == .quit { quit() } else { model.perform(popover: action) }
    }

    /// A click on the icon toggles the popover through the icon's action;
    /// closing here too would reopen it when the click ends.
    func popoverShouldClose(_ popover: NSPopover) -> Bool {
        let clicks: [NSEvent.EventType] = [.leftMouseDown, .rightMouseDown, .otherMouseDown]
        guard let event = NSApp.currentEvent, let anchor, event.window === anchor.window else { return true }
        return !clicks.contains(event.type)
    }

    func popoverDidShow(_ notification: Notification) { model.setPreviewSurface(.popover, visible: true) }
    func popoverDidClose(_ notification: Notification) {
        model.setPreviewSurface(.popover, visible: false)
        // Released on the next turn, so a button that closed the popover
        // never frees the view its action is running in.
        DispatchQueue.main.async { [weak self] in
            guard let self, !self.popover.isShown else { return }
            self.popover.contentViewController = nil
        }
    }
}
