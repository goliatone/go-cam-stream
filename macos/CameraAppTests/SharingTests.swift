import Foundation

// T08 sharing checks. Published Go fixtures define the wire; fixture helpers
// stand in for the owner pipe. No capture, network or public delivery occurs.
func runSharingTests(repo: URL, check: @escaping (Bool, String) -> Void) throws {
    func load(_ path: String) throws -> [String: Any] {
        try JSONSerialization.jsonObject(with: Data(contentsOf: repo.appendingPathComponent(path))) as! [String: Any]
    }
    let examples = try load("data/api/sharing_examples.json")
    let modes = try load("macos/fixtures/sharing-ingress-modes.json")
    func decode<T: Decodable>(_ value: Any) throws -> T { try JSONDecoder().decode(T.self, from: JSONSerialization.data(withJSONObject: value)) }
    func object(_ value: Any?) -> [String: Any] { value as! [String: Any] }
    func shape(_ value: Any?) -> NSDictionary { var body = object(value); body.removeValue(forKey: "id"); return body as NSDictionary }
    func encoded(_ method: SharingMethod, _ arguments: NativeSharingRequest.Arguments?) throws -> NSDictionary {
        let data = try JSONEncoder().encode(NativeSharingRequest(version: 1, id: "fixture", method: method, sharing: arguments))
        return shape(try JSONSerialization.jsonObject(with: data))
    }

    // The Swift client decodes every published native projection.
    let ready: NativeSharingEvent<NativeSharingReady> = try decode(examples["native_ready"]!)
    check(ready.event == "ready" && ready.result.sharing == 1, "published ready advertises sharing version one")
    let setupReply: NativeSharingReply<NativeSharingSetup> = try decode(examples["native_setup_reply"]!)
    check(setupReply.result?.ingressState == .ready && setupReply.result?.saved?.relayCredentialSet == true, "setup readback decodes saved settings")
    let configured: NativeSharingReply<NativeSharingSetup> = try decode(examples["native_configured"]!)
    check(configured.result?.saved?.relayMode == "cloudflare_turn" && configured.error == nil, "configure reply decodes")
    let relayFailure: NativeSharingReply<NativeSharingSetup> = try decode(examples["native_relay_failure"]!)
    check(relayFailure.result == nil && relayFailure.error?.code == "relay_unavailable", "relay failure decodes as an error")
    let changed: NativeSharingEvent<NativeSharingView> = try decode(examples["native_changed"]!)
    check(changed.event == "sharing_changed" && changed.result.state == .available, "sharing_changed decodes")
    let created: NativeSharingCreated = try decode(examples["created"]!)
    let shareID = created.share.shareID!
    let empty: NativeSharingSetup = try decode(examples["setup_empty"]!)
    check(empty.ingressModes.isEmpty, "empty capability array decodes")
    let managedSetup: NativeSharingSetup = try decode(modes["managed_public_private"]!)
    let privateChanged: NativeSharingEvent<NativeSharingView> = try decode(modes["private_changed"]!)
    check(managedSetup.ingressModes.count == 3 && privateChanged.result.audience == .privateTailnet, "managed capability fixtures decode")

    // Requests encode exactly the published, Go-validated shapes.
    var fixtureForm = SharingForm()
    fixtureForm.ingressMode = "external_https"; fixtureForm.publicBaseURL = "https://view.example.test"
    fixtureForm.guestListen = "127.0.0.1:8890"; fixtureForm.relayMode = "cloudflare_turn"
    fixtureForm.keyID = "fixture-key"; fixtureForm.apiToken = "fixture-write-only-token"
    check(try encoded(.configure, .init(settings: fixtureForm.settings, relayCredentials: fixtureForm.credentials)) == shape(examples["native_configure"]),
          "configure encodes the published request")
    check(try encoded(.setup, nil) == shape(examples["native_setup"]) && encoded(.testRelay, nil) == shape(examples["native_relay_test"]),
          "setup and relay test carry no sharing arguments")
    check(try encoded(.create, .init(cameraID: SharingModel.cameraID, options: .init(durationSeconds: .fifteenMinutes, viewerLimit: 1))) == shape(examples["create"]),
          "create encodes the published request")
    var managedForm = SharingForm(managedSetup)
    managedForm.ingressMode = "tailscale_funnel"
    check(try encoded(.configure, .init(settings: managedForm.settings, relayCredentials: managedForm.credentials)) == shape(modes["managed_configure"]),
          "registered managed modes configure through the same contract")
    check("\(fixtureForm.credentials!)" == "[relay input redacted]", "relay input stays redacted in diagnostics")

    let start = SharingModel.date("2026-11-17T22:30:00Z")!
    var now = start
    func model(_ helper: FixtureSharingHelper) -> SharingModel {
        let value = SharingModel(helper: helper)
        value.clock = { now }
        value.locale = Locale(identifier: "en_GB")
        value.timeZone = TimeZone(identifier: "UTC")!
        return value
    }
    let stoppedView: [String: Any] = ["camera_id": "local-camera", "state": "stopped", "media": "idle", "viewers": 0, "viewer_limit": 0]
    func view(_ state: String, media: String = "idle", viewers: Int = 0, id: String = shareID, audience: String = "public") -> [String: Any] {
        ["share_id": id, "camera_id": "local-camera", "state": state, "media": media, "audience": audience,
         "expires_at": "2026-11-17T23:15:00Z", "viewers": viewers, "viewer_limit": 1]
    }
    let link = object(examples["created"])["link"]!
    let linkURL = created.link.url

    // A helper without the capability receives no sharing request.
    do {
        let helper = FixtureSharingHelper(); helper.sharingVersion = 0
        let legacy = model(helper)
        legacy.connect()
        legacy.observe(captureLive: true, captureStopped: false)
        legacy.shareCamera(); legacy.loadSetup(); legacy.saveSetup(); legacy.testRelay()
        check(!legacy.supported && helper.requests.isEmpty && legacy.shareBlockedReason!.contains("isn't available"),
              "unsupported helper keeps sharing local and explains why")
    }

    // One-time setup: registered modes, validation, write-only credentials and relay test.
    let helper = FixtureSharingHelper()
    helper.replies[.setup] = ["result": examples["setup"]!]
    helper.replies[.status] = ["result": stoppedView]
    let sharing = model(helper)
    sharing.connect()
    check(sharing.supported && helper.methods == [.setup, .status], "connect reads saved setup and current status")
    check(sharing.setupStatus == "Setup needed" && !sharing.canShare && sharing.shareBlockedReason == "Set up the guest page in Sharing settings first.",
          "setup-needed blocks sharing with one action")
    check(sharing.form.ingressMode == "disabled" && sharing.form.relayMode == "cloudflare_turn" && !sharing.setupDirty,
          "a fresh setup proposes the managed relay without unsaved changes")
    check(sharing.ingressOptions.map(\.mode) == ["disabled", "external_https"] && sharing.ingressOptions[1].label == "Your HTTPS address (public internet)",
          "only registered ingress modes render, with their audience")
    sharing.form.ingressMode = "external_https"
    for (origin, listener) in [("http://view.example.test", "127.0.0.1:8890"), ("https://view.example.test/path", "127.0.0.1:8890"),
                               ("https://view.example.test?x=1", "127.0.0.1:8890"), ("https://view.example.test", "0.0.0.0:8890"),
                               ("https://view.example.test", "127.0.0.1:70000")] {
        sharing.form.publicBaseURL = origin; sharing.form.guestListen = listener
        check(sharing.setupValidation != nil && !sharing.canSaveSetup, "invalid origin or listener is refused: \(origin) \(listener)")
    }
    sharing.form.publicBaseURL = "https://view.example.test"; sharing.form.guestListen = "127.0.0.1:8890"
    check(sharing.setupValidation?.contains("TURN key ID") == true, "the relay step asks for credentials once")
    sharing.form.keyID = "fixture-key"; sharing.form.apiToken = "has space"
    check(sharing.setupValidation != nil, "relay token must be printable without spaces")
    sharing.form.apiToken = "fixture-write-only-token"
    check(sharing.canSaveSetup, "complete external HTTPS and relay settings can be saved")
    helper.hold = [.configure]
    sharing.saveSetup()
    check(sharing.setupWorking && !sharing.canEditSetup && !sharing.canSaveSetup, "save in flight locks the form")
    check(try encoded(.configure, helper.requests.last!.arguments) == shape(examples["native_configure"]), "model save sends the published configure request")
    var untested = object(object(examples["native_configured"])["result"]); untested["relay_state"] = "setup_needed"
    helper.deliver(["result": untested])
    check(sharing.setup?.ingressState == .ready && sharing.form.apiToken.isEmpty && sharing.form.keyID.isEmpty && !sharing.setupDirty,
          "readback applies and clears write-only input")
    check(sharing.setupStatus == "Ready · relay not tested" && sharing.canTestRelay && sharing.setupNotice?.contains("Test Connection") == true,
          "a saved relay asks for Test Connection")
    sharing.form.publicBaseURL = "https://camera.example.test"
    check(sharing.canSaveSetup && sharing.form.credentials == nil && !sharing.canTestRelay, "a saved relay credential is reused; unsaved edits block the test")
    helper.hold = []
    helper.replies[.configure] = ["error": ["code": "invalid_request", "message": "PRIVATE-DETAIL", "retryable": false]]
    sharing.saveSetup()
    check(helper.requests.last!.arguments?.relayCredentials == nil, "an unchanged credential is not resent")
    check(sharing.setupProblem == SharingModel.setupMessage("invalid_request") && !sharing.setupProblem!.contains("PRIVATE"),
          "setup errors use local copy only")
    sharing.form.publicBaseURL = "https://view.example.test"
    sharing.form.relayMode = "coturn"
    check(sharing.setupValidation?.contains("TURN URLs") == true, "coturn needs relay URLs")
    sharing.form.relayURLs = "turns:relay.example.test:5349?transport=tcp\nturn:relay.example.test:3478?transport=udp"
    check(sharing.setupValidation?.contains("shared secret") == true, "a Cloudflare credential is not reused for coturn")
    sharing.form.relayMode = "cloudflare_turn"; sharing.form.relayURLs = ""
    check(!sharing.setupDirty, "returning to saved values leaves nothing to save")
    helper.replies[.testRelay] = ["error": object(examples["native_relay_failure"])["error"]!]
    sharing.testRelay()
    check(sharing.relayProblem != nil && !sharing.relayReady && sharing.relayStatus == sharing.relayProblem && !sharing.relayProblem!.contains("Check the configured"),
          "relay failure shows one local actionable error")
    var readyRelay = untested; readyRelay["relay_state"] = "ready"
    helper.replies[.testRelay] = ["result": readyRelay]
    sharing.testRelay()
    check(sharing.relayReady && sharing.relayProblem == nil && sharing.setupStatus == "Ready" && sharing.relayStatus == "Relay ready",
          "Test Connection reports Ready")

    // Routine sharing: Share, Copy Link and Stop Sharing.
    check(!sharing.canShare && sharing.shareBlockedReason!.contains("Start the camera"), "sharing waits for an explicitly started camera")
    sharing.observe(captureLive: true, captureStopped: false)
    check(sharing.canShare && sharing.duration == .oneHour && sharing.viewerLimit == 1 && sharing.headline == "Not sharing",
          "one hour and one viewer are the defaults")
    check(sharing.preShareNotice.hasPrefix("Anyone with the link can watch for 1 hour, one viewer at a time."), "the audience is stated before sharing")
    sharing.duration = .fifteenMinutes
    helper.hold = [.create]
    sharing.shareCamera()
    sharing.shareCamera()
    check(sharing.phase == .pending && !sharing.canShare && !sharing.canEditOptions && sharing.setupLockedByShare && sharing.headline == "Creating link…",
          "pending share locks options and settings")
    check(try helper.methods.filter { $0 == .create }.count == 1 && encoded(.create, helper.requests.last!.arguments) == shape(examples["create"]),
          "one create with the selected options")
    helper.deliver(["result": examples["created"]!])
    check(sharing.phase == .active && sharing.headline == "Shared publicly until 23:15", "the audience and absolute end time lead the share card")
    check(sharing.viewerLine == "No one is watching yet · 1 viewer at a time" && sharing.scopeBadge?.title == "Public link",
          "the card shows the viewer limit and marks a public link apart from the private stream")
    check(sharing.audienceNotice == "Anyone with this link can watch until 23:15.", "the anyone-with-link notice names the end time")
    check(sharing.showsLinkControls && !sharing.canTestRelay && !sharing.canEditSetup,
          "Test Connection waits while a link exists, so Stop Sharing never queues behind it")
    check(sharing.mediaLabel == "Waiting for a viewer" && sharing.canCopyLink && sharing.canStopSharing && !sharing.canEditSetup,
          "an active share offers Copy Link and Stop Sharing only")
    let shown = [sharing.headline, sharing.detail ?? "", sharing.notice ?? "", sharing.audienceNotice, sharing.mediaLabel ?? ""].joined()
    check(!shown.contains("testonly") && !shown.contains("/s/"), "link material never reaches status text")
    helper.emit(view("available", media: "live", viewers: 1))
    check(sharing.mediaLabel == "Live" && sharing.viewerLine == "1 of 1 viewer watching", "live media and viewer count update from events")
    helper.emit(view("available", media: "reconnecting", viewers: 1))
    check(sharing.mediaLabel == "Reconnecting…" && sharing.detail!.contains("30 seconds"), "reconnecting keeps its reservation")
    helper.emit(view("degraded", media: "unavailable", viewers: 1))
    check(sharing.phase == .degraded && sharing.mediaLabel == "Connection problem" && sharing.canStopSharing && sharing.canCopyLink,
          "degraded sharing stays stoppable")
    check(sharing.detail == "Video isn't reaching viewers right now. Viewers can reload the page to try again; Stop Sharing ends the link."
          && sharing.viewerLine == "1 of 1 viewer watching" && sharing.headline == "Shared publicly until 23:15",
          "a media problem keeps the audience, viewers and expiry in view")
    let requestsBeforeIngress = helper.requests.count
    var ingress = view("degraded", media: "live", viewers: 1)
    ingress["error"] = ["code": "ingress_unavailable", "message": "PRIVATE-DETAIL", "retryable": true]
    helper.emit(ingress)
    check(sharing.phase == .degraded && sharing.detail?.contains("Camstream checks again every few seconds") == true
          && !(sharing.detail ?? "").contains("PRIVATE-DETAIL") && sharing.canStopSharing && helper.requests.count == requestsBeforeIngress,
          "an unreachable guest page explains the automatic check and sends nothing to the helper")
    helper.emit(view("available", media: "live", viewers: 1))
    check(sharing.phase == .active, "recovered media returns to active")
    var copied: [String] = []
    sharing.writeLink = { copied.append($0) }
    helper.hold = [.copyLink]
    sharing.copyLink()
    check(sharing.copying && !sharing.canCopyLink && helper.requests.last!.arguments?.shareID == shareID, "Copy Link selects the exact share")
    helper.deliver(["result": link])
    check(copied == [linkURL] && sharing.notice == "Link copied. Anyone with this link can watch until 23:15.", "the link reaches only the pasteboard")
    helper.hold = []
    helper.replies[.copyLink] = ["result": ["url": "https://view.example.test/s/other#token"]]
    sharing.copyLink()
    check(copied.count == 1 && sharing.problem != nil, "unexpected link material is not copied")
    helper.hold = [.revoke]
    sharing.stopSharing()
    check(sharing.phase == .stopping && helper.requests.last!.arguments?.shareID == shareID && !sharing.canStopSharing && !sharing.canCopyLink,
          "Stop Sharing revokes immediately without confirmation")
    helper.emit(view("available", media: "live", viewers: 1))
    check(sharing.phase == .stopping, "a stale active event cannot revive a share being stopped")
    var revoked = view("revoked"); revoked["viewer_limit"] = 1
    helper.deliver(["result": revoked])
    helper.emit(stoppedView)
    check(sharing.phase == .ended(.revoked) && sharing.headline == "Sharing stopped" && sharing.shareButtonTitle == "Share Again" && sharing.canShare,
          "a stopped share offers Share Again")
    check(sharing.canEditOptions && !sharing.setupLockedByShare && sharing.canEditSetup, "options and settings unlock after the share ends")
    helper.hold = []
    let secondID = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
    var second = object(examples["created"]); second["share"] = view("available", id: secondID)
    helper.replies[.create] = ["result": second]
    sharing.shareCamera()
    check(helper.requests.last!.arguments?.shareID == nil && sharing.share?.shareID == secondID && sharing.phase == .active,
          "Share Again issues a new invitation")
    now = SharingModel.date("2026-11-17T23:15:00Z")!
    helper.emit(stoppedView)
    check(sharing.phase == .ended(.expired) && sharing.headline == "Link expired" && sharing.detail!.contains("23:15"), "expiry ends the share")
    now = start
    sharing.shareCamera()
    now = SharingModel.date("2026-11-17T23:15:01Z")!
    sharing.tick()
    check(sharing.phase == .ended(.expired), "a passed deadline ends the local view without an event")
    now = start
    sharing.shareCamera()
    sharing.observe(captureLive: false, captureStopped: false)
    check(sharing.phase == .stopping && sharing.detail == "Sharing ends when the camera stops.", "Stop Camera ends sharing truthfully")
    helper.emit(stoppedView)
    check(sharing.phase == .ended(.cameraStopped) && !sharing.canShare && sharing.shareBlockedReason!.contains("Start the camera"),
          "a camera stop ends the share and Share Again waits for capture")
    // Stop Camera while a link is still being created: the owner pipe answers
    // the create first, then the stop.
    sharing.observe(captureLive: true, captureStopped: false)
    helper.hold = [.create]
    sharing.shareCamera()
    sharing.observe(captureLive: false, captureStopped: false)
    check(sharing.phase == .pending && sharing.detail == "Sharing ends when the camera stops." && !sharing.showsLinkControls,
          "a link being created stays pending while the camera stops")
    helper.deliver(["result": second])
    check(sharing.phase == .stopping && sharing.share?.shareID == secondID && !sharing.canCopyLink && !sharing.canStopSharing,
          "a link created while the camera stops ends with it")
    sharing.observe(captureLive: false, captureStopped: true)
    check(sharing.phase == .ended(.cameraStopped) && !sharing.showsLinkControls && sharing.shareButtonTitle == "Share Again",
          "Stop Camera during link creation ends sharing instead of leaving it stopping")
    sharing.observe(captureLive: true, captureStopped: false)
    sharing.shareCamera()
    sharing.observe(captureLive: false, captureStopped: true)
    check(sharing.phase == .ended(.cameraStopped), "a camera stop before the create reply ends the pending link")
    helper.deliver(["result": second])
    check(sharing.phase == .ended(.cameraStopped) && sharing.share == nil && !sharing.showsLinkControls,
          "a late create reply cannot revive a link the camera stop ended")
    helper.hold = []
    sharing.observe(captureLive: true, captureStopped: false)
    helper.replies[.create] = ["error": ["code": "camera_unavailable", "message": "PRIVATE-DETAIL", "retryable": true]]
    sharing.shareCamera()
    check(sharing.phase == .failed && sharing.problem == "Start the camera, then share again." && sharing.shareButtonTitle == "Share Again",
          "create failure is actionable and leaks no helper text")
    helper.replies[.create] = ["result": second]
    sharing.shareCamera()
    helper.replies[.revoke] = ["error": ["code": "rate_limited", "message": "x", "retryable": true]]
    helper.replies[.status] = ["result": view("available", id: secondID)]
    sharing.stopSharing()
    check(sharing.phase == .active && sharing.problem == "Wait a moment, then try again." && helper.methods.last == .status,
          "a failed Stop Sharing stays stoppable and reads back status")
    helper.replies[.copyLink] = ["error": ["code": "expired", "message": "x", "retryable": false]]
    sharing.copyLink()
    check(sharing.phase == .ended(.expired) && sharing.problem == nil, "an expired Copy Link ends the share and clears stale errors")
    sharing.shareCamera()
    sharing.disconnect()
    check(sharing.phase == .ended(.interrupted) && sharing.headline == "Sharing ended" && !sharing.canShare && !sharing.canStopSharing,
          "owner loss ends sharing and disables actions")

    // Managed public/private capabilities render through the same contract.
    do {
        let managed = FixtureSharingHelper()
        managed.replies[.setup] = ["result": modes["managed_public_private"]!]
        managed.replies[.status] = ["result": stoppedView]
        let value = model(managed)
        value.connect()
        check(value.ingressOptions.map(\.mode) == ["disabled", "external_https", "tailscale_funnel", "tailscale_serve"],
              "every registered mode renders")
        check(value.ingressOptions[3].label == "Tailscale Serve (private tailnet)" && value.selectedAudience == .privateTailnet && value.ingressReady,
              "the effective private audience is shown")
        value.observe(captureLive: true, captureStopped: false)
        check(value.preShareNotice.hasPrefix("Anyone on your tailnet with the link"), "private audience before sharing")
        value.form.ingressMode = "tailscale_funnel"
        check(value.preShareNotice.hasPrefix("Anyone on your tailnet with the link"),
              "the pre-share notice follows the saved method, not an unsaved choice")
        value.form.ingressMode = "tailscale_serve"
        managed.replies[.create] = ["result": ["share": object(modes["private_changed"])["result"]!, "link": link]]
        value.shareCamera()
        check(value.audienceNotice == "Anyone on your tailnet with this link can watch until 23:15.", "private audience after sharing")
        check(value.headline == "Shared on your tailnet until 23:15" && value.scopeBadge?.title == "Tailnet link",
              "a tailnet link is labeled as private to the tailnet")
        let none = FixtureSharingHelper()
        none.replies[.setup] = ["result": examples["setup_empty"]!]
        none.replies[.status] = ["result": stoppedView]
        let off = model(none)
        off.connect()
        off.observe(captureLive: true, captureStopped: false)
        check(off.ingressOptions.map(\.mode) == ["disabled"] && off.registeredModes.isEmpty && !off.canShare, "no registered mode leaves sharing off")
    }

    // Each presented state has a distinct accessible status.
    do {
        let states = FixtureSharingHelper()
        states.replies[.setup] = ["result": readyRelay]
        states.replies[.status] = ["result": stoppedView]
        states.replies[.create] = ["result": examples["created"]!]
        let value = model(states)
        value.connect()
        value.observe(captureLive: true, captureStopped: false)
        var seen: [String: String] = ["stopped": value.headline]
        states.hold = [.create]; value.shareCamera(); seen["pending"] = value.headline
        states.deliver(["result": examples["created"]!]); seen["active"] = value.headline + " " + (value.mediaLabel ?? "")
        states.emit(view("degraded", media: "unavailable")); seen["degraded"] = value.headline + " " + (value.mediaLabel ?? "")
        check(value.activityLine == "Shared publicly · until 23:15 · 0 of 1 viewers · Connection problem", "the menu bar shows a degraded share")
        states.hold = [.revoke]; value.stopSharing(); seen["stopping"] = value.headline
        states.deliver(["result": view("revoked")]); seen["revoked"] = value.headline
        states.hold = []; value.shareCamera(); now = SharingModel.date("2026-11-17T23:16:00Z")!; value.tick(); seen["expired"] = value.headline
        now = start
        check(seen.count == 7 && Set(seen.values).count == 7 && seen.values.allSatisfy { !$0.isEmpty },
              "stopped, pending, active, degraded, stopping, revoked and expired have distinct status text")
    }

    // The app model wires sharing to explicit capture and helper ownership.
    do {
        let camera = FixtureSharingCamera()
        camera.sharing.replies[.setup] = ["result": modes["core"]!]
        camera.sharing.replies[.status] = ["result": stoppedView]
        camera.sharing.replies[.create] = ["result": examples["created"]!]
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let app = CameraModel(client: camera, permissions: FixturePermissions(), directory: directory)
        app.sharing.clock = { start }
        app.sharing.locale = Locale(identifier: "en_GB")
        app.sharing.timeZone = TimeZone(identifier: "UTC")!
        app.launch()
        defer { _ = app.quit() }
        app.section = 2
        check(app.sharing.supported && app.sharing.ingressReady && !camera.methods.contains("start") && !app.sharing.canShare,
              "opening Sharing never starts capture")
        app.sharing.shareCamera()
        check(!camera.methods.contains("sharing_create"), "Share is unavailable until Start Camera")
        app.start()
        camera.status = ["state": "streaming", "frames": 30]
        app.tick()
        check(app.capture.phase == .streaming && app.sharing.canShare, "live capture enables Share")
        app.sharing.shareCamera()
        check(app.sharing.phase == .active && app.sharing.activityLine == "Shared publicly · until 23:15 · 0 of 1 viewers",
              "the menu reports the active share")
        app.stop()
        check(app.capture.phase == .stopped && app.sharing.phase == .ended(.cameraStopped) && app.sharing.activityLine == nil,
              "Stop Camera ends sharing")
        check(camera.methods.filter { $0 == "start" }.count == 1, "sharing never starts capture")
        app.start()
        camera.status = ["state": "streaming", "frames": 30]
        app.tick()
        app.sharing.shareCamera()
        check(app.sharing.phase == .active, "Share Again follows a new explicit Start Camera")
        camera.sharing.hold = [.hostSleep]
        var acknowledged = false
        app.prepareForSleep { acknowledged = true }
        check(!acknowledged && !app.sharing.canShare && app.sharing.phase == .ended(.interrupted)
                && camera.sharing.methods.last == .hostSleep, "sleep inhibits sharing and waits for owner acknowledgement")
        camera.sharing.emit(view("available"))
        check(!app.sharing.hasShare, "pre-sleep status cannot restore the link")
        camera.sharing.deliver(["result": [:]])
        check(acknowledged && app.capture.phase == .streaming, "sleep revocation preserves independent capture")
        app.wakeFromSleep()
        check(app.sharing.canShare && !app.sharing.hasShare, "wake enables explicit Share Again without restoring a link")
        app.sharing.shareCamera()
        camera.onFailure?()
        check(app.sharing.phase == .ended(.interrupted) && !app.sharing.connected, "helper loss ends sharing")
    }

    do {
        let camera = FixtureSharingCamera()
        camera.sharing.replies[.setup] = ["result": modes["core"]!]
        camera.sharing.replies[.status] = ["result": stoppedView]
        camera.sharing.replies[.hostSleep] = ["error": ["code": "media_unreachable"]]
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let app = CameraModel(client: camera, permissions: FixturePermissions(), directory: directory)
        app.launch()
        defer { _ = app.quit() }
        var acknowledged = false
        app.prepareForSleep { acknowledged = true }
        check(acknowledged && !app.ready && !app.sharing.connected,
              "a failed sleep command closes the owner and waits for its exit")
    }
}

// The real HelperClient against an NDJSON peer replaying the published fixtures.
func runSharingTransportTests(fixture: URL, check: @escaping (Bool, String) -> Void) throws {
    let script = fixture.deletingLastPathComponent().appendingPathComponent("fake-sharing-helper.py")
    let now = SharingModel.date("2026-11-17T22:30:00Z")!
    for mode in ["contract", "legacy", "unexpected-event"] {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let directory = root.appendingPathComponent(mode)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let client = HelperClient()
        let sharing = SharingModel(helper: client)
        sharing.clock = { now }
        var copied: [String] = [], failures = 0, exits = 0
        sharing.writeLink = { copied.append($0) }
        client.onReady = { sharing.connect() }
        client.onFailure = { failures += 1; sharing.disconnect() }
        client.onExit = { exits += 1; sharing.disconnect() }
        client.launch(executable: script, directory: directory)
        func wait(_ done: () -> Bool) -> Bool {
            let deadline = Date().addingTimeInterval(8)
            while !done() && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.02)) }
            return done()
        }
        switch mode {
        case "contract":
            check(wait { sharing.setup != nil } && sharing.supported && client.sharingVersion == 1 && sharing.setupStatus == "Setup needed",
                  "helper setup readback decodes over the pipe")
            sharing.form.ingressMode = "external_https"; sharing.form.publicBaseURL = "https://view.example.test"
            sharing.form.guestListen = "127.0.0.1:8890"; sharing.form.relayMode = "cloudflare_turn"
            sharing.form.keyID = "fixture-key"; sharing.form.apiToken = "fixture-write-only-token"
            sharing.saveSetup()
            check(wait { !sharing.setupWorking } && sharing.setup?.ingressState == .ready && sharing.setupProblem == nil,
                  "the piped configure request matches the published request")
            sharing.testRelay()
            check(wait { !sharing.testingRelay } && sharing.relayProblem != nil, "piped relay failure is actionable")
            sharing.testRelay()
            check(wait { !sharing.testingRelay } && sharing.relayReady, "piped relay test reports Ready")
            sharing.observe(captureLive: true, captureStopped: false)
            sharing.duration = .fifteenMinutes
            sharing.shareCamera()
            check(wait { sharing.share?.media == .live } && sharing.phase == .active && sharing.viewerLine == "1 of 1 viewer watching",
                  "create reply and sharing_changed event drive status")
            sharing.copyLink()
            check(wait { !copied.isEmpty } && copied.count == 1 && copied[0].hasPrefix("https://view.example.test/s/"), "Copy Link copies the helper link")
            sharing.stopSharing()
            check(wait { sharing.phase == .ended(.revoked) }, "Stop Sharing revokes over the pipe")
            RunLoop.current.run(until: Date().addingTimeInterval(0.1))
            check(sharing.phase == .ended(.revoked) && failures == 0, "the post-revoke stopped event keeps the ended state")
            client.shutdown()
            check(wait { exits == 1 } && failures == 0, "sharing helper exits cleanly on owner close")
            let lines = try String(contentsOf: directory.appendingPathComponent("requests.jsonl"), encoding: .utf8).split(separator: "\n")
            let carrying = lines.filter { $0.contains("fixture-write-only-token") }
            check(carrying.count == 1 && carrying[0].contains("sharing_configure"), "the write-only relay token crosses the pipe once, in configure")
            check(!lines.contains { $0.contains("\"start\"") }, "sharing never starts capture")
        case "legacy":
            check(wait { sharing.connected } && !sharing.supported && client.sharingVersion == 0, "a helper without sharing hides it")
            sharing.observe(captureLive: true, captureStopped: false)
            sharing.shareCamera(); sharing.loadSetup(); sharing.testRelay()
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
            client.shutdown()
            check(wait { exits == 1 } && failures == 0, "no sharing request reaches a helper without the capability")
        default:
            check(wait { exits == 1 } && failures == 1 && !sharing.supported, "an unadvertised sharing event fails the transport closed")
        }
    }
}
