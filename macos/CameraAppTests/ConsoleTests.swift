import AppKit

// The camera console: stage, status pill and banner projections, Start Camera
// applying valid edits, hot-plugged devices, calls on the stage with Start
// Call and Join, the first-run welcome guide, and Start Camera and Create
// Link. Fixture helpers only; nothing here can open a sensor.
func runConsoleTests(conversations: [String: Any], sharing examples: [String: Any], check: (Bool, String) -> Void) {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    func live(_ model: CameraModel, _ status: (([String: Any]) -> Void)) {
        model.startCamera()
        status(["state": "streaming", "frames": 30])
        model.tick()
    }

    do { // One state for the stage and the pill.
        let helper = FixtureCameraHelper()
        let permissions = FixturePermissions()
        permissions.video = "not_determined"
        let model = CameraModel(client: helper, permissions: permissions, directory: directory)
        defer { _ = model.quit() }
        check(model.stage == .serviceStarting("Starting the camera service…") && model.statusPill.label(at: Date()) == "Starting…",
              "before the camera service is ready, the stage and the pill say it is starting")
        model.launch()
        check(model.statusPill == StatusPill(text: "Setup needed", tone: .attention) &&
              model.stage.feedback?.title == "Camstream needs camera access" && model.stage.feedback?.actions == [.allowCamera],
              "without camera access the stage asks for it, with Allow Camera as its action")
        permissions.video = "denied"
        model.refreshPermission()
        check(model.stage.feedback?.actions == [.openCameraSettings], "denied access points to System Settings")
        permissions.video = "authorized"
        model.refreshPermission()
        check(model.stage == .off(webcam: "Built-in camera", name: "macbook") && model.statusPill.label(at: Date()) == "Off",
              "a stopped camera shows its webcam and camera name, and the pill says Off")
        model.draft.device = "Studio Display Camera"
        check(model.stage == .off(webcam: "Studio Display Camera", name: "macbook"), "the stage names a chosen webcam")
        model.draft.device = "auto"
        model.startCamera()
        check(model.stage == .starting && model.statusPill.text == "Starting…", "Start Camera shows the starting state")
        helper.status = ["state": "streaming", "frames": 30]
        model.tick()
        let pill = model.statusPill
        check(model.stage == .live && pill.tone == .live && pill.since != nil &&
              pill.label(at: pill.since!.addingTimeInterval(75)) == "Live · 1:15" &&
              pill.label(at: pill.since!.addingTimeInterval(3725)) == "Live · 1:02:05",
              "a live stream counts its time in the pill")
        helper.onEvent?(["event": "capture_stopped", "error": ["code": "capture_failed"]])
        check(model.stage.feedback?.title == "Camera needs attention" && model.stage.feedback?.actions == [.startCamera, .chooseWebcam] &&
              model.statusPill == StatusPill(text: "Needs attention", tone: .attention) && model.streamingSince == nil,
              "a camera problem shows its cause with Try Again and Choose Webcam, and the timer stops")
        helper.onFailure?(.disconnected)
        helper.onExit?()
        check(model.stage.feedback?.title == "The camera service disconnected" && model.stage.feedback?.actions == [.retryHelper] &&
              model.statusPill.text == "Needs attention", "a lost camera service shows one message and Reconnect")
    }

    do { // Settings apply when valid; Start Camera applies pending edits first.
        let helper = FixtureCameraHelper()
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
        defer { _ = model.quit() }
        model.launch()
        model.draft.path = "desk"
        check(model.dirty && !model.canStart && model.canStartCamera && model.startCameraBlockedReason == nil,
              "a valid edit that isn't applied yet doesn't block Start Camera")
        model.startCamera()
        check(Array(helper.methods.suffix(2)) == ["configure", "start"] && model.saved?.path == "desk" &&
              model.notice == nil && model.capture.phase == .starting,
              "Start Camera applies the edit without a notice, then starts")
        model.stop()
        let configures = { helper.methods.filter { $0 == "configure" }.count }
        let before = configures()
        model.portText = "80"
        model.applySettings()
        check(configures() == before && model.startCameraBlockedReason == "Choose a port from 1024 to 65535." &&
              model.validationPane == .network && model.portValidation != nil && model.nameValidation == nil,
              "an invalid value doesn't apply, and Start names it and its Settings pane")
        model.portText = "8554"
        helper.hold = "configure"
        model.draft.path = "one"
        let sent = model.config
        model.applySettings()
        model.draft.path = "two"
        helper.hold = nil
        helper.deliver(["result": try! sent.helperObject(audioSupported: true)])
        check(model.saved?.path == "one" && model.draft.path == "two" && model.dirty && model.notice == nil,
              "an edit made while a change applies stays as the draft")
        model.applySettings()
        check(model.saved?.path == "two" && !model.dirty, "the next apply sends it")
        helper.hold = "devices"
        model.refreshDevices()
        model.draft.path = "three"
        var applied: Bool?
        model.applySettings { applied = $0 }
        check(applied == nil && model.working, "a change waits while the camera service is busy")
        helper.hold = nil
        helper.deliver(["result": [["index": "0", "name": "MacBook Pro Camera"]]])
        check(applied == true && model.saved?.path == "three" && !model.working, "and applies once it is free")
        model.save()
        check(model.notice == nil, "Save without changes does nothing")
    }

    do { // Connected and removed devices refresh the lists, never while live.
        let helper = FixtureCameraHelper()
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
        defer { _ = model.quit() }
        model.launch()
        let lists = { helper.methods.filter { $0 == "devices" }.count }
        let launched = lists()
        model.devicesChanged()
        check(lists() == launched + 1, "a connected or removed device refreshes the camera list")
        live(model) { helper.status = $0 }
        let streaming = lists()
        model.devicesChanged()
        model.tick()
        check(model.capture.phase == .streaming && lists() == streaming, "the list doesn't refresh while the camera is live")
        model.stop()
        model.tick()
        check(lists() == streaming + 1, "it refreshes once the camera stops")
        model.tick()
        check(lists() == streaming + 1, "and only once")
    }

    do { // One banner: problems first, then the last result.
        let helper = FixtureSharingCamera()
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
        model.writeClipboard = { _ in }
        defer { _ = model.quit() }
        model.launch()
        check(model.banner == nil && model.callCardLine == "No call. Start one, or join from an invitation.",
              "an idle console has no message")
        model.copyEndpoint()
        check(model.banner == ConsoleBanner(source: .camera, text: "RTSP address copied. The password is only in the exported .env file.", problem: false),
              "a result shows in the banner")
        helper.conversationReply = ["error": ["code": "transport"]]
        model.startConversation()
        let feedback = model.conversationFeedback
        check(model.banner?.source == .call && model.banner?.problem == true && feedback != nil &&
              model.banner?.title == feedback?.title && model.banner?.text == feedback?.message &&
              model.banner?.actions == Array(feedback!.actions.prefix(2)) && model.banner?.actions.isEmpty == false &&
              model.banner?.dismissible == true && model.stage == .off(webcam: "Built-in camera", name: "macbook"),
              "a call that couldn't start brings its name and fixes to the banner, and the stage keeps the camera")
        model.dismissBanner()
        check(model.banner?.source == .camera && model.conversationNotice == nil, "dismissing clears that message")
        model.dismissBanner()
        check(model.banner == nil && model.notice == nil, "then the next one")
    }

    let waiting = conversations["waiting"] as! [String: Any]
    let page = "https://call.example.test/r/" + (waiting["room_id"] as! String)
    func admission(_ role: String) -> [String: Any] {
        ["result": ["page_url": page, "invitation_url": page + "#invitation-secret",
                    "admission": ["room": waiting, "role": role, "token": "fixture-grant"]]]
    }

    do { // Calls: the call takes the stage; the pill and card follow it.
        let helper = FixtureSharingCamera()
        helper.conversationReply = admission("host")
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory, openBrowser: { _ in true })
        defer { _ = model.quit() }
        model.launch()
        check(model.callStage == nil, "without a call the stage shows the camera")
        helper.heldConversationMethods = [.create]
        model.startConversation()
        check(model.stage == .call(.opening(model.conversationProgress?.message ?? "Opening the invitation…")) &&
              model.callCardLine != "No call. Start one, or join from an invitation.",
              "a call that is opening takes the stage with what it waits for")
        helper.heldConversationMethods = []
        helper.pendingConversation.removeValue(forKey: .create)?(admission("host"))
        guard let surface = model.conversationSurface else { check(false, "a host call opens"); return }
        check(model.stage == .call(.page) && model.statusPill.text == "In call" &&
              model.callCardLine == "Hosting call · Camera off · Mic off",
              "an open call shows its page on the stage, in the pill and on its card")
        _ = model.reportConversationState(ConversationCallState(role: .host, phase: .connected, camera: false, microphone: false), from: surface)
        let pill = model.statusPill
        check(pill.tone == .live && pill.since != nil && pill.label(at: pill.since!.addingTimeInterval(61)) == "In call · 1:01",
              "a connected call counts its time")
        var lease: UUID?
        model.requestConversationCapture(stop: {}) { lease = $0 }
        check(lease != nil && model.stage == .call(.page), "the camera in the call keeps the page on the stage")
        if let lease { model.releaseConversationCapture(lease) }
        model.closeConversation()
        check(model.statusPill.text == "Off" && model.callConnectedSince == nil && model.callStage == nil &&
              model.stage == .off(webcam: "Built-in camera", name: "macbook"),
              "closing the call returns the stage to the camera and the pill to Off")

        model.startConversationInBrowser()
        var browserStage = false
        if case .call(.browser) = model.stage { browserStage = true }
        check(browserStage && model.conversationSurface == nil, "hosting in the browser shows the room's state on the stage")
        model.closeConversation()
    }

    do { // Start Call follows Settings → Calls and asks before it stops a stream; Join stops it at once.
        let suite = "camstream-console-tests-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        let helper = FixtureSharingCamera()
        helper.conversationReply = admission("host")
        var opened: [URL] = []
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory,
                                openBrowser: { opened.append($0); return true }, defaults: defaults)
        defer { _ = model.quit() }
        var asked: [Confirmation] = []
        var answer = false
        model.confirm = { confirmation, confirmed in
            asked.append(confirmation)
            if answer { confirmed() }
        }
        model.launch()
        check(model.callStart == .cameraOff, "Start Call starts with the camera and mic off unless Settings says otherwise")
        model.startCall()
        check(asked.isEmpty && model.stage == .call(.page), "with the camera off, Start Call opens the call at once")
        model.endCall()
        check(asked.last == model.endConfirmation && asked.last?.title == "End this call?" && asked.last?.confirm == "End Call" &&
              model.conversationPrejoin != nil, "End Call asks first, and Keep Call keeps it")
        answer = true
        model.endCall()
        check(model.conversationPrejoin == nil && helper.methods.filter { $0 == "conversation_end" }.count == 1,
              "confirming ends the call once")

        live(model) { helper.status = $0 }
        answer = false
        asked = []
        model.startCall()
        check(asked == [model.streamStopConfirmation] && asked.first?.title == "Stop the stream and start a call?" &&
              asked.first?.confirm == "Stop Stream" && asked.first?.cancel == "Cancel" &&
              model.capture.phase == .streaming && model.conversationPrejoin == nil,
              "while streaming, Start Call asks before stopping the stream, and Cancel keeps it")
        answer = true
        model.startCall()
        check(model.capture.phase != .streaming && helper.methods.contains("stop") && model.stage == .call(.page),
              "Stop Stream stops the camera, then the call takes the stage")
        model.endCall()

        model.callStart = .browser
        let reopened = CameraModel(client: FixtureSharingCamera(), permissions: FixturePermissions(), directory: directory, defaults: defaults)
        check(defaults.string(forKey: "callStart") == "browser" && reopened.callStart == .browser, "the Start Call choice persists")
        _ = reopened.quit()
        model.startCall()
        check(model.hostInBrowser && opened.count == 1, "Host in your browser opens the host controls there")
        model.endCall()
        model.callStart = .setup
        model.startCall()
        check(model.stage == .call(.page) && !model.hostInBrowser, "Check camera and mic first opens the call page here")
        model.endCall()

        helper.conversationReply = admission("guest")
        live(model) { helper.status = $0 }
        asked = []
        model.joinCall("   ")
        check(model.capture.phase == .streaming && model.conversationPrejoin == nil, "an empty invitation joins nothing")
        model.joinCall("  camstream://conversation/join?v=1#fixture  ")
        check(asked.isEmpty && model.capture.phase != .streaming && helper.incomingInvitations.last == "camstream://conversation/join?v=1#fixture" &&
              model.conversationPrejoin?.admission.role == .guest,
              "Join stops the stream without asking again, then opens the trimmed invitation")
        answer = false
        model.endCall()
        check(asked.last == Confirmation(title: "Leave this call?", detail: "The host stays in the call.", confirm: "Leave", cancel: "Cancel") &&
              model.conversationPrejoin != nil, "Leave Call asks the guest first")
        answer = true
        model.endCall()
        check(model.conversationPrejoin == nil && helper.methods.contains("conversation_leave"), "Leave leaves the call")
    }

    do { // First run: the welcome guide reaches Start Camera without network settings.
        let helper = FixtureCameraHelper()
        helper.configured = false
        let permissions = FixturePermissions()
        permissions.video = "not_determined"
        let model = CameraModel(client: helper, permissions: permissions, directory: directory)
        defer { _ = model.quit() }
        check(model.welcomeShown == nil, "the guide waits for the camera service")
        model.launch()
        check(model.welcome == .access && model.welcomeShown == .access, "a first launch without settings opens the guide at camera access")
        model.continueWelcome()
        check(model.welcome == .access, "Continue waits for camera access")
        model.requestPermission()                                               // 1. Allow Camera
        permissions.resolveVideo("authorized")
        check(model.welcome == .camera && model.draft.device == "auto" && model.draft.path == "macbook",
              "allowing the camera moves on to the webcam and its prefilled camera name")
        model.draft.path = "desk camera"
        model.continueWelcome()
        check(model.welcome == .camera && model.saved == nil, "an invalid camera name keeps the guide on that step")
        model.draft.path = "desk"
        model.continueWelcome()                                                 // 2. Continue
        check(model.welcome == .microphone && model.saved?.path == "desk", "Continue applies the webcam and its name")
        model.backInWelcome()
        check(model.welcome == .camera, "Back returns to the webcam")
        model.continueWelcome()
        model.setMicrophoneEnabled(true)
        model.finishWelcome()
        check(model.welcome == .microphone && model.microphoneValidation != nil, "Done waits for a microphone once audio is included")
        model.setMicrophoneEnabled(false)
        model.finishWelcome()                                                   // 3. Done
        check(model.welcome == nil && model.welcomeShown == nil && model.stage == .off(webcam: "Built-in camera", name: "desk") &&
              model.canStartCamera, "Done ends on the console at Start Camera")
        model.startCamera()                                                     // 4. Start Camera
        helper.status = ["state": "streaming", "frames": 30]
        model.tick()
        check(model.stage == .live && !helper.methods.contains("export"), "the fourth step is live, with no network or server step")
        model.stop()
        helper.configured = false
        helper.onReady?()
        check(model.welcome == nil, "a finished guide doesn't open again when the camera service reconnects")
    }

    do { // The guide starts where access already is, and gives way to Set Up Later, Start Camera and calls.
        let helper = FixtureCameraHelper()
        helper.configured = false
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
        defer { _ = model.quit() }
        model.launch()
        check(model.welcome == .camera, "with access already allowed, the guide starts at the webcam")
        model.startCamera()
        check(model.welcome == nil && model.capture.phase == .starting, "Start Camera from the menus ends the guide")

        let later = FixtureCameraHelper()
        later.configured = false
        let skipped = CameraModel(client: later, permissions: FixturePermissions(), directory: directory)
        defer { _ = skipped.quit() }
        skipped.launch()
        skipped.draft.path = "studio"
        skipped.skipWelcome()
        check(skipped.welcome == nil && skipped.saved == nil && skipped.dirty && skipped.stage == .off(webcam: "Built-in camera", name: "studio"),
              "Set Up Later goes to the console and keeps the choices as a draft for Start Camera")

        let calls = FixtureSharingCamera()
        calls.configured = false
        calls.conversationReply = admission("guest")
        let invited = CameraModel(client: calls, permissions: FixturePermissions(), directory: directory, openBrowser: { _ in true })
        defer { _ = invited.quit() }
        invited.launch()
        check(invited.welcomeShown == .camera, "a first launch with calls opens the guide too")
        invited.openConversationInvitation("camstream://conversation/join?v=1#fixture")
        check(invited.welcomeShown == nil && invited.stage == .call(.page), "an invitation opened on first run shows its call")
        invited.closeConversation()
        check(invited.welcomeShown == .camera, "after the call, the guide is where it was")
    }

    do { // Start Camera and Create Link: one labelled action, then the link is copied.
        var created = examples["created"] as! [String: Any]
        var share = created["share"] as! [String: Any]
        share["expires_at"] = ISO8601DateFormatter().string(from: Date().addingTimeInterval(3600))
        created["share"] = share
        let link = "https://view.example.test/s/\(share["share_id"] as! String)#fixture-token"
        let helper = FixtureSharingCamera()
        helper.sharing.replies[.setup] = examples["native_setup_reply"] as? [String: Any]
        helper.sharing.replies[.status] = ["result": ["camera_id": "local-camera", "state": "stopped", "media": "idle", "viewers": 0, "viewer_limit": 0]]
        helper.sharing.replies[.create] = ["result": created]
        helper.sharing.replies[.copyLink] = ["result": ["url": link]]
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
        var copied: [String] = []
        model.sharing.writeLink = { copied.append($0) }
        defer { _ = model.quit() }
        model.launch()
        let creates = { helper.sharing.methods.filter { $0 == .create }.count }
        check(model.sharing.ingressReady && !model.sharing.canShare, "with the camera off, sharing alone can't create a link")
        model.createShareLink()
        check(helper.methods.contains("start") && creates() == 0, "Start Camera and Create Link starts the camera first")
        helper.status = ["state": "streaming", "frames": 30]
        model.tick()
        check(creates() == 1 && model.sharing.phase == .active && copied == [link] &&
              model.banner?.source == .sharing && model.banner?.text.hasPrefix("Link copied.") == true,
              "once the stream is live it creates the link and copies it")
        check(model.statusPill.extras == ["0 viewers"] && model.statusPill.label(at: model.streamingSince!) == "Live · 0:00 · 0 viewers",
              "the pill adds the link's viewer count")
        model.sharing.stopSharing()
        model.stop()
        model.createShareLink()
        model.stop()
        live(model) { helper.status = $0 }
        check(creates() == 1 && copied == [link], "stopping the camera cancels Start Camera and Create Link")
        model.createShareLink()
        check(creates() == 2 && copied == [link, link], "with the camera live, Create Link shares and copies at once")
    }
}
