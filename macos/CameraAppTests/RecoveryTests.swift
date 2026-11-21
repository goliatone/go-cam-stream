import Foundation

// First-run reasons, locked settings and recovery actions for the camera and
// calls. Fixture helpers only: no device, subprocess or permission prompt.
func runRecoveryTests(conversations: [String: Any], check: (Bool, String) -> Void) {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    func failedEvent(_ code: String) -> [String: Any] {
        ["version": 1, "event": "capture_stopped", "result": ["state": "failed"], "error": ["code": code]]
    }

    do { // First launch: permission, then Save, then Start, each named while it blocks.
        let permissions = FixturePermissions()
        permissions.video = "not_determined"
        let helper = FixtureCameraHelper()
        helper.configured = false
        let model = CameraModel(client: helper, permissions: permissions, directory: directory)
        check(model.startBlockedReason == "Waiting for the camera helper…" && model.helperBlockedReason == model.startBlockedReason,
              "before the helper is ready, Start names the helper")
        model.launch()
        check(model.saved == nil && !model.canStart && model.startBlockedReason == "Allow camera access first." &&
              model.saveBlockedReason == "Allow camera access first." && model.cameraFeedback == nil,
              "a first launch names camera access as the first step for Start and Save")
        model.requestPermission()
        check(model.startBlockedReason == "Respond to the permission request first.", "an open permission request is named")
        permissions.video = "authorized"
        permissions.resolveAudio(permissions.audio)
        check(model.permission == "authorized" && model.startBlockedReason == "Save your settings first." &&
              model.canSave && model.saveBlockedReason == nil,
              "after access is allowed, Start asks for the first Save and Save is available")
        model.save()
        check(model.saved != nil && model.canStart && model.startBlockedReason == nil, "saved settings enable Start")
        let port = model.portText
        model.portText = "80"
        check(model.startBlockedReason == "Choose a port from 1024 to 65535." && model.saveBlockedReason == model.startBlockedReason && !model.canSave,
              "an invalid edit blocks Save and Start with the same reason")
        model.portText = "8555"
        check(model.startBlockedReason == "Save your changes first." && model.canSave && model.saveBlockedReason == nil,
              "a valid unsaved edit asks for Save before Start")
        model.portText = port
        model.working = true
        check(model.startBlockedReason == "Wait for the current change to finish." && model.settingsLockReason == nil,
              "a pending helper change is named without locking settings")
        model.working = false

        model.start()
        model.capture.status("streaming", frames: 12)
        check(model.settingsLockReason == "Settings are locked while the camera is on. Stop Camera to change them." &&
              model.startBlockedReason == nil && !model.canEditSettings,
              "settings say why they are locked while the camera is on")
        model.portText = "8556"
        check(model.saveBlockedReason == model.settingsLockReason, "Save repeats the lock reason while the camera is on")
        model.portText = port

        helper.onEvent?(failedEvent("capture_failed"))
        let feedback = model.cameraFeedback
        check(model.capture.problem == "The camera stopped. Close other apps using the webcam, then try again." &&
              feedback?.title == "Camera needs attention" && feedback?.actions == [.startCamera, .refreshCameras] &&
              feedback?.message.contains("permission") == false,
              "a capture failure with access allowed offers Try Again and Refresh Cameras without permission advice")
        check(feedback?.announcement == "Camera needs attention. " + (model.capture.problem ?? ""), "the camera problem is announced in full")
        let starts = helper.methods.filter { $0 == "start" }.count
        model.perform(.startCamera)
        check(helper.methods.filter { $0 == "start" }.count == starts + 1, "Try Again starts the saved stream through the normal action")
        model.capture.status("streaming", frames: 12)

        permissions.video = "denied"
        model.refreshPermission()
        helper.onEvent?(failedEvent("capture_failed"))
        check(model.cameraFeedback?.actions == [.openCameraSettings] &&
              model.startBlockedReason == "Turn on camera access in System Settings first.",
              "after access is turned off, recovery opens Camera settings and Start names it")
        permissions.video = "restricted"
        model.refreshPermission()
        check(model.startBlockedReason == "Camera access is restricted on this Mac.", "restricted access is named")
        permissions.video = "authorized"
        model.refreshPermission()
        check(model.problemText("device_unavailable").hasPrefix("No available webcam was found.") &&
              model.problemText("configuration_failed").hasPrefix("Settings could not be saved."),
              "device and settings failures name their own recovery")
        _ = model.quit()
    }

    do { // Helper loss blocks every action with one reason and a single recovery.
        let helper = FixtureCameraHelper()
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
        model.launch()
        model.start()
        model.capture.status("streaming", frames: 12)
        helper.onFailure?()
        check(model.cameraFeedback?.title == "Camera helper stopped" && model.cameraFeedback?.actions == [.quit],
              "a lost helper offers only Quit")
        helper.onExit?()
        check(model.startBlockedReason == "The camera helper stopped. Quit and reopen Camstream Camera." &&
              model.conversationBlockedReason == model.startBlockedReason,
              "an exited helper is named for Start and for calls")
    }

    let waiting = conversations["waiting"] as! [String: Any]
    let room = waiting["room_id"] as! String
    let page = "https://call.example.test/r/" + room
    func prejoin(_ role: String) -> [String: Any] {
        ["result": ["page_url": page, "invitation_url": page + "#invitation-secret",
                    "admission": ["room": waiting, "role": role, "token": "fixture-grant"]]]
    }
    func setupFailure(_ setup: String?) -> [String: Any] {
        var fault: [String: Any] = ["code": "media_unreachable", "retryable": false]
        if let setup { fault["setup"] = setup }
        return ["error": fault]
    }
    var opened: [URL] = []
    func launched(_ prepare: (FixtureSharingCamera) -> Void = { _ in }) -> (CameraModel, FixtureSharingCamera) {
        let helper = FixtureSharingCamera()
        prepare(helper)
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory,
                                openBrowser: { opened.append($0); return true })
        model.writeClipboard = { _ in }
        model.launch()
        return (model, helper)
    }
    let invitation = "camstream://conversation/join?v=1#fixture"

    do { // A build without calls says so once, with no action that cannot work.
        let (model, _) = launched { $0.conversationVersion = 0 }
        let feedback = model.conversationFeedback
        check(feedback?.title == "Calls aren't available in this build" && feedback?.tone == .unavailable &&
              feedback?.actions == [] && !model.canStartConversation &&
              model.conversationBlockedReason == "This Camstream Camera helper can't host or join conversations.",
              "an unsupported build is distinguished from missing setup")
    }

    do { // Missing, invalid and unavailable call setup, for hosts and guests.
        let (model, helper) = launched()
        helper.conversationReply = conversations["setup_missing"] as! [String: Any]
        model.startConversation()
        var feedback = model.conversationFeedback
        check(model.conversationSetup == .notConfigured && feedback?.title == "Calls aren't set up on this Mac" &&
              feedback?.message == "Add the call settings described in the setup guide, then try again." &&
              feedback?.actions == [.openSetupGuide, .showSettingsFolder],
              "a host without call setup is told so with the guide and the settings folder")
        model.perform(.openSetupGuide)
        check(opened.last == CameraModel.setupGuide && CameraModel.setupGuide.fragment == "conversations",
              "Open Setup Guide opens the conversation setup section")

        helper.conversationReply = setupFailure("not_configured")
        model.openConversationInvitation(invitation)
        feedback = model.conversationFeedback
        check(feedback?.title == "Joining isn't set up on this Mac" && feedback?.message.contains("trust the host's server") == true,
              "a guest without call setup is told how to join")

        helper.conversationReply = setupFailure("invalid_configuration")
        model.startConversation()
        check(model.conversationFeedback?.title == "Call settings need attention" &&
              model.conversationFeedback?.actions == [.openSetupGuide, .showSettingsFolder],
              "invalid call settings point to the guide and settings folder")

        helper.conversationReply = setupFailure("runtime_unavailable")
        model.startConversation()
        feedback = model.conversationFeedback
        check(feedback?.title == "Calls couldn't start" && feedback?.actions == [.retryConversation, .openSetupGuide],
              "an unavailable call runtime offers Try Again before the guide")
        let creates = helper.methods.filter { $0 == "conversation_create" }.count
        helper.conversationReply = setupFailure("start_failed")
        model.perform(.retryConversation)
        check(helper.methods.filter { $0 == "conversation_create" }.count == creates + 1 &&
              model.conversationFeedback?.title == "Calls couldn't start",
              "Try Again starts the conversation through the normal action")

        helper.conversationReply = ["error": ["code": "media_unreachable", "setup": "listener_in_use", "port": 18894]]
        model.startConversation()
        check(model.conversationFeedback?.title == "Call port 18894 is already in use" &&
              model.conversationFeedback?.message.contains("stop the test") == true &&
              model.conversationFeedback?.actions.first == .retryConversation && !model.canCopyConversation,
              "a port conflict names the port and gives an action without exposing an invitation")

        helper.conversationReply = setupFailure("public_route_unreachable")
        model.perform(.retryConversation)
        check(model.conversationFeedback?.title == "Guests can't reach this Mac" && model.conversationSetupPort == nil &&
              model.conversationFeedback?.actions == [.retryConversation, .testLocalConversation] && !model.canCopyConversation,
              "a failed route check explains the tunnel and keeps the invitation unavailable")
        var diagnostics = ""
        model.writeClipboard = { diagnostics = $0 }
        model.copyConversationDiagnostics()
        check(diagnostics.contains("public_route_unreachable") && !diagnostics.contains("18894") && !diagnostics.contains("https://"),
              "call diagnostics include the current reason and omit endpoint and stale port data")
        model.perform(.testLocalConversation)
        check(helper.methods.last == "conversation_create_local", "the local action requests a separate loopback room")

        helper.conversationReply = setupFailure("relay_unavailable")
        model.startConversationInBrowser()
        check(model.conversationFeedback?.actions == [.retryRelay, .showSettingsFolder], "an unavailable relay offers a useful retry and settings")
        helper.conversationReply = prejoin("host")
        model.perform(.retryRelay)
        check(model.hostInBrowser, "a retry preserves the host in browser start mode")
        helper.conversationReply = setupFailure("public_route_unreachable")
        model.perform(.checkConversationRoute)
        check(!model.canCopyConversation && model.conversationRouteFeedback?.actions.first == .checkConversationRoute,
              "an unavailable public route suspends copying and offers a connection check")
        helper.conversationReply = ["result": true]
        model.perform(.checkConversationRoute)
        check(model.canCopyConversation && model.conversationRouteFeedback == nil,
              "a successful check restores invitation copy")
    }

    do { // Older helpers without a setup cause: neutral, role-specific recovery.
        let (model, helper) = launched()
        helper.conversationReply = setupFailure(nil)
        model.startConversation()
        let feedback = model.conversationFeedback
        check(model.conversationSetup == nil && feedback?.title == "Calls couldn't start" &&
              feedback?.message == "Try again, or check the call setup guide." && feedback?.actions == [.retryConversation, .openSetupGuide],
              "a host start failure without a cause offers Try Again, never a stopped call")
        helper.conversationReply = ["error": ["code": "room_full", "retryable": true]]
        model.openConversationInvitation(invitation)
        check(model.conversationFeedback?.title == "Couldn't join the conversation" &&
              model.conversationFeedback?.message.contains("already has a guest") == true && model.conversationFeedback?.actions == [],
              "a guest who cannot join is told why without a host action")
    }

    do { // New helpers report each setup stage while a start is pending.
        let (model, helper) = launched()
        helper.heldConversationMethods.insert(.create)
        model.startConversation()
        check(model.conversationProgress == .listener && !model.canCopyConversation, "start begins with the local listener check")
        helper.onEvent?(["event": "conversation_setup_progress", "result": "public_route"])
        check(model.conversationProgress == .publicRoute, "the helper advances the public link progress")
        helper.onEvent?(["event": "conversation_setup_progress", "result": "relay"])
        check(model.conversationProgress == .relay, "the helper advances the relay progress")
        helper.pendingConversation.removeValue(forKey: .create)?(setupFailure("call_assets_unavailable"))
        check(model.conversationProgress == nil && model.conversationFeedback?.actions == [.openInstallGuide],
              "missing call files stop progress and offer installation help")
        model.perform(.openInstallGuide)
        check(opened.last == CameraModel.installGuide, "installation help opens the app replacement instructions")
    }

    do { // Status is passive. Reconnect sends one dedicated owner command.
        let (model, helper) = launched { $0.callConnectorVersion = 1 }
        model.section = 3
        helper.conversationReply = ["result": "stopped"]
        model.tick()
        check(model.callConnectorStatus == .stopped && helper.methods.contains("conversation_connector_status"),
              "the Conversations section shows the connector's observed state")
        helper.heldConversationMethods.insert(.reconnectConnector)
        model.perform(.reconnectCallConnector)
        model.perform(.reconnectCallConnector)
        check(model.reconnectingCallConnector && helper.methods.filter { $0 == "conversation_connector_reconnect" }.count == 1,
              "Reconnect sends exactly one scoped connector action while pending")
        helper.pendingConversation.removeValue(forKey: .reconnectConnector)?(["result": "connected"])
        check(!model.reconnectingCallConnector && model.callConnectorStatus == .connected,
              "a completed connector reconnect updates the visible state")
    }

    do { // Calls that stop after opening, and explicit closes.
        let (model, helper) = launched()
        helper.conversationReply = prejoin("host")
        model.startConversation()
        check(model.conversationBlockedReason == "End the current conversation first.", "an open call blocks another start with a reason")
        model.copyConversationInvitation()
        check(model.conversationNotice?.problem == false && model.conversationFeedback == nil, "an informational note is not a problem")
        model.conversationSurface?.close(problem: "The connection failed. Check your network, then start a new conversation.")
        var feedback = model.conversationFeedback
        check(model.conversationSurface == nil && feedback?.title == "The conversation stopped" &&
              feedback?.message.contains("start a new conversation") == true && feedback?.actions == [.startNewConversation],
              "a host whose call fails is offered Start New Conversation")

        helper.conversationReply = prejoin("guest")
        model.openConversationInvitation(invitation)
        model.conversationSurface?.close(problem: "The connection failed. Check your network and reopen the invitation.")
        feedback = model.conversationFeedback
        check(feedback?.title == "The conversation stopped" && feedback?.actions == [], "a guest whose call fails gets guest advice only")

        model.start()
        model.capture.status("streaming", frames: 12)
        helper.conversationReply = prejoin("host")
        model.startConversationInBrowser()
        check(model.conversationNotice?.text == "Stop Camera before opening the host controls in your browser." &&
              model.conversationNotice?.problem == true && model.conversationPrejoin != nil,
              "a busy camera keeps the opened room and explains the browser handoff")
        model.closeConversation()
        check(model.conversationNotice == nil && model.conversationFeedback == nil && model.conversationPrejoin == nil &&
              model.capture.phase == .streaming,
              "an explicit close clears problems that belonged to the closed call")
        _ = model.quit()
    }

    let idle = ActivitySummary(stream: "Camera stopped · Microphone off", share: nil, call: nil, icon: .idle, shared: false)
    let browser = ActivitySummary(stream: idle.stream, share: "Shared publicly · until 18:48 · 1 of 3 viewers",
                                  call: "Call hosted in browser · Connected", icon: .call, shared: true)
    check(CameraView.announcement(from: idle, to: browser) == "Shared publicly · until 18:48 · 1 of 3 viewers. Call hosted in browser · Connected" &&
          CameraView.announcement(from: browser, to: idle) == "Share link ended. Conversation ended" &&
          CameraView.announcement(from: idle, to: idle) == nil,
          "status changes are announced, including a browser-hosted call ending")

    let host = ConversationView.invitationHost
    check(host("https://call.example.test/r/room-1#secret") == "call.example.test" &&
          host(" camstream://conversation/join?v=1&origin=https%3A%2F%2Fcall.example.test#secret ") == "call.example.test",
          "the invitation host is shown for web and app links")
    check(host("http://call.example.test/r/room-1") == nil && host("https://call.example.test/other") == nil &&
          host("camstream://conversation/join?origin=http%3A%2F%2Fcall.example.test") == nil && host("not a link") == nil,
          "only trusted invitation shapes show a host")
}
