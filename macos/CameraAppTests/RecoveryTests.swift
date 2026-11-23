import Darwin
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
        check(model.startBlockedReason == "Starting the camera service…" && model.helperBlockedReason == model.startBlockedReason,
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
              feedback?.title == "Camera needs attention" && feedback?.actions == [.startCamera, .chooseWebcam] &&
              feedback?.message.contains("permission") == false,
              "a capture failure with access allowed offers Try Again and Choose Webcam without permission advice")
        var panes: [SettingsPane?] = []
        model.showSettings = { panes.append($0) }
        model.perform(.chooseWebcam)
        check(panes == [.camera] && model.stage == .problem(feedback!), "Choose Webcam opens Settings at the Camera pane")
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
              model.problemText("configuration_failed").hasPrefix("Settings could not be applied."),
              "device and settings failures name their own recovery")
        _ = model.quit()
    }

    do { // Reconnect waits for the old owner to exit and preserves edits without starting capture.
        let helper = FixtureCameraHelper()
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
        model.launch()
        model.start()
        model.capture.status("streaming", frames: 12)
        model.portText = "8556"
        helper.onFailure?(.disconnected)
        check(model.helperFeedback?.title == "The camera service disconnected" && model.cameraFeedback == nil &&
              model.helperFeedback?.actions.isEmpty == true && model.activity.icon == .attention,
              "a lost helper shows the shared error while the old owner is closing")
        let requests = helper.methods
        model.retryHelper()
        check(helper.methods == requests, "reconnect waits for the previous helper to exit")
        helper.onExit?()
        check(model.helperFeedback?.actions == [.retryHelper] && model.conversationBlockedReason == model.startBlockedReason,
              "every tab offers reconnect after the helper exits")
        model.perform(.retryHelper)
        check(model.ready && model.helperFeedback == nil && model.capture.phase == .stopped &&
              model.portText == "8556" && model.saved?.port == 8554 && model.dirty && model.canSave,
              "reconnect restores access and preserves the unsaved port")
        check(!helper.methods.dropFirst(requests.count).contains("start") && !model.capture.releasedForCall,
              "reconnect doesn't start capture or grant the call lease before cleanup is confirmed")
        model.portText = "8554"
        for endEvent in [failedEvent("capture_failed"), ["version": 1, "event": "capture_stopped", "result": ["state": "stopped"]]] {
            model.start()
            check(model.capture.phase == .starting, "the new helper acknowledged a Start attempt")
            helper.onEvent?(endEvent)
            check(!model.capture.releasedForCall, "the new helper ending its attempt does not confirm the old supervisor released capture")
        }
        var lease: UUID?
        helper.hold = "stop"
        model.requestConversationCapture(stop: {}) { lease = $0 }
        helper.deliver(["error": ["code": "busy"]])
        check(lease == nil && !model.capture.releasedForCall, "a surviving capture owner blocks call devices after reconnect")
        model.requestConversationCapture(stop: {}) { lease = $0 }
        helper.deliver(["result": [:]])
        check(lease == nil && !model.capture.releasedForCall, "missing stop confirmation leaves call devices blocked")
        helper.hold = nil
        model.requestConversationCapture(stop: {}) { lease = $0 }
        check(lease != nil && helper.methods.last == "stop", "explicit call capture confirms cleanup through the new helper")
        _ = model.quit()
    }

    do { // Without the camera service the window shows one state and its fix, in the glossary's words.
        let starting = CameraModel(client: FixtureCameraHelper(), permissions: FixturePermissions(), directory: directory)
        check(starting.activity.lines == ["Starting camera service…"] && starting.helperFeedback == nil,
              "while the camera service starts, the header names only that")
        let failures: [HelperFailure] = [.launch, .runtime, .busy, .settings, .startup, .incompatible, .timeout,
                                         .disconnected, .sleepInterrupted]
        for failure in failures {
            let helper = FixtureCameraHelper()
            let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
            model.launch()
            helper.onFailure?(failure)
            helper.onExit?()
            let feedback = model.helperFeedback
            let text = [feedback?.title, feedback?.message].compactMap { $0 } + (feedback?.actions.map(\.title) ?? [])
            check(feedback != nil && !feedback!.actions.isEmpty &&
                  !text.contains { $0.localizedCaseInsensitiveContains("helper") || $0.localizedCaseInsensitiveContains("bundled") },
                  "\(failure) names the camera service, not a helper, and offers a fix")
            check(model.activity.lines == ["Camera service unavailable"],
                  "\(failure) leaves one header line, without a microphone state")
            _ = model.quit()
        }
    }

    if let executable = ProcessInfo.processInfo.environment["CAMSTREAM_NATIVE_HELPER"] {
        let helperURL = URL(fileURLWithPath: executable)
        let owner = HelperClient()
        var ownerReady = false, ownerExited = false, modelExited = false
        func waitFor(_ condition: () -> Bool) {
            let deadline = Date().addingTimeInterval(5)
            while !condition() && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.02)) }
        }
        owner.onReady = { ownerReady = true }
        owner.onExit = { ownerExited = true }
        owner.launch(executable: helperURL, directory: directory)
        waitFor { ownerReady }
        check(ownerReady, "the bundled helper acquired the private app-session lock")

        let permissions = FixturePermissions()
        permissions.video = "denied"
        let model = CameraModel(permissions: permissions, directory: directory, helper: helperURL)
        model.didExit = { modelExited = true }
        model.launch()
        waitFor { modelExited }
        check(model.helperFailure == .busy && model.helperFeedback?.actions == [.retryHelper] && owner.idle,
              "the packaged helper classifies a competing app session without disturbing its owner")
        owner.shutdown()
        waitFor { ownerExited }
        model.perform(.retryHelper)
        waitFor { model.ready && !model.working }
        check(ownerExited && model.ready && model.helperFailure == nil && model.capture.phase == .stopped,
              "retry opens a new packaged helper session after the previous owner releases the lock")
        let instance = open(directory.appendingPathComponent("instance.lock").path, O_CREAT | O_RDWR | O_CLOEXEC, 0o600)
        check(instance >= 0 && flock(instance, LOCK_EX | LOCK_NB) == 0, "a separate owner can hold the capture instance")
        var refused = false, lease: UUID?
        model.requestConversationCapture(stop: {}) { granted in refused = granted == nil }
        waitFor { refused }
        check(refused && !model.capture.releasedForCall, "the packaged helper refuses to confirm another owner's cleanup")
        modelExited = false
        let quitImmediately = model.quit()
        waitFor { modelExited }
        check(!quitImmediately && modelExited, "the recovered session exits through the normal Quit path")

        let fresh = CameraModel(permissions: permissions, directory: directory, helper: helperURL)
        var freshExited = false
        fresh.didExit = { freshExited = true }
        fresh.launch()
        waitFor { fresh.ready && !fresh.working }
        check(fresh.ready && !fresh.working, "a fresh app session starts while a separate owner holds the capture lock")
        var firstAttempt: UUID??
        fresh.requestConversationCapture(stop: {}) { firstAttempt = .some($0) }
        waitFor { firstAttempt != nil }
        check(firstAttempt == .some(nil) && !fresh.capture.releasedForCall, "the packaged helper denies a cold call-device lease while capture is locked")
        check(close(instance) == 0, "the simulated old supervisor releases its lock")

        let contender = open(directory.appendingPathComponent("instance.lock").path, O_RDWR | O_CLOEXEC)
        check(contender >= 0, "a competing supervisor can open the shared lock file")
        var tookLockAfterProbe = false
        let takeLockBeforeGrant = fresh.$capture.sink { state in
            if state.releasedForCall && !tookLockAfterProbe {
                tookLockAfterProbe = flock(contender, LOCK_EX | LOCK_NB) == 0
            }
        }
        var racedAttempt: UUID??
        fresh.requestConversationCapture(stop: {}) { racedAttempt = .some($0) }
        waitFor { racedAttempt != nil }
        takeLockBeforeGrant.cancel()
        check(tookLockAfterProbe && racedAttempt == .some(nil), "another owner taking the lock after Stop prevents the first lease")
        check(flock(contender, LOCK_UN) == 0, "the competing supervisor makes capture available again")

        fresh.requestConversationCapture(stop: {}) { lease = $0 }
        waitFor { lease != nil }
        check(fresh.capture.releasedForCall && lease != nil, "a fresh request grants the call lease after capture release")
        fresh.releaseConversationCapture(UUID())
        check(flock(contender, LOCK_EX | LOCK_NB) == -1 && errno == EWOULDBLOCK,
              "a stale release cannot unlock the active call")
        if let lease { fresh.releaseConversationCapture(lease) }
        check(flock(contender, LOCK_EX | LOCK_NB) == 0, "ending the lease releases the shared lock")
        fresh.requestConversationCapture(stop: {}) { lease = $0 }
        check(lease == nil, "a subsequent call checks for a new capture owner even after confirmed release")
        check(flock(contender, LOCK_UN) == 0, "the contender releases its second lock")
        fresh.requestConversationCapture(stop: {}) { lease = $0 }
        check(lease != nil, "a call can reserve the lock again")
        var quitReplies = 0
        fresh.didFinishQuit = { quitReplies += 1 }
        check(!fresh.quit(), "Quit waits for helper exit and native media teardown")
        waitFor { freshExited }
        check(freshExited && flock(contender, LOCK_EX | LOCK_NB) == -1 && errno == EWOULDBLOCK,
              "helper exit cannot unlock devices while native call teardown is pending")
        check(quitReplies == 0 && !fresh.quit(), "Quit after helper exit still awaits native media teardown")
        if let lease { fresh.releaseConversationCapture(lease) }
        check(quitReplies == 1 && flock(contender, LOCK_EX | LOCK_NB) == 0,
              "native media teardown releases capture and completes Quit once")
        check(close(contender) == 0, "the competing capture probe exits")
        try? FileManager.default.removeItem(at: directory)
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
              model.conversationBlockedReason == "This version of the camera service can't host or join calls.",
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
        check(opened.last == CameraModel.setupGuide && CameraModel.setupGuide.fragment == "calls",
              "Open Setup Guide opens the call setup section")

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
              "Try Again starts the call through the normal action")

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
        model.dismissBanner()
        check(model.banner?.title == "Guests can't reach this Mac" && model.banner?.dismissible == false &&
              model.banner?.actions == [.checkConversationRoute, .openSetupGuide],
              "the banner keeps an unreachable guest link, with its fixes, until it clears")
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
        check(model.conversationFeedback?.title == "Couldn't join the call" &&
              model.conversationFeedback?.message.contains("already has a guest") == true &&
              model.conversationFeedback?.actions == [.retryConversationInvitation],
              "a full room leaves the invitation available for explicit retry")
        model.perform(.retryConversationInvitation)
        check(helper.incomingInvitations == [invitation, invitation], "the retry action uses the same invitation")
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
        helper.conversationReply = ["result": "stopped"]
        model.tick()
        check(!helper.methods.contains("conversation_connector_status"), "the connector is polled only while Settings → Calls shows it")
        model.callSettingsVisible = true
        model.tick()
        check(model.callConnectorStatus == .stopped && helper.methods.contains("conversation_connector_status"),
              "Settings → Calls shows the connector's observed state")
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
        check(model.conversationBlockedReason == "End the current call first.", "an open call blocks another start with a reason")
        model.copyConversationInvitation()
        check(model.conversationNotice?.problem == false && model.conversationFeedback == nil, "an informational note is not a problem")
        model.conversationSurface?.close(problem: "The connection failed. Check your network, then start a new call.")
        var feedback = model.conversationFeedback
        check(model.conversationSurface == nil && feedback?.title == "The call stopped" &&
              feedback?.message.contains("start a new call") == true && feedback?.actions == [.startNewConversation] &&
              feedback?.actions.first?.title == "Start New Call",
              "a host whose call fails is offered Start New Call")
        check(model.banner?.title == "The call stopped" && model.banner?.actions == [.startNewConversation] && model.banner?.dismissible == true,
              "with no call on the stage, the banner carries the problem and Start New Call")
        helper.conversationReply = prejoin("host")
        model.perform(.startNewConversation)
        check(model.conversationSurface != nil, "Start New Call starts a call as the Call card does")
        model.closeConversation()

        helper.conversationReply = prejoin("guest")
        model.openConversationInvitation(invitation)
        model.conversationSurface?.close(problem: "The connection failed. Check your network and reopen the invitation.")
        feedback = model.conversationFeedback
        check(feedback?.title == "The call stopped" && feedback?.actions == [], "a guest whose call fails gets guest advice only")

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
          CameraView.announcement(from: browser, to: idle) == "Share link ended. Call ended" &&
          CameraView.announcement(from: idle, to: idle) == nil,
          "status changes are announced, including a browser-hosted call ending")

    let host = JoinCallView.invitationHost
    check(host("https://call.example.test/r/room-1#secret") == "call.example.test" &&
          host(" camstream://conversation/join?v=1&origin=https%3A%2F%2Fcall.example.test#secret ") == "call.example.test",
          "the invitation host is shown for web and app links")
    check(host("http://call.example.test/r/room-1") == nil && host("https://call.example.test/other") == nil &&
          host("camstream://conversation/join?origin=http%3A%2F%2Fcall.example.test") == nil && host("not a link") == nil,
          "only trusted invitation shapes show a host")
}
