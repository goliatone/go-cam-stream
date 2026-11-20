import Foundation

// Call activity and host setup state on the native side. Fixture helpers
// only: no WebKit page load, device or permission prompt is involved.
func runCallActivityTests(examples: [String: Any], check: (Bool, String) -> Void) {
    let waiting = examples["waiting"] as! [String: Any]
    let room = waiting["room_id"] as! String
    let valid: [String: Any] = ["version": 1, "room": room, "role": "host", "phase": "waiting",
                                "camera": true, "microphone": false, "camera_label": " FaceTime HD Camera "]

    // The page report is a closed, versioned payload for one room and role.
    let accepted = ConversationCallState(payload: valid, room: room, role: .host)
    check(accepted == ConversationCallState(role: .host, phase: .waiting, camera: true, microphone: false,
                                            cameraLabel: "FaceTime HD Camera"),
          "a bound call page reports role, phase, inputs and a trimmed device name")
    var minimal = valid
    minimal.removeValue(forKey: "camera_label")
    check(ConversationCallState(payload: minimal, room: room, role: .host)?.cameraLabel == nil,
          "device names are optional")
    func variant(_ key: String, _ value: Any?) -> [String: Any] {
        var payload = valid
        payload[key] = value
        return payload
    }
    let rejected: [(String, Any)] = [
        ("another room", variant("room", String(repeating: "B", count: 43))),
        ("another role", variant("role", "guest")),
        ("an unknown phase", variant("phase", "ended")),
        ("an unsupported version", variant("version", 2)),
        ("a fractional version", variant("version", 1.5)),
        ("a boolean version", variant("version", true)),
        ("a string version", variant("version", "1")),
        ("a numeric camera flag", variant("camera", 1)),
        ("a string microphone flag", variant("microphone", "false")),
        ("an admission token", variant("token", "fixture-grant")),
        ("an invitation", variant("invitation_url", "https://call.example.test/r/" + room + "#secret")),
        ("an oversized device name", variant("camera_label", String(repeating: "é", count: 65))),
        ("a control character", variant("microphone_label", "Mic\u{7}")),
        ("a non-text device name", variant("camera_label", 7)),
        ("a missing phase", variant("phase", nil)),
        ("a list", [valid]),
    ]
    for (name, payload) in rejected {
        check(ConversationCallState(payload: payload, room: room, role: .host) == nil, "call state rejects \(name)")
    }
    check(ConversationCallState(payload: variant("camera_label", "  "), room: room, role: .host)?.cameraLabel == nil,
          "a blank device name is treated as unknown")

    // Only the main frame of the bound page may use the bridge.
    let page = URL(string: "https://call.example.test/r/" + room)!
    let local = URL(string: "http://127.0.0.1:8123/r/" + room)!
    check(ConversationMediaSurface.trusted(page: page, mainFrame: true, url: page, scheme: "https", host: "call.example.test", port: 0) &&
          ConversationMediaSurface.trusted(page: local, mainFrame: true, url: local, scheme: "http", host: "127.0.0.1", port: 8123),
          "the bound page's main frame is trusted")
    let frames: [(String, Bool, URL?, String, String, Int)] = [
        ("a subframe", false, page, "https", "call.example.test", 0),
        ("another origin", true, page, "https", "evil.example.test", 0),
        ("another scheme", true, page, "http", "call.example.test", 0),
        ("another port", true, page, "https", "call.example.test", 8443),
        ("another room path", true, URL(string: "https://call.example.test/r/other")!, "https", "call.example.test", 0),
        ("a query", true, URL(string: page.absoluteString + "?debug=1")!, "https", "call.example.test", 0),
        ("a fragment", true, URL(string: page.absoluteString + "#host=grant")!, "https", "call.example.test", 0),
        ("an unknown URL", true, nil, "https", "call.example.test", 0),
    ]
    for (name, main, url, scheme, host, port) in frames {
        check(!ConversationMediaSurface.trusted(page: page, mainFrame: main, url: url, scheme: scheme, host: host, port: port),
              "the call bridge rejects \(name)")
    }

    // Setup classification decodes leniently and maps to specific recovery text.
    func fault(_ value: Any?) -> ConversationFault? {
        guard let value, let data = try? JSONSerialization.data(withJSONObject: value) else { return nil }
        return try? JSONDecoder().decode(ConversationFault.self, from: data)
    }
    let missing = fault((examples["setup_missing"] as? [String: Any])?["error"])
    check(missing?.code == "media_unreachable" && missing?.setup == .notConfigured && missing?.retryable == false,
          "the published setup fixture decodes as missing setup")
    check(fault(["code": "media_unreachable", "retryable": true])?.setup == nil &&
          fault(["code": "media_unreachable", "retryable": true, "setup": "relay_rotation"])?.setup == nil &&
          fault(["code": "room_full"])?.retryable == false,
          "older and newer helpers fall back to an unknown setup cause")
    let texts = [ConversationSetup.notConfigured, .invalidConfiguration, .runtimeUnavailable, .startFailed].map {
        CameraModel.conversationProblem(code: "media_unreachable", setup: $0, role: .host)
    }
    check(Set(texts).count == 4 && texts[0].contains("setup guide") && texts[1].contains("setup guide") &&
          texts[2].contains("Try again") && texts[3].contains("Try again"),
          "each setup cause has its own recovery text")
    check(CameraModel.conversationProblem(code: "permission_denied", setup: .notConfigured, role: .guest).contains("trust the host's server"),
          "a guest without call setup is told to set up joining")
    let neutral = CameraModel.conversationProblem(code: "media_unreachable", setup: nil, role: .host)
    check(neutral.contains("Try again") && !neutral.contains("settings.json") && !neutral.contains("/") &&
          CameraModel.conversationProblem(code: nil, setup: nil, role: .host) == neutral,
          "an unclassified host failure gets neutral recovery text")
    check(CameraModel.conversationProblem(code: "room_full", setup: nil, role: .guest).contains("already has a guest") &&
          CameraModel.conversationProblem(code: "invitation_invalid", setup: nil, role: .guest).contains("trusted server"),
          "guest invitation failures keep their specific recovery text")

    // Model binding, browser hosting and resets.
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    let helper = FixtureSharingCamera()
    var opened: URL?
    let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory,
                            openBrowser: { opened = $0; return true })
    model.launch()
    let created: [String: Any] = ["result": [
        "page_url": page.absoluteString,
        "invitation_url": page.absoluteString + "#invitation-secret",
        "admission": ["room": waiting, "role": "host", "token": "host-fixture-grant"],
    ]]

    helper.conversationReply = examples["setup_missing"] as! [String: Any]
    model.startConversation()
    check(model.conversationSurface == nil && model.conversationSetup == .notConfigured &&
          model.conversationMessage == texts[0], "a host without call setup sees the setup cause")
    helper.conversationReply = ["error": ["code": "media_unreachable", "retryable": true]]
    model.startConversation()
    check(model.conversationSetup == nil && model.conversationMessage == neutral, "an older helper reply clears the stale cause")

    helper.conversationReply = created
    check(model.conversationActivity == nil, "no conversation has no call activity")
    model.startConversation()
    guard let surface = model.conversationSurface, let prejoin = model.conversationPrejoin else {
        check(false, "a host start opens the call view"); return
    }
    check(model.conversationSetup == nil &&
          model.conversationActivity == ConversationActivity(role: .host, phase: .unknown, camera: false, microphone: false),
          "an unreported call shows unknown phase with devices off while the page holds no lease")
    let report = ConversationCallState(role: .host, phase: .waiting, camera: true, microphone: true,
                                       cameraLabel: "FaceTime HD Camera", microphoneLabel: "MacBook Microphone")
    check(model.reportConversationState(report, from: surface) &&
          model.conversationActivity == ConversationActivity(role: .host, phase: .waiting, camera: false, microphone: false),
          "a page cannot claim active inputs without the native lease")
    guard let lease = model.claimConversationCapture(stop: {}) else { check(false, "released capture grants a lease"); return }
    check(model.conversationActivity == ConversationActivity(role: .host, phase: .waiting, camera: true, microphone: true,
                                                              cameraLabel: "FaceTime HD Camera", microphoneLabel: "MacBook Microphone"),
          "a leased page reports its live inputs and device names")
    let projection = String(reflecting: model.conversationActivity!)
    check(!projection.contains("host-fixture-grant") && !projection.contains("invitation-secret") && !projection.contains(room),
          "the activity projection carries no admission, invitation or room identifier")
    check(!model.reportConversationState(ConversationCallState(role: .guest, phase: .connected, camera: false, microphone: false), from: surface),
          "a report for another role is rejected")
    let stale = ConversationMediaSurface(prejoin: prejoin, camera: model) { _ in }!
    check(!model.reportConversationState(ConversationCallState(role: .host, phase: .connected, camera: false, microphone: false), from: stale) &&
          model.conversationActivity?.phase == .waiting, "a view that is not the open call cannot report")
    stale.close()
    model.releaseConversationCapture(lease)
    check(model.conversationActivity?.camera == false && model.conversationActivity?.microphone == false,
          "releasing the lease turns reported inputs off")

    model.closeConversation()
    check(model.conversationActivity == nil && !model.reportConversationState(report, from: surface),
          "closing the call clears its activity and rejects late reports")
    model.startConversation()
    check(model.conversationSurface !== surface && model.conversationActivity?.phase == .unknown,
          "a new call starts without the previous session's activity")
    model.closeConversation()

    model.startConversationInBrowser()
    check(opened != nil && model.hostInBrowser &&
          model.conversationActivity == ConversationActivity(role: .host, phase: .unknown, camera: nil, microphone: nil, hostedInBrowser: true),
          "a browser-hosted room is labeled with unknown inputs until the owner reports")
    helper.conversationReply = ["result": examples["connected"]!]
    model.tick()
    check(model.conversationActivity == ConversationActivity(role: .host, phase: .connected, camera: nil, microphone: nil, hostedInBrowser: true),
          "browser hosting follows the owner's room phase without claiming local inputs")
    helper.onFailure?()
    check(model.conversationActivity == nil && !model.hostInBrowser, "helper loss clears call activity")
}
