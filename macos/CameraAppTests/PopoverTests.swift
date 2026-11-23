import AppKit

// The menu-bar popover: one preview attach shared with the window, the
// launch choice, and what the popover shows and does in each console state.
// Fixture helpers only; nothing here can open a sensor.
func runPopoverTests(conversations: [String: Any], sharing examples: [String: Any], check: (Bool, String) -> Void) {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    let waiting = conversations["waiting"] as! [String: Any]
    let page = "https://call.example.test/r/" + (waiting["room_id"] as! String)
    func admission(_ role: String) -> [String: Any] {
        ["result": ["page_url": page, "invitation_url": page + "#invitation-secret",
                    "admission": ["room": waiting, "role": role, "token": "fixture-grant"]]]
    }
    func live(_ model: CameraModel, _ status: (([String: Any]) -> Void)) {
        model.startCamera()
        status(["state": "streaming", "frames": 30])
        model.tick()
    }

    do { // The window and the popover share one preview attach.
        let helper = FixtureCameraHelper()
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
        defer { _ = model.quit() }
        func count(_ method: String) -> Int { helper.methods.filter { $0 == method }.count }
        model.launch()
        model.setPreviewSurface(.popover, visible: true)
        check(count("preview_attach") == 0, "the popover over a stopped camera attaches no preview")
        model.setPreviewSurface(.popover, visible: false)
        live(model) { helper.status = $0 }
        check(model.capture.phase == .streaming && count("preview_attach") == 0,
              "a live stream attaches no preview while neither the window nor the popover shows it")
        helper.hold = "preview_attach"
        model.setPreviewSurface(.popover, visible: true)
        check(count("preview_attach") == 1 && model.preview.phase == .connecting, "opening the popover attaches the preview")
        model.setPreviewSurface(.window, visible: true)
        model.setPreviewSurface(.popover, visible: false)
        check(count("preview_attach") == 1 && count("preview_detach") == 0 && model.preview.phase == .connecting,
              "moving from the popover to the window keeps the same attach")
        model.setPreviewSurface(.window, visible: false)
        helper.hold = nil
        helper.deliver(["result": ["version": 1, "socket": "/tmp/unused", "generation": "0123456789abcdef"]])
        check(count("preview_detach") == 1 && model.preview.phase == .idle && model.preview.image == nil,
              "with neither showing, the preview detaches")
        model.setWindowVisible(true)
        check(count("preview_attach") == 2, "the window alone attaches again")
        model.setWindowVisible(false)
        _ = model.quit()
        model.setPreviewSurface(.popover, visible: true)
        model.setPreviewSurface(.window, visible: true)
        check(count("preview_attach") == 2, "after Quit, neither surface attaches")
    }

    do { // A call on the stage pauses the preview, whichever surface shows.
        let helper = FixtureSharingCamera()
        helper.previewVersion = 1
        helper.conversationReply = admission("host")
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory, openBrowser: { _ in true })
        defer { _ = model.quit() }
        func attaches() -> Int { helper.methods.filter { $0 == "preview_attach" }.count }
        model.launch()
        live(model) { helper.status = $0 }
        model.setPreviewSurface(.popover, visible: true)
        check(attaches() == 1, "the popover attaches the preview of a live stream")
        // Opening the call: while streaming, the host controls wait for Stop Camera.
        model.startConversationInBrowser()
        let during = attaches()
        model.setPreviewSurface(.window, visible: true)
        model.setPreviewSurface(.popover, visible: false)
        model.setPreviewSurface(.popover, visible: true)
        check(model.callStage == .ready(host: true) && attaches() == during && model.preview.phase == .idle,
              "a call on the stage pauses the preview in the popover and the window")
        model.closeConversation()
        check(model.callStage == nil && attaches() == during + 1, "the preview resumes when the call ends")
    }

    do { // The launch choice: on by default and stored; the first-run guide still opens the window.
        let suite = "camstream-popover-tests-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = CameraModel(client: FixtureCameraHelper(), permissions: FixturePermissions(), directory: directory, defaults: defaults)
        defer { _ = model.quit() }
        check(model.opensWindowAtLaunch, "the window opens at launch by default")
        model.opensWindowAtLaunch = false
        let reopened = CameraModel(client: FixtureCameraHelper(), permissions: FixturePermissions(), directory: directory, defaults: defaults)
        defer { _ = reopened.quit() }
        var shown = 0
        reopened.showWindow = { shown += 1 }
        reopened.launch()
        check(!reopened.opensWindowAtLaunch && shown == 0, "turned off, the choice lasts, and saved settings open no window")

        let firstRun = FixtureCameraHelper()
        firstRun.configured = false
        let fresh = CameraModel(client: firstRun, permissions: FixturePermissions(), directory: directory, defaults: defaults)
        defer { _ = fresh.quit() }
        var guides = 0
        fresh.showWindow = { guides += 1 }
        fresh.launch()
        check(fresh.welcome == .camera && guides == 1, "with no settings saved, the first-run guide opens the window")
        firstRun.onReady?()
        check(guides == 1, "a camera service reconnect while the guide shows doesn't bring the window forward again")
    }

    do { // Content: the camera from service start to a problem.
        let helper = FixtureSharingCamera()
        let permissions = FixturePermissions()
        let model = CameraModel(client: helper, permissions: permissions, directory: directory, openBrowser: { _ in true })
        defer { _ = model.quit() }
        var content = model.popover
        check(content.picture == .progress("Starting the camera service…") && content.primary == nil &&
              content.pill.text == "Starting…", "while the camera service starts, the popover says so and offers no primary action")
        model.launch()
        content = model.popover
        check(content.picture == .message(symbol: "video.slash", title: "Camera off", detail: "Built-in camera · macbook", attention: false) &&
              content.primary == PopoverButton(action: .startCamera) && content.primaryNote == nil &&
              content.pill == model.statusPill && content.lines == model.activity.lines && content.banner == nil,
              "a stopped camera shows the stage's message, the pill, the activity lines and Start Camera")
        check(content.actions == [PopoverButton(action: .startCall(asks: false)), PopoverButton(action: .joinCall)] &&
              content.actions.map(\.action.title) == ["Start Call", "Join Call…"],
              "without a call, the popover offers Start Call and Join Call…")
        model.draft.path = "bad name!"
        content = model.popover
        check(content.primary == PopoverButton(action: .startCamera, enabled: false) && content.primaryNote == model.startCameraBlockedReason &&
              content.primaryNote != nil, "an invalid setting disables Start Camera and says why under it")
        model.draft.path = "macbook"
        model.startCamera()
        content = model.popover
        check(content.picture == .progress("Starting camera…") && content.primary == PopoverButton(action: .stopCamera),
              "a starting camera shows its progress, with Stop Camera")
        helper.status = ["state": "streaming", "frames": 30]
        model.tick()
        content = model.popover
        check(content.picture == .preview && content.primary == PopoverButton(action: .stopCamera) &&
              content.actions.first == PopoverButton(action: .startCall(asks: true)) && content.actions.first?.action.title == "Start Call…",
              "a live stream shows the preview with Stop Camera, and Start Call says it asks first")
        model.setPreviewSurface(.popover, visible: true)
        check(model.popover.picture == .previewUnavailable, "a camera service without previews says the preview is unavailable")
        model.setPreviewSurface(.popover, visible: false)
        helper.onEvent?(["event": "capture_stopped", "error": ["code": "capture_failed"]])
        content = model.popover
        guard case .message(_, let title, _, let attention) = content.picture else { check(false, "a camera problem draws a message"); return }
        check(title == "Camera needs attention" && attention && content.primary == PopoverButton(action: .fix(.startCamera)) &&
              content.primary?.action.title == "Try Again", "a camera problem shows its cause and its first fix")
        helper.onFailure?(.disconnected)
        helper.onExit?()
        content = model.popover
        check(content.primary == PopoverButton(action: .fix(.retryHelper)) && content.pill.text == "Needs attention" &&
              content.actions.allSatisfy { !$0.enabled }, "a lost camera service offers Reconnect, and the call actions wait for it")
    }

    do { // Content: camera access and the first-run guide.
        let permissions = FixturePermissions()
        permissions.video = "not_determined"
        let model = CameraModel(client: FixtureCameraHelper(), permissions: permissions, directory: directory)
        defer { _ = model.quit() }
        model.launch()
        check(model.popover.primary == PopoverButton(action: .fix(.allowCamera)) && model.popover.actions.isEmpty,
              "without camera access the popover offers Allow Camera, and a build without calls has no call actions")
        let firstRun = FixtureCameraHelper()
        firstRun.configured = false
        let fresh = CameraModel(client: firstRun, permissions: FixturePermissions(), directory: directory)
        defer { _ = fresh.quit() }
        fresh.launch()
        check(fresh.popover.primary == PopoverButton(action: .finishSetup) && PopoverAction.finishSetup.handoff == .window,
              "while the first-run guide shows, Finish Setup opens the window")
    }

    do { // Content: calls show their state, Show Call and their ending, never video.
        let helper = FixtureSharingCamera()
        helper.conversationReply = admission("host")
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory, openBrowser: { _ in true })
        defer { _ = model.quit() }
        model.launch()
        helper.heldConversationMethods = [.create]
        model.startConversation()
        var content = model.popover
        check(content.picture == .progress(model.conversationProgress?.message ?? "Opening the invitation…") &&
              content.primary == PopoverButton(action: .showCall) && content.actions.last == PopoverButton(action: .cancelCall),
              "a call that is opening shows what it waits for, with Show Call and Cancel Call")
        model.perform(popover: .cancelCall)
        content = model.popover
        check(model.callStage == .ending && content.picture == .progress("Ending the call…") &&
              content.actions.last == PopoverButton(action: .endCall(guest: false), enabled: false),
              "Cancel Call ends the opening call, and the popover waits for the camera service to answer")
        helper.heldConversationMethods = []
        helper.pendingConversation.removeValue(forKey: .create)?(admission("host"))
        let endDeadline = Date().addingTimeInterval(1)
        while model.callStage != nil && Date() < endDeadline { RunLoop.current.run(until: Date().addingTimeInterval(0.01)) }
        check(model.callStage == nil && model.popover.primary == PopoverButton(action: .startCamera) &&
              helper.methods.filter { $0 == "conversation_end" }.count == 1,
              "a room that opens after Cancel Call is ended, and the popover returns to the camera")
        model.startConversation()
        content = model.popover
        check(model.stage == .call(.page) &&
              content.picture == .message(symbol: "phone.fill", title: "In a call", detail: "Call video shows in the Camstream window.", attention: false) &&
              content.primary == PopoverButton(action: .showCall) && content.actions.last == PopoverButton(action: .endCall(guest: false)) &&
              content.actions.last?.action.title == "End Call…" &&
              !content.actions.contains { if case .startCall = $0.action { return true }; return $0.action == .joinCall },
              "an open call shows its state with Show Call and End Call…, and no video")
        var confirmations: [Confirmation] = []
        model.confirm = { confirmation, _ in confirmations.append(confirmation) }
        model.perform(popover: .endCall(guest: false))
        check(confirmations.count == 1 && PopoverAction.endCall(guest: false).handoff == .window && model.callStage != nil,
              "End Call… opens the window and asks there first")
        model.closeConversation()

        helper.conversationReply = admission("guest")
        model.openConversationInvitation("camstream://conversation/join?v=1#fixture")
        check(model.popover.actions.last == PopoverButton(action: .endCall(guest: true)) &&
              model.popover.actions.last?.action.title == "Leave Call…", "a guest leaves the call instead")
        model.closeConversation()

        helper.conversationReply = ["error": ["code": "transport"]]
        model.startConversation()
        let banner = model.popover.banner
        check(banner != nil && banner?.text == model.banner?.text && model.banner!.actions.count >= 1 &&
              banner?.actions == [model.banner!.actions[0]], "the popover shows the console's message with only its first fix")
        model.perform(popover: .dismissBanner)
        check(model.banner?.source != .call, "Dismiss clears the message")
    }

    do { // Content: the share link's actions, and what each action leaves open.
        var created = examples["created"] as! [String: Any]
        var share = created["share"] as! [String: Any]
        share["expires_at"] = ISO8601DateFormatter().string(from: Date().addingTimeInterval(3600))
        created["share"] = share
        let helper = FixtureSharingCamera()
        helper.sharing.replies[.setup] = examples["native_setup_reply"] as? [String: Any]
        helper.sharing.replies[.status] = ["result": ["camera_id": "local-camera", "state": "stopped", "media": "idle", "viewers": 0, "viewer_limit": 0]]
        helper.sharing.replies[.create] = ["result": created]
        helper.sharing.replies[.copyLink] = ["result": ["url": "https://view.example.test/s/\(share["share_id"] as! String)#fixture-token"]]
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
        model.sharing.writeLink = { _ in }
        defer { _ = model.quit() }
        model.launch()
        live(model) { helper.status = $0 }
        model.createShareLink()
        let content = model.popover
        check(content.actions.prefix(2).map(\.action) == [.copyShareLink, .stopSharing] && content.actions.prefix(2).allSatisfy(\.enabled) &&
              content.lines.count == 2 && content.lines[1] == model.activity.share,
              "a share link adds Copy Share Link and Stop Sharing, and its line")
        model.perform(popover: .stopSharing)
        check(helper.sharing.methods.contains(.revoke), "Stop Sharing revokes the link from the popover")

        var panes: [SettingsPane?] = []
        model.showSettings = { panes.append($0) }
        model.perform(popover: .settings)
        model.perform(popover: .joinCall)
        check(panes.count == 1 && panes[0] == nil && model.showingJoin, "Settings… opens Settings, and Join Call… opens its sheet")
        let stays: [PopoverAction] = [.startCamera, .stopCamera, .retryPreview, .copyShareLink, .stopSharing, .copyInvitation, .cancelCall,
                                      .dismissBanner, .fix(.allowCamera), .fix(.retryHelper)]
        let windows: [PopoverAction] = [.finishSetup, .showCall, .startCall(asks: true), .startCall(asks: false), .joinCall,
                                        .endCall(guest: false), .openWindow, .fix(.startNewConversation), .fix(.testLocalConversation),
                                        .fix(.retryConversationInvitation)]
        let closes: [PopoverAction] = [.settings, .quit, .fix(.chooseWebcam), .fix(.openCameraSettings), .fix(.openSetupGuide)]
        check(stays.allSatisfy { $0.handoff == .stay } && windows.allSatisfy { $0.handoff == .window } && closes.allSatisfy { $0.handoff == .close },
              "camera and share actions keep the popover open; sheets, confirmations and calls go to the window; other places close it")
        let kinds: [PopoverAction] = [.startCamera, .stopCamera, .finishSetup, .showCall, .retryPreview, .fix(.allowCamera),
                                      .copyShareLink, .stopSharing, .startCall(asks: false), .joinCall, .copyInvitation, .cancelCall,
                                      .endCall(guest: false), .dismissBanner, .openWindow, .settings, .quit]
        check(Set(kinds.map(\.identifier)).count == kinds.count &&
              (stays + windows + closes).allSatisfy { $0.identifier.hasPrefix("popover-") && !$0.title.isEmpty },
              "every popover action has a title, and each kind its own identifier")
    }
}
