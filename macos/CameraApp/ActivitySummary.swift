import AppKit

/// What Camstream is doing now. The menu bar item, its menu, the app menus
/// and the window header read this one projection so they cannot disagree.
struct ActivitySummary: Equatable {
    enum Icon: String { case idle = "video", streaming = "video.fill", attention = "video.slash", call = "phone.fill" }

    /// Camera stream, for example "Camera live · Microphone live".
    let stream: String
    /// Share link while one exists, for example "Shared publicly · until 18:48 · 1 of 3 viewers".
    let share: String?
    /// Open conversation, for example "Hosting call · Connected · Camera on · Mic off".
    let call: String?
    let icon: Icon
    /// A share link is active. The status item shows a link badge, so the
    /// indicator does not depend on color.
    let shared: Bool

    var lines: [String] { [stream] + [share, call].compactMap { $0 } }
    var label: String { "Camstream Camera: " + lines.joined(separator: "; ") }
    var tooltip: String { "Camstream Camera\n" + lines.joined(separator: "\n") }

    /// Monochrome template image for the menu bar.
    var statusImage: NSImage? {
        let configuration = NSImage.SymbolConfiguration(pointSize: 15, weight: .regular)
        guard let base = NSImage(systemSymbolName: icon.rawValue, accessibilityDescription: nil)?
            .withSymbolConfiguration(configuration) else { return nil }
        guard shared, let badge = NSImage(systemSymbolName: "link", accessibilityDescription: nil)?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 8, weight: .bold)) else {
            base.isTemplate = true
            return base
        }
        let size = NSSize(width: base.size.width + badge.size.width * 0.8, height: max(base.size.height, 16))
        let image = NSImage(size: size, flipped: false) { rect in
            base.draw(at: NSPoint(x: 0, y: (rect.height - base.size.height) / 2), from: .zero, operation: .sourceOver, fraction: 1)
            let frame = NSRect(x: rect.width - badge.size.width, y: 0, width: badge.size.width, height: badge.size.height)
            // Clear a margin so the badge reads as a separate shape in one color.
            NSGraphicsContext.current?.compositingOperation = .clear
            NSBezierPath(ovalIn: frame.insetBy(dx: -1, dy: -1)).fill()
            NSGraphicsContext.current?.compositingOperation = .sourceOver
            badge.draw(in: frame)
            return true
        }
        image.isTemplate = true
        return image
    }
}

/// Text for confirming that a conversation ends or that the guest leaves.
/// Window close and the explicit menu actions share it.
struct EndConfirmation: Equatable {
    let title: String
    let detail: String
    let confirm: String
    let cancel: String

    /// The safe choice is first and answers Return; the end action is
    /// destructive and has no key equivalent.
    func alert() -> NSAlert {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = title
        alert.informativeText = detail
        alert.addButton(withTitle: cancel).keyEquivalent = "\r"
        let end = alert.addButton(withTitle: confirm)
        end.keyEquivalent = ""
        end.hasDestructiveAction = true
        return alert
    }

    /// Only the second button ends or leaves the conversation.
    static func confirmed(_ response: NSApplication.ModalResponse) -> Bool { response == .alertSecondButtonReturn }
}

extension CameraModel {
    var activity: ActivitySummary {
        let call = conversationActivity
        let icon: ActivitySummary.Icon = call != nil ? .call : capture.phase == .streaming ? .streaming :
            capture.phase == .error ? .attention : .idle
        return ActivitySummary(stream: streamLine, share: sharing.activityLine, call: call.map(Self.callLine),
                               icon: icon, shared: sharing.showsHeaderStatus)
    }

    /// Stream state with the selected devices while the stream runs. An
    /// automatic camera choice is not named because the helper does not
    /// report which device it opened.
    var streamLine: String {
        var camera = title
        if capture.active, let saved, saved.device != "auto", !saved.device.isEmpty { camera += " (\(saved.device))" }
        var microphone = microphoneSummary
        if capture.phase == .streaming, capture.audioState == "live", let saved, saved.audioEnabled,
           let device = microphones.first(where: { $0.matches(saved.audioDeviceID) }) {
            microphone += " (\(device.label(in: microphones)))"
        }
        return camera + " · " + microphone
    }

    static func callLine(_ call: ConversationActivity) -> String {
        let phase: String?
        switch call.phase {
        case .prejoin: phase = "Not joined yet"
        case .waiting: phase = call.role == .host ? "Waiting for guest" : "Waiting for host"
        case .connecting: phase = "Connecting"
        case .connected: phase = "Connected"
        case .reconnecting: phase = "Reconnecting"
        case .unknown: phase = nil
        }
        if call.hostedInBrowser { return (["Call hosted in browser"] + [phase].compactMap { $0 }).joined(separator: " · ") }
        var parts = [call.role == .host ? "Hosting call" : "In call"] + [phase].compactMap { $0 }
        if let camera = call.camera, let microphone = call.microphone {
            parts.append(camera ? "Camera on" + (call.cameraLabel.map { " (\($0))" } ?? "") : "Camera off")
            parts.append(microphone ? "Mic on" + (call.microphoneLabel.map { " (\($0))" } ?? "") : "Mic off")
        } else {
            parts.append("Camera and mic status unavailable")
        }
        return parts.joined(separator: " · ")
    }

    /// Nil when no conversation is open. Hosts are asked before the room
    /// ends for both people; guests before they leave.
    var endConfirmation: EndConfirmation? {
        guard let call = conversationActivity else { return nil }
        if call.role == .guest {
            return EndConfirmation(title: "Leave this conversation?", detail: "The host stays in the conversation.",
                                   confirm: "Leave", cancel: "Cancel")
        }
        let guestPresent = [.connecting, .connected, .reconnecting].contains(call.phase) ||
            browserRoom?.participants.contains { $0.role == .guest && $0.joined } == true
        return guestPresent
            ? EndConfirmation(title: "End this conversation for both of you?", detail: "Your guest will be disconnected.",
                              confirm: "End Conversation", cancel: "Keep Call")
            : EndConfirmation(title: "End this conversation?", detail: "The invitation will stop working.",
                              confirm: "End Conversation", cancel: "Keep Call")
    }
}

extension SharingModel {
    /// Who can watch and until when, while a link exists.
    var activityLine: String? {
        guard showsHeaderStatus else { return nil }
        switch phase {
        case .pending: return "Creating share link…"
        case .stopping: return "Stopping sharing…"
        default: break
        }
        var parts = [scopeLabel]
        if let deadline { parts.append("until \(timeText(deadline))") }
        if let share { parts.append("\(share.viewers) of \(share.viewerLimit) viewers") }
        if phase == .degraded { parts.append("Connection problem") }
        return parts.joined(separator: " · ")
    }
}
