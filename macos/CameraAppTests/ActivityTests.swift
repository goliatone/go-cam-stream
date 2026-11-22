import AppKit

// One activity projection for the menu bar, app menus and window header, plus
// the confirmation that guards ending a call. Fixture helpers only.
func runActivityTests(conversations: [String: Any], sharing: [String: Any], check: (Bool, String) -> Void) {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    let waiting = conversations["waiting"] as! [String: Any]
    let room = waiting["room_id"] as! String
    let page = "https://call.example.test/r/" + room
    func prejoin(_ role: String) -> [String: Any] {
        ["result": ["page_url": page, "invitation_url": page + "#invitation-secret",
                    "admission": ["room": waiting, "role": role, "token": "fixture-grant"]]]
    }
    func launched(_ prepare: (FixtureSharingCamera) -> Void = { _ in }) -> (CameraModel, FixtureSharingCamera) {
        let helper = FixtureSharingCamera()
        prepare(helper)
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory, openBrowser: { _ in true })
        model.launch()
        return (model, helper)
    }
    // Start through the model, then let a status poll report the live stream.
    func stream(_ model: CameraModel, _ helper: FixtureSharingCamera) {
        model.start()
        helper.status = ["state": "streaming", "frames": 30]
        model.tick()
    }

    do { // Stream states and their icons.
        let (model, helper) = launched()
        check(model.activity == ActivitySummary(stream: "Camera stopped · Microphone off", share: nil, call: nil, icon: .idle, shared: false) &&
              model.activity.label == "Camstream Camera: Camera stopped · Microphone off" && model.activity.statusImage?.isTemplate == true,
              "an idle app reports one stopped stream line with the outlined icon")
        check(model.endConfirmation == nil, "nothing asks for confirmation without a conversation")
        model.draft.device = "Studio Display Camera"
        model.saved = model.config
        stream(model, helper)
        check(model.activity.stream == "Camera live (Studio Display Camera) · Microphone off" && model.activity.icon == .streaming,
              "a live stream names its selected camera and fills the icon")
        model.capture.failed("Camera failed.", released: true)
        check(model.activity.stream.hasPrefix("Camera needs attention") && model.activity.icon == .attention,
              "a stream error has its own icon shape")
    }

    do { // A public share link adds a line and a badge that does not depend on color.
        var created = sharing["created"] as! [String: Any]
        var share = created["share"] as! [String: Any]
        share["expires_at"] = ISO8601DateFormatter().string(from: Date().addingTimeInterval(3600))
        created["share"] = share
        let (model, helper) = launched { helper in
            helper.sharing.replies[.setup] = sharing["native_setup_reply"] as? [String: Any]
            helper.sharing.replies[.status] = ["result": ["camera_id": "local-camera", "state": "stopped", "media": "idle", "viewers": 0, "viewer_limit": 0]]
            helper.sharing.replies[.create] = ["result": created]
        }
        stream(model, helper)
        model.sharing.shareCamera()
        let activity = model.activity
        check(activity.share?.hasPrefix("Shared publicly · until ") == true && activity.share?.hasSuffix(" · 0 of 1 viewers") == true &&
              activity.shared && activity.lines.count == 2 && activity.label.contains("; Shared publicly"),
              "an active public link reads as public with its end time and viewers")
        let plain = ActivitySummary(stream: activity.stream, share: nil, call: nil, icon: activity.icon, shared: false).statusImage
        check(activity.statusImage?.isTemplate == true && plain != nil && activity.statusImage!.size.width > plain!.size.width,
              "the shared icon adds a monochrome link badge")
    }

    do { // Host calls: activity, confirmation and teardown that keeps the stream.
        let (model, helper) = launched()
        stream(model, helper)
        helper.conversationReply = prejoin("host")
        model.startConversation()
        guard let surface = model.conversationSurface else { check(false, "a host call opens"); return }
        check(model.activity.call == "Hosting call · Camera off · Mic off" && model.activity.icon == .call &&
              model.activity.lines.count == 2, "an open host call adds a call line beside the live stream")
        check(model.endConfirmation == EndConfirmation(title: "End this conversation?", detail: "The invitation will stop working.",
                                                       confirm: "End Conversation", cancel: "Keep Call"),
              "a host without a guest is asked before the invitation stops working")
        let alert = model.endConfirmation!.alert()
        check(alert.buttons.map(\.title) == ["Keep Call", "End Conversation"] && alert.buttons[0].keyEquivalent == "\r" &&
              alert.buttons[1].keyEquivalent.isEmpty && alert.buttons[1].hasDestructiveAction && !alert.buttons[0].hasDestructiveAction &&
              alert.messageText == "End this conversation?" && alert.alertStyle == .warning,
              "Keep Call is the Return default and End is the destructive second choice")
        check(!EndConfirmation.confirmed(.alertFirstButtonReturn) && !EndConfirmation.confirmed(.cancel) &&
              !EndConfirmation.confirmed(.abort) && EndConfirmation.confirmed(.alertSecondButtonReturn),
              "only the explicit End or Leave button ends the call")
        _ = model.reportConversationState(ConversationCallState(role: .host, phase: .connected, camera: true, microphone: false,
                                                                cameraLabel: "FaceTime HD Camera"), from: surface)
        check(model.activity.call == "Hosting call · Connected · Camera off · Mic off",
              "a page report without the device lease keeps inputs off")
        model.stop()
        guard let lease = model.claimConversationCapture(stop: {}) else { check(false, "a stopped stream frees the devices"); return }
        check(model.activity.call == "Hosting call · Connected · Camera on (FaceTime HD Camera) · Mic off",
              "a leased call names its live camera")
        check(model.endConfirmation?.title == "End this conversation for both of you?" &&
              model.endConfirmation?.detail == "Your guest will be disconnected.",
              "a host with a guest is asked before ending it for both")
        model.releaseConversationCapture(lease)
        stream(model, helper)
        let stops = helper.methods.filter { $0 == "stop" }.count
        model.closeConversation()
        model.closeConversation()
        check(helper.methods.filter { $0 == "conversation_end" }.count == 1 && helper.methods.filter { $0 == "stop" }.count == stops &&
              model.capture.phase == .streaming && model.activity.call == nil && model.endConfirmation == nil,
              "confirmed close ends the room once and keeps the camera stream running")
    }

    do { // Guests, unknown device state and browser hosting.
        let (model, helper) = launched()
        helper.conversationReply = prejoin("guest")
        model.openConversationInvitation("camstream://conversation/join?v=1#fixture")
        check(model.activity.call == "In call · Camera off · Mic off" &&
              model.endConfirmation == EndConfirmation(title: "Leave this conversation?", detail: "The host stays in the conversation.",
                                                       confirm: "Leave", cancel: "Cancel"),
              "a guest is asked before leaving, with Cancel as the safe choice")
        var lease: UUID?
        model.requestConversationCapture(stop: {}) { lease = $0 }
        check(lease != nil && model.activity.call == "In call · Camera and mic status unavailable",
              "a leased call without a page report says its device state is unavailable")
        model.releaseConversationCapture(lease!)
        model.closeConversation()
        check(helper.methods.filter { $0 == "conversation_leave" }.count == 1 && model.activity.call == nil, "leaving releases the guest seat once")

        helper.conversationReply = prejoin("host")
        model.startConversationInBrowser()
        check(model.activity.call == "Call hosted in browser" && model.endConfirmation?.title == "End this conversation?",
              "a browser-hosted room is labeled without claiming local inputs")
        helper.conversationReply = ["result": conversations["connected"]!]
        model.tick()
        check(model.activity.call == "Call hosted in browser · Connected" &&
              model.endConfirmation?.title == "End this conversation for both of you?",
              "browser hosting follows the owner's room and guest")
        model.closeConversation()
    }

    do { // Standard menus, key equivalents and current validation.
        let (model, helper) = launched()
        let menus = AppMenus(model: model)
        var shown: [CameraModel.Screen] = []
        var panes: [SettingsPane?] = []
        var ends = 0
        menus.show = { shown.append($0) }
        menus.openSettings = { panes.append($0) }
        menus.confirmEnd = { ends += 1 }
        check(menus.main.items.map(\.title) == ["Camstream Camera", "File", "Edit", "View", "Camera", "Call", "Window", "Help"],
              "the app has standard Mac menus plus Camera and Call")
        func find(_ title: String) -> NSMenuItem? {
            for menu in menus.main.items.compactMap(\.submenu) {
                if let item = menu.items.first(where: { $0.title == title }) { return item }
            }
            return nil
        }
        let expected: [(String, String, NSEvent.ModifierFlags)] = [
            ("Settings…", ",", .command), ("Hide Camstream Camera", "h", .command), ("Hide Others", "h", [.command, .option]),
            ("Quit Camstream Camera", "q", .command), ("Close Window", "w", .command), ("Undo", "z", .command),
            ("Redo", "z", [.command, .shift]), ("Camera", "1", .command), ("Call", "2", .command),
            ("Start Camera", "s", [.command, .shift]), ("Minimize", "m", .command),
        ]
        for (title, key, modifiers) in expected {
            let item = find(title)
            check(item?.keyEquivalent == key && item?.keyEquivalentModifierMask == modifiers, "\(title) shows its key equivalent")
        }
        check(find("Close Window")?.action == #selector(NSWindow.performClose(_:)) &&
              find("Minimize")?.action == #selector(NSWindow.performMiniaturize(_:)) &&
              find("Zoom") != nil && find("Bring All to Front") != nil && find("About Camstream Camera") != nil &&
              find("Show All") != nil && find("Camstream Camera Help") != nil,
              "Close, Minimize, Zoom, Hide, About and Help use the standard actions")
        let shortcuts = menus.main.items.compactMap(\.submenu).flatMap(\.items).filter { !$0.keyEquivalent.isEmpty }
            .map { "\($0.keyEquivalentModifierMask.rawValue)-\($0.keyEquivalent)" }
        check(Set(shortcuts).count == shortcuts.count, "no two commands share a key equivalent")

        let start = find("Start Camera")!
        check(menus.validateMenuItem(start) && find("Save Settings") == nil, "Start is available, with no separate Save command")
        model.draft.path = "edited camera"
        check(!menus.validateMenuItem(start), "menu validation follows an invalid edit without a model notification")
        model.draft.path = "edited-camera"
        check(menus.validateMenuItem(start), "a valid edit that isn't applied yet keeps Start available; it applies first")
        model.draft.path = model.saved!.path
        (find("Settings…")!.target as! NSObject).perform(find("Settings…")!.action, with: nil)
        check(panes == [nil], "Settings… opens the Settings window where it was left")
        stream(model, helper)
        check(menus.validateMenuItem(start) && start.title == "Stop Camera", "the same command stops a live stream")
        (start.target as! NSObject).perform(start.action, with: start)
        check(model.capture.phase == .stopping || model.capture.phase == .stopped, "the menu command stops the stream")
        for entry in AppMenus.screens {
            let item = find(entry.title)!
            (item.target as! NSObject).perform(item.action, with: item)
        }
        check(shown == [.camera, .call], "View commands show the camera console and the call")

        let startCall = find("Start Conversation")!, showCall = find("Show Call")!, endCall = find("End Conversation…")!
        check(menus.validateMenuItem(startCall) && !menus.validateMenuItem(showCall) && !menus.validateMenuItem(endCall),
              "without a call, the Call menu offers Start Conversation")
        helper.conversationReply = prejoin("host")
        (startCall.target as! NSObject).perform(startCall.action, with: startCall)
        check(model.conversationSurface != nil && shown.last == .call && !menus.validateMenuItem(startCall) &&
              menus.validateMenuItem(showCall) && menus.validateMenuItem(endCall) && endCall.title == "End Conversation…",
              "Start Conversation opens the call; Show Call and End become available")
        (endCall.target as! NSObject).perform(endCall.action, with: endCall)
        check(ends == 1 && model.conversationSurface != nil, "End asks for confirmation instead of ending at once")

        menus.update()
        let visible = menus.status.items.filter { !$0.isHidden && !$0.isSeparatorItem }.map(\.title)
        check(visible.prefix(2) == ["Camera stopped · Microphone off", "Hosting call · Camera off · Mic off"] &&
              visible.contains("Show Call") && visible.contains("End Conversation…") && !visible.contains("Start Conversation") &&
              !visible.contains("Copy Share Link"), "the status menu shows the call line and its actions")
        model.closeConversation()
        helper.conversationReply = prejoin("guest")
        model.openConversationInvitation("camstream://conversation/join?v=1#guest")
        menus.update()
        check(menus.validateMenuItem(endCall) && endCall.title == "Leave Conversation…" &&
              menus.status.items.contains { !$0.isHidden && $0.title == "Leave Conversation…" },
              "guests see Leave Conversation in both menus")
        model.closeConversation()
        menus.update()
        check(menus.status.items.contains { !$0.isHidden && $0.title == "Start Conversation" } &&
              !menus.status.items.contains { !$0.isHidden && $0.title == "Show Call" }, "the status menu can start a call again")
        let statusTitles = menus.status.items.map(\.title)
        check(statusTitles.contains("Show Camstream Camera") && statusTitles.contains("Settings…") &&
              !statusTitles.contains("Sharing…") && !statusTitles.contains("Connection Details…"),
              "the status menu opens the window and Settings instead of tabs")
        let export = find("Export Connection Files")!
        (export.target as! NSObject).perform(export.action, with: export)
        check(panes.last == .server, "Export Connection Files opens Settings → Server, where its result shows")
    }
}
