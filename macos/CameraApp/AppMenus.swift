import AppKit

/// The standard Mac menus and the status item menu. Both follow the model's
/// activity projection; window work goes through the closures the app sets.
final class AppMenus: NSObject, NSMenuDelegate {
    let main = NSMenu()
    let status = NSMenu()
    let windowMenu = NSMenu(title: "Window")
    let helpMenu = NSMenu(title: "Help")
    /// Opens Settings at a pane, or where it was left.
    var openSettings: (SettingsPane?) -> Void = { _ in }
    /// Brings the main window forward; a call shows on its stage.
    var showWindow: () -> Void = {}
    var openHelp: () -> Void = {}

    private let model: CameraModel
    private let streamLine = AppMenus.info()
    private let shareLine = AppMenus.info()
    private let callLine = AppMenus.info()
    private var statusToggle: NSMenuItem!
    private var copyLinkItem: NSMenuItem!
    private var stopSharingItem: NSMenuItem!
    private var statusStartCall: NSMenuItem!
    private var statusJoinCall: NSMenuItem!
    private var statusEndCall: NSMenuItem!
    private var statusCopyInvitation: NSMenuItem!

    init(model: CameraModel) {
        self.model = model
        super.init()
        buildMain()
        buildStatus()
        update()
    }

    func menuWillOpen(_ menu: NSMenu) { model.refreshPermission(); update() }

    /// Status menu lines and visibility. Enabled state and titles come from
    /// menu validation, so key equivalents always see current state.
    func update() {
        let activity = model.activity
        streamLine.title = activity.stream
        shareLine.title = activity.share ?? ""
        shareLine.isHidden = activity.share == nil
        callLine.title = activity.call ?? ""
        callLine.isHidden = activity.call == nil
        copyLinkItem.isHidden = activity.share == nil
        stopSharingItem.isHidden = activity.share == nil
        let open = model.endConfirmation != nil
        statusStartCall.isHidden = open
        statusJoinCall.isHidden = open
        statusEndCall.isHidden = !open
        statusCopyInvitation.isHidden = !model.canCopyConversation
        for item in [statusToggle!, statusStartCall!, statusEndCall!] { _ = validateMenuItem(item) }
    }

    private func buildMain() {
        let app = submenu("Camstream Camera")
        app.addItem(item("About Camstream Camera", #selector(showAbout)))
        app.addItem(.separator())
        app.addItem(item("Settings…", #selector(showSettings), ","))
        app.addItem(.separator())
        app.addItem(withTitle: "Hide Camstream Camera", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        app.addItem(withTitle: "Hide Others", action: #selector(NSApplication.hideOtherApplications(_:)), keyEquivalent: "h")
            .keyEquivalentModifierMask = [.command, .option]
        app.addItem(withTitle: "Show All", action: #selector(NSApplication.unhideAllApplications(_:)), keyEquivalent: "")
        app.addItem(.separator())
        app.addItem(withTitle: "Quit Camstream Camera", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")

        let file = submenu("File")
        file.addItem(withTitle: "Close Window", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w")

        let edit = submenu("Edit")
        edit.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        edit.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "z").keyEquivalentModifierMask = [.command, .shift]
        edit.addItem(.separator())
        for (title, action, key) in [("Cut", "cut:", "x"), ("Copy", "copy:", "c"), ("Paste", "paste:", "v"), ("Select All", "selectAll:", "a")] {
            edit.addItem(withTitle: title, action: Selector(action), keyEquivalent: key)
        }

        let camera = submenu("Camera")
        let toggle = item("Start Camera", #selector(toggleCapture), "s")
        toggle.keyEquivalentModifierMask = [.command, .shift]
        for entry in [toggle, .separator(),
                      item("Export Connection Files", #selector(exportFiles)), item("Show Files in Finder", #selector(revealFiles))] {
            camera.addItem(entry)
        }

        let call = submenu("Call")
        for entry in [item("Start Call", #selector(startCall)), item("Join Call…", #selector(joinCall)), .separator(),
                      item("Copy Invitation", #selector(copyInvitation)), item("End Call…", #selector(endCall))] {
            call.addItem(entry)
        }

        let window = submenu("Window", menu: windowMenu)
        window.addItem(withTitle: "Minimize", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m")
        window.addItem(withTitle: "Zoom", action: #selector(NSWindow.performZoom(_:)), keyEquivalent: "")
        window.addItem(.separator())
        window.addItem(withTitle: "Bring All to Front", action: #selector(NSApplication.arrangeInFront(_:)), keyEquivalent: "")

        submenu("Help", menu: helpMenu).addItem(item("Camstream Camera Help", #selector(help)))
    }

    private func buildStatus() {
        status.delegate = self
        statusToggle = item("Start Camera", #selector(toggleCapture))
        copyLinkItem = item("Copy Share Link", #selector(copyShareLink))
        stopSharingItem = item("Stop Sharing", #selector(stopSharing))
        statusStartCall = item("Start Call", #selector(startCall))
        statusJoinCall = item("Join Call…", #selector(joinCall))
        statusEndCall = item("End Call…", #selector(endCall))
        statusCopyInvitation = item("Copy Invitation", #selector(copyInvitation))
        let entries: [NSMenuItem] = [
            streamLine, shareLine, callLine, .separator(),
            statusToggle, copyLinkItem, stopSharingItem, statusStartCall, statusJoinCall, statusCopyInvitation, statusEndCall, .separator(),
            item("Show Camstream Camera", #selector(showMainWindow)), item("Settings…", #selector(showSettings)), .separator(),
            NSMenuItem(title: "Quit Camstream Camera", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q"),
        ]
        entries.forEach(status.addItem)
    }

    @discardableResult
    private func submenu(_ title: String, menu given: NSMenu? = nil) -> NSMenu {
        let container = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        let menu = given ?? NSMenu(title: title)
        container.submenu = menu
        main.addItem(container)
        return menu
    }

    private func item(_ title: String, _ action: Selector, _ key: String = "", tag: Int = 0) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: action, keyEquivalent: key)
        item.target = self
        item.tag = tag
        return item
    }

    private static func info() -> NSMenuItem {
        let item = NSMenuItem(title: "", action: nil, keyEquivalent: "")
        item.isEnabled = false
        return item
    }

    @objc private func showAbout() {
        let buildID = Bundle.main.object(forInfoDictionaryKey: "CamstreamBuildID") as? String
        let options: [NSApplication.AboutPanelOptionKey: Any] = buildID.map {
            [.credits: NSAttributedString(string: "Build ID: \($0)")]
        } ?? [:]
        NSApp.orderFrontStandardAboutPanel(options: options)
    }

    @objc private func showSettings() { openSettings(nil) }
    @objc private func showMainWindow() { showWindow() }
    @objc private func toggleCapture() { if model.capture.active { model.stop() } else { model.startCamera() } }
    /// The export result and checklist show in Settings → Server.
    @objc private func exportFiles() {
        openSettings(.server)
        model.export()
    }
    @objc private func revealFiles() { model.revealExports() }
    @objc private func copyShareLink() { model.sharing.copyLink() }
    @objc private func stopSharing() { model.sharing.stopSharing() }
    /// The call takes the main window's stage, so the window comes forward.
    @objc private func startCall() {
        showWindow()
        model.startCall()
    }
    @objc private func joinCall() {
        showWindow()
        model.showingJoin = true
    }
    @objc private func copyInvitation() { model.copyConversationInvitation() }
    /// Asks first, on the main window.
    @objc private func endCall() { model.endCall() }
    @objc private func help() { openHelp() }
}

extension AppMenus: NSMenuItemValidation {
    func validateMenuItem(_ item: NSMenuItem) -> Bool {
        switch item.action {
        case #selector(toggleCapture):
            item.title = model.capture.active ? "Stop Camera" : "Start Camera"
            return model.capture.active ? model.canStop : model.canStartCamera
        case #selector(exportFiles): return model.canExport
        case #selector(revealFiles): return !model.exported.isEmpty
        case #selector(copyShareLink): return model.sharing.canCopyLink
        case #selector(stopSharing): return model.sharing.canStopSharing
        case #selector(startCall):
            // While the camera streams, Start Call asks before stopping it.
            item.title = model.canStop ? "Start Call…" : "Start Call"
            return model.canStartConversation
        case #selector(joinCall): return model.canStartConversation
        case #selector(copyInvitation): return model.canCopyConversation
        case #selector(endCall):
            item.title = model.conversationActivity?.role == .guest ? "Leave Call…" : "End Call…"
            return model.endConfirmation != nil
        default: return true
        }
    }
}
