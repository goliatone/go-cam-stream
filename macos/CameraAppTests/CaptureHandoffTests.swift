import Foundation

// Call-device handoff after stream stop or failure. Fixture helpers only: no
// device, subprocess or permission prompt is involved.
func runCaptureHandoffTests(conversation: [String: Any], check: (Bool, String) -> Void) {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    let noStop: () -> Void = {}
    func launched() -> (CameraModel, FixtureCameraHelper) {
        let helper = FixtureCameraHelper()
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
        model.launch()
        return (model, helper)
    }
    func streaming(_ model: CameraModel) {
        model.start()
        model.capture.status("streaming", frames: 12)
    }
    func stopCount(_ helper: FixtureCameraHelper) -> Int { helper.methods.filter { $0 == "stop" }.count }
    func failedEvent(_ code: String) -> [String: Any] {
        ["version": 1, "event": "capture_stopped", "result": ["state": "failed"], "error": ["code": code]]
    }

    do { // Healthy stop and an active stream.
        let (model, helper) = launched()
        streaming(model)
        check(model.capture.phase == .streaming && model.claimConversationCapture(stop: noStop) == nil,
              "an active stream keeps call devices busy")
        var granted: UUID?? = .none
        let stops = stopCount(helper)
        model.requestConversationCapture(stop: noStop) { granted = .some($0) }
        check(granted == .some(nil) && stopCount(helper) == stops && model.capture.phase == .streaming,
              "a call device request never stops a live stream")
        model.stop()
        check(model.capture.phase == .stopped && model.capture.releasedForCall, "an acknowledged Stop releases capture")
        let lease = model.claimConversationCapture(stop: noStop)
        check(lease != nil && !model.canStart, "a healthy stop grants the call lease and excludes Start")
        model.releaseConversationCapture(lease!)
        _ = model.quit()
    }

    do { // Refused Start: nothing ran, except possibly for busy.
        let (model, helper) = launched()
        helper.hold = "start"
        model.start()
        helper.deliver(["error": ["code": "not_configured"]])
        check(model.capture.phase == .error && model.capture.releasedForCall, "a refused Start leaves capture released")
        let lease = model.claimConversationCapture(stop: noStop)
        check(lease != nil && model.capture.problem == cameraMessage("not_configured"),
              "a failed start allows call devices and preserves the stream error")
        model.releaseConversationCapture(lease!)
        model.start()
        helper.deliver(["error": ["code": "busy"]])
        helper.hold = nil
        check(model.capture.phase == .error && !model.capture.releasedForCall && model.claimConversationCapture(stop: noStop) == nil,
              "a busy Start leaves ownership unresolved")
        var granted: UUID?
        let stops = stopCount(helper)
        model.requestConversationCapture(stop: noStop) { granted = $0 }
        check(granted != nil && stopCount(helper) == stops + 1 && model.capture.problem == cameraMessage("busy"),
              "the explicit call request confirms cleanup first and keeps the stream error")
        model.releaseConversationCapture(granted!)
        _ = model.quit()
    }

    do { // A capture failure the helper reported after reaping its run.
        let (model, helper) = launched()
        streaming(model)
        let stops = stopCount(helper)
        helper.onEvent?(failedEvent("capture_failed"))
        check(model.capture.phase == .error && model.capture.releasedForCall, "a current-run capture_stopped event confirms cleanup")
        let lease = model.claimConversationCapture(stop: noStop)
        check(lease != nil && stopCount(helper) == stops && model.capture.problem == model.problemText("capture_failed"),
              "a confirmed failure grants call devices without another Stop")
        helper.onEvent?(failedEvent("capture_failed"))
        check(model.capture.phase == .error && model.conversationCapture, "a repeated capture event leaves the call lease intact")
        model.releaseConversationCapture(lease!)
        _ = model.quit()
    }

    do { // A status failure stops capture and waits for the acknowledgement.
        let (model, helper) = launched()
        streaming(model)
        helper.hold = "status"
        model.tick()
        helper.hold = nil
        helper.deliver(["error": ["code": "status_unavailable"]])
        check(model.capture.phase == .error && model.capture.releasedForCall && helper.methods.last == "stop",
              "an acknowledged failure Stop releases capture")
        check(model.claimConversationCapture(stop: noStop) != nil, "call devices work after a stream failure without Save or relaunch")
        _ = model.quit()
    }

    do { // A rejected Stop fails closed until cleanup is confirmed.
        let (model, helper) = launched()
        streaming(model)
        helper.hold = "status"
        model.tick()
        helper.hold = "stop"
        helper.deliver(["error": ["code": "status_unavailable"]])
        check(model.capture.phase == .stopping && !model.capture.releasedForCall, "failure cleanup is pending")
        var waiting: UUID?? = .none
        model.requestConversationCapture(stop: noStop) { waiting = .some($0) }
        check(waiting == nil, "a call request waits while cleanup is in flight")
        helper.deliver(["error": ["code": "invalid_request"]])
        check(model.capture.phase == .error && !model.capture.released && waiting == .some(nil),
              "a rejected Stop leaves ownership unresolved and fails the waiting request closed")
        var retry: UUID?? = .none
        model.requestConversationCapture(stop: noStop) { retry = .some($0) }
        check(retry == nil && helper.methods.last == "stop", "an explicit retry asks the helper to confirm cleanup")
        helper.deliver(["error": ["code": "invalid_request"]])
        check(retry == .some(nil) && !model.conversationCapture, "unconfirmed cleanup keeps call devices blocked")
        helper.hold = nil
        var granted: UUID?
        model.requestConversationCapture(stop: noStop) { granted = $0 }
        check(granted != nil && model.capture.phase == .error && model.capture.problem == cameraMessage("status_unavailable"),
              "confirmed cleanup grants the lease and the stream error stays for recovery")
        _ = model.quit()
    }

    do { // Stale capture events around a user Stop.
        let (model, helper) = launched()
        streaming(model)
        helper.hold = "stop"
        model.stop()
        helper.onEvent?(failedEvent("capture_failed"))
        check(model.capture.phase == .stopping && !model.capture.releasedForCall, "a capture event during Stop cannot release ownership early")
        var granted: UUID?? = .none
        model.requestConversationCapture(stop: noStop) { granted = .some($0) }
        check(granted == nil, "the call request waits for the Stop acknowledgement")
        helper.hold = nil
        helper.deliver(["result": ["state": "stopped"]])
        check(model.capture.phase == .stopped && granted != nil && granted! != nil, "a matched Stop acknowledgement grants the waiting request")
        helper.onEvent?(failedEvent("capture_failed"))
        check(model.capture.phase == .stopped && model.capture.problem == nil && model.conversationCapture,
              "a stale capture event after Stop changes nothing")
        _ = model.quit()
    }

    do { // Helper loss fails pending requests closed.
        let (model, helper) = launched()
        streaming(model)
        helper.hold = "stop"
        model.stop()
        var granted: UUID?? = .none
        model.requestConversationCapture(stop: noStop) { granted = .some($0) }
        helper.onFailure?()
        check(granted == .some(nil) && !model.capture.releasedForCall && model.claimConversationCapture(stop: noStop) == nil,
              "helper loss fails a pending call request closed")
        _ = model.quit()
    }

    do { // Browser hosting uses the same release rule.
        let camera = FixtureSharingCamera()
        var opened: URL?
        let model = CameraModel(client: camera, permissions: FixturePermissions(), directory: directory,
                                openBrowser: { opened = $0; return true })
        model.launch()
        streaming(model)
        camera.onEvent?(failedEvent("capture_failed"))
        let roomID = conversation["room_id"] as! String
        camera.conversationReply = ["result": [
            "page_url": "https://call.example.test/r/" + roomID,
            "invitation_url": "https://call.example.test/r/" + roomID + "#fixture",
            "admission": ["room": conversation, "role": "host", "token": "host-fixture-grant"],
        ]]
        model.startConversationInBrowser()
        check(model.hostInBrowser && opened != nil && model.conversationCapture && model.capture.problem != nil,
              "browser hosting works after a confirmed stream failure")
        model.closeConversation()
        _ = model.quit()
    }
}
