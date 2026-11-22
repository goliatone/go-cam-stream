import SwiftUI

/// The camera console: one message slot, the stage, and a card each for the
/// server stream, the share link and calls. One-time setup is in Settings.
struct ConsoleView: View {
    @ObservedObject var model: CameraModel
    @ObservedObject var server: ServerMonitor
    /// Below this width the cards stack under the stage.
    static let twoColumnWidth: CGFloat = 860
    static let cardsWidth: CGFloat = 300

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            if let banner = model.banner { BannerView(banner: banner, model: model) }
            if model.ready {
                GeometryReader { proxy in
                    if proxy.size.width >= Self.twoColumnWidth {
                        HStack(alignment: .top, spacing: 20) {
                            StageView(model: model, preview: model.preview, fillsHeight: true)
                                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
                            ScrollView { cards }.frame(width: Self.cardsWidth)
                        }
                    } else {
                        ScrollView {
                            VStack(spacing: 18) {
                                StageView(model: model, preview: model.preview)
                                cards
                            }.padding(.trailing, 4)
                        }
                    }
                }
            } else {
                // The cards wait for the camera service; the stage says why.
                StageView(model: model, preview: model.preview)
                    .frame(maxWidth: 760)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            }
        }
    }

    private var cards: some View {
        VStack(spacing: 12) {
            ServerStreamCard(model: model, server: server)
            ShareLinkCard(model: model, sharing: model.sharing)
            CallCard(model: model)
        }
    }
}

/// A title, one state line, one main action and details on demand.
struct ActivityCard<Content: View>: View {
    let title: String
    let symbol: String
    var tint: Color = .secondary
    let state: String
    var stateTint: Color = .primary
    var stateIdentifier: String?
    @ViewBuilder var content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label {
                Text(title).font(.headline)
            } icon: {
                Image(systemName: symbol).foregroundStyle(tint)
            }
            .accessibilityAddTraits(.isHeader)
            Text(state).font(.callout).foregroundStyle(stateTint)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier(stateIdentifier ?? "")
            content()
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Color(nsColor: .separatorColor)))
        .accessibilityElement(children: .contain)
        .accessibilityLabel(title)
    }
}

private struct CardDetail: View {
    let text: String
    var tint: Color = .secondary
    var body: some View {
        Text(text).font(.callout).foregroundStyle(tint).fixedSize(horizontal: false, vertical: true)
    }
}

struct ServerStreamCard: View {
    @ObservedObject var model: CameraModel
    @ObservedObject var server: ServerMonitor

    var body: some View {
        ActivityCard(title: "Server stream", symbol: "server.rack", tint: tint, state: server.message,
                     stateIdentifier: "server-card-status") {
            if let saved = model.saved {
                Text(saved.endpoint).font(.system(.callout, design: .monospaced)).textSelection(.enabled)
                    .lineLimit(1).truncationMode(.middle).help(saved.endpoint)
                    .accessibilityLabel("RTSP address: \(saved.endpoint)")
                HStack(spacing: 8) {
                    Button("Copy Address", action: model.copyEndpoint)
                        .help("Copies the address without the password. Players also need the stream password from the exported .env file.")
                        .accessibilityHint("Copies the RTSP address, without the password.")
                        .accessibilityIdentifier("copy-rtsp-address")
                    if let url = server.viewerURL {
                        Link("Open in Browser", destination: url)
                            .help(url.absoluteString)
                            .accessibilityHint("Opens \(url.absoluteString) in your browser.")
                            .accessibilityIdentifier("open-server-viewer")
                    }
                }
            } else {
                CardDetail(text: "Start Camera once to create this camera’s address.")
            }
            Button("Server Settings…") { model.showSettings(.server) }.buttonStyle(.link)
        }
    }

    private var tint: Color {
        switch server.status {
        case .reachable: return .green
        case .accessDenied, .unavailable: return .orange
        case .unchecked: return .secondary
        }
    }
}

struct ShareLinkCard: View {
    @ObservedObject var model: CameraModel
    @ObservedObject var sharing: SharingModel
    @State private var showingOptions = false

    var body: some View {
        ActivityCard(title: "Share link", symbol: symbol, tint: tint, state: sharing.headline, stateIdentifier: "sharing-status") {
            if let viewers = sharing.viewerLine {
                CardDetail(text: viewers, tint: .primary).accessibilityIdentifier("sharing-viewers")
            }
            if let media = mediaLine {
                CardDetail(text: media.text, tint: media.problem ? .orange : .secondary).accessibilityIdentifier("sharing-media")
            }
            if let detail { CardDetail(text: detail) }
            if sharing.showsLinkControls { badges }
            actions
        }
    }

    /// What the video is doing once someone opens the link. Until then the
    /// viewer line already says no one is watching.
    private var mediaLine: (text: String, problem: Bool)? {
        guard let media = sharing.share?.media, let label = sharing.mediaLabel else { return nil }
        if sharing.phase == .degraded || media == .unavailable { return (label, true) }
        if media == .idle { return nil }
        return (sharing.detail ?? label, false)
    }

    /// The model's detail, except before a link exists: Share… can start the
    /// camera now, so "start the camera first" no longer applies.
    private var detail: String? {
        guard sharing.connected, sharing.supported else { return sharing.detail }
        if !sharing.ingressReady, !sharing.showsLinkControls { return "Set up the guest page once in Sharing settings." }
        switch sharing.phase {
        case .idle: return "Creates a link that someone can open in a browser to watch."
        // The viewer and media lines cover an active link; a failure shows in the banner.
        case .active, .failed: return nil
        default: return sharing.detail
        }
    }

    private var badges: some View {
        HStack(spacing: 10) {
            // The audience badge sets a link apart from the private stream.
            if let badge = sharing.scopeBadge {
                Label(badge.title, systemImage: badge.symbol).font(.caption.weight(.semibold))
                    .accessibilityIdentifier("sharing-scope")
            }
            Label("Video only", systemImage: "video").font(.caption).foregroundStyle(.secondary)
                .accessibilityLabel("Video only. Guests receive no audio.").accessibilityIdentifier("sharing-video-only")
        }
    }

    @ViewBuilder private var actions: some View {
        if sharing.showsLinkControls {
            HStack(spacing: 8) {
                Button(sharing.copying ? "Copying…" : "Copy Link", action: sharing.copyLink)
                    .disabled(!sharing.canCopyLink)
                    .accessibilityIdentifier("copy-share-link")
                Button("Stop Sharing", action: sharing.stopSharing).disabled(!sharing.canStopSharing)
                    .help("Ends the link for every viewer. The camera and the server stream keep running.")
                    .accessibilityHint("Ends the link and every viewer's video. The camera keeps running.")
                    .accessibilityIdentifier("stop-sharing")
            }
        } else if sharing.connected, sharing.supported, !sharing.ingressReady {
            Button("Set Up Sharing…") { model.showSettings(.sharing) }
        } else if sharing.connected, sharing.supported {
            HStack(spacing: 8) {
                Button(sharing.phase == .pending ? "Creating Link…" : shareTitle) { showingOptions = true }
                    .disabled(!sharing.canEditOptions)
                    .accessibilityHint("Choose how long the link lasts and how many people can watch.")
                    .accessibilityIdentifier("open-share-options")
                    .popover(isPresented: $showingOptions, arrowEdge: .bottom) {
                        ShareOptionsView(model: model, sharing: sharing) { showingOptions = false }
                    }
                if sharing.phase == .pending { ProgressView().controlSize(.small) }
            }
        }
    }

    private var shareTitle: String { sharing.shareButtonTitle == "Share Again" ? "Share Again…" : "Share…" }

    private var symbol: String {
        switch sharing.phase {
        case .active: return sharing.share?.media == .live ? "dot.radiowaves.left.and.right" : "link"
        case .degraded, .failed: return "exclamationmark.triangle"
        case .pending, .stopping: return "hourglass"
        case .ended: return "link.badge.plus"
        case .idle: return "link"
        }
    }
    private var tint: Color {
        switch sharing.phase {
        case .active: return sharing.share?.media == .live ? .green : .accentColor
        case .degraded, .failed: return .orange
        default: return .secondary
        }
    }
}

/// Link expiry and viewer limit change per link, so they live here rather
/// than in Settings. With the camera off, the button says it starts it.
struct ShareOptionsView: View {
    @ObservedObject var model: CameraModel
    @ObservedObject var sharing: SharingModel
    let dismiss: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("New share link").font(.headline)
            Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 8) {
                GridRow {
                    Text("Expires after").gridColumnAlignment(.trailing)
                    Picker("Link expires after", selection: $sharing.duration) {
                        Text("15 minutes").tag(SharingDuration.fifteenMinutes)
                        Text("1 hour").tag(SharingDuration.oneHour)
                        Text("8 hours").tag(SharingDuration.eightHours)
                    }.labelsHidden().fixedSize().accessibilityIdentifier("sharing-duration")
                }
                GridRow {
                    Text("Viewers at a time")
                    Picker("Viewers at a time", selection: $sharing.viewerLimit) {
                        ForEach(1...3, id: \.self) { Text("\($0)").tag($0) }
                    }.labelsHidden().fixedSize().accessibilityIdentifier("sharing-viewer-limit")
                }
            }.disabled(!sharing.canEditOptions)
            Text(sharing.preShareNotice).font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Label("Video only. Guests receive no audio.", systemImage: "video").font(.caption).foregroundStyle(.secondary)
            if let reason = blockedReason {
                Label(reason, systemImage: "info.circle").font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Spacer()
                Button("Cancel", action: dismiss).keyboardShortcut(.cancelAction)
                Button(createTitle) {
                    model.createShareLink()
                    dismiss()
                }
                .buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction).disabled(!canCreate)
                .accessibilityHint(live ? "Creates a new link and copies it." : "Starts the camera, then creates a new link and copies it.")
                .accessibilityIdentifier("share-camera")
            }
        }
        .padding(16)
        .frame(width: 340)
    }

    private var live: Bool { model.capture.phase == .streaming }
    private var createTitle: String {
        live || model.capture.phase == .starting ? "Create Link" : "Start Camera and Create Link"
    }
    private var canCreate: Bool { model.canCreateShareLink }
    private var blockedReason: String? {
        guard !canCreate else { return nil }
        if live { return sharing.shareBlockedReason }
        if model.capture.active { return "Wait for the camera to go live." }
        return model.startCameraBlockedReason ?? sharing.shareBlockedReason
    }
}

/// Start Call or Join… without a call, and the invitation while hosting.
/// The call itself is on the stage, with End Call under it.
struct CallCard: View {
    @ObservedObject var model: CameraModel

    var body: some View {
        ActivityCard(title: "Call", symbol: model.conversationActivity == nil ? "phone" : "phone.fill",
                     tint: model.conversationActivity == nil ? .secondary : .green,
                     state: model.callCardLine, stateIdentifier: "call-summary") {
            if let note = localNote { CardDetail(text: note) }
            if model.checkingConversationRoute {
                ProgressView("Checking the guest link…").controlSize(.small)
            }
            actions
            if model.hasConversationDiagnostics {
                Button("Copy Diagnostics", action: model.copyConversationDiagnostics).buttonStyle(.link)
                    .accessibilityHint("Copies the app version and setup reason.")
            }
        }
    }

    /// A call started with Test on This Mac can be joined only here.
    private var localNote: String? {
        guard let prejoin = model.conversationPrejoin, prejoin.admission.role == .host,
              prejoin.pageURL.scheme == "http" else { return nil }
        return "This call works only on this Mac. Open the invitation in a browser on this Mac."
    }

    @ViewBuilder private var actions: some View {
        if model.conversationPrejoin != nil {
            if model.canCopyConversation {
                Button("Copy Invitation", action: model.copyConversationInvitation)
                    .help("Copies the link your guest opens to join.")
                    .accessibilityIdentifier("copy-invitation")
            }
        } else if model.conversationsSupported, model.callStage == nil {
            // A call that is opening or ending has its controls on the stage.
            HStack(spacing: 8) {
                Button("Start Call", action: model.startCall)
                    .help(model.conversationBlockedReason ?? startHelp)
                    .accessibilityHint(model.conversationBlockedReason ?? startHelp)
                    .accessibilityIdentifier("start-call")
                Button("Join…") { model.showingJoin = true }
                    .help(model.conversationBlockedReason ?? "Paste an invitation link to join someone’s call.")
                    .accessibilityIdentifier("open-join-call")
            }
            .disabled(!model.canStartConversation)
        }
    }

    /// What Start Call does, from Settings → Calls.
    private var startHelp: String {
        let start: String
        switch model.callStart {
        case .cameraOff: start = "Starts a call with your camera and mic off."
        case .setup: start = "Starts a call that opens on your camera and mic setup."
        case .browser: start = "Starts a call with the host controls in your browser."
        }
        return model.canStop ? start + " It asks before stopping the stream." : start
    }
}

/// The console's one message: a problem with its fixes, or the last result.
struct BannerView: View {
    let banner: ConsoleBanner
    @ObservedObject var model: CameraModel

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Image(systemName: banner.problem ? "exclamationmark.triangle.fill" : "info.circle")
                .foregroundStyle(banner.problem ? Color.orange : Color.secondary).accessibilityHidden(true)
            message.fixedSize(horizontal: false, vertical: true).accessibilityIdentifier(identifier)
            Spacer(minLength: 8)
            ForEach(Array(banner.actions.enumerated()), id: \.offset) { index, action in
                if index == 0 {
                    Button(action.title) { model.perform(action) }.buttonStyle(.borderedProminent)
                } else {
                    Button(action.title) { model.perform(action) }
                }
            }
            if banner.dismissible {
                Button { model.dismissBanner() } label: { Image(systemName: "xmark") }
                    .buttonStyle(.borderless).help("Dismiss").accessibilityLabel("Dismiss")
            }
        }
        .padding(.horizontal, 12).padding(.vertical, 9)
        .background(RoundedRectangle(cornerRadius: 8).fill(banner.problem ? Color.orange.opacity(0.12) : Color.secondary.opacity(0.08)))
        .overlay(RoundedRectangle(cornerRadius: 8)
            .strokeBorder(banner.problem ? Color.orange.opacity(0.8) : Color.secondary.opacity(0.25),
                          style: StrokeStyle(lineWidth: 1, dash: banner.problem ? [5, 3] : [])))
        .accessibilityElement(children: .contain)
        .onAppear(perform: announce)
        .onChange(of: banner) { announce() }
    }

    private var identifier: String {
        switch banner.source {
        case .camera: return "camera-notice"
        case .sharing: return banner.problem ? "sharing-error" : "sharing-notice"
        case .call: return "conversation-status"
        }
    }

    /// A problem's name in bold, then its cause.
    private var message: Text {
        guard let title = banner.title else { return Text(banner.text) }
        return Text("\(Text(title + ".").bold()) \(banner.text)")
    }

    private func announce() {
        AccessibilityNotification.Announcement(banner.title.map { $0 + ". " + banner.text } ?? banner.text).post()
    }
}

/// State, elapsed time and any share link, in the toolbar and the menu-bar
/// popover. Shape and color both change with state, so it never relies on
/// color alone.
struct StatusPillView: View {
    @ObservedObject var model: CameraModel
    var identifier = "camera-status"
    /// The tooltip says what closing the window keeps running, so only the
    /// window's pill has it.
    var showsTooltip = true

    var body: some View {
        TimelineView(.periodic(from: .now, by: 1)) { context in
            let pill = model.statusPill
            let label = pill.label(at: context.date)
            HStack(spacing: 6) {
                Image(systemName: symbol(pill.tone)).imageScale(.small).foregroundStyle(color(pill.tone))
                Text(label).font(.callout.weight(.medium)).monospacedDigit().lineLimit(1)
            }
            .padding(.horizontal, 10).padding(.vertical, 3)
            .background(Capsule().fill(color(pill.tone).opacity(pill.tone == .neutral ? 0.12 : 0.18)))
            .help(showsTooltip ? tooltip : "")
            .accessibilityElement(children: .ignore)
            .accessibilityLabel("Status: " + label)
            .accessibilityIdentifier(identifier)
        }
    }

    /// The activity lines, plus what closing the window keeps running.
    private var tooltip: String {
        var lines = model.activity.lines
        if model.endConfirmation != nil {
            lines.append("Closing this window asks before the call ends.")
        } else if model.capture.active || model.sharing.showsHeaderStatus {
            lines.append("Closing this window keeps the server stream and share link running. Stop Camera or Quit ends them.")
        }
        return lines.joined(separator: "\n")
    }

    private func symbol(_ tone: StatusPill.Tone) -> String {
        switch tone {
        case .live: return "circle.fill"
        case .attention: return "exclamationmark.triangle.fill"
        case .neutral: return "circle"
        }
    }
    private func color(_ tone: StatusPill.Tone) -> Color {
        switch tone {
        case .live: return .green
        case .attention: return .orange
        case .neutral: return .secondary
        }
    }
}
