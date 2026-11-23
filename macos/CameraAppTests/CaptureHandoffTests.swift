import Foundation

// Call-device handoff after stream stop or failure. Fixture helpers only: no
// device, subprocess or permission prompt is involved.
func runCaptureHandoffTests(conversation: [String: Any], check: (Bool, String) -> Void) {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    let noStop: () -> Void = {}
    func launched(releaseConfirmed: Bool = false) -> (CameraModel, FixtureCameraHelper) {
        let helper = FixtureCameraHelper()
        let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
        model.launch()
        if releaseConfirmed {
            model.requestConversationCapture(stop: noStop) { if let lease = $0 { model.releaseConversationCapture(lease) } }
        }
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

    do { // A deliberate selection starts a local attempt on a cold launch.
        let (model, helper) = launched()
        check(!model.webcamPreview.ownsCapture, "restoring saved settings leaves local preview idle")
        // The test selects a nonexistent name, so the native path cannot open a camera.
        model.chooseWebcam("Missing test webcam", in: .settings)
        check(model.webcamPreview.surface == .settings && model.webcamPreview.ownsCapture &&
              !helper.methods.contains("start"), "choosing a webcam alone starts preview, not publication")
        model.startCamera()
        check(!helper.methods.contains("start"), "Start Camera waits for the preview to release its device")
        let deadline = Date().addingTimeInterval(2)
        while !helper.methods.contains("start"), Date() < deadline {
            RunLoop.current.run(until: Date().addingTimeInterval(0.01))
        }
        check(helper.methods.contains("start") && !model.webcamPreview.ownsCapture && model.webcamPreview.surface == nil,
              "the publisher starts after the local preview has been cleared and released")
        _ = model.quit()
    }

    do { // The previous supervisor may outlive a cold launch.
        let (model, helper) = launched()
        check(!model.capture.releasedForCall && stopCount(helper) == 0, "cold launch leaves release unconfirmed without issuing Stop")
        helper.hold = "stop"
        var granted: UUID??
        model.requestConversationCapture(stop: noStop) { granted = .some($0) }
        check(granted == nil && stopCount(helper) == 1, "the first device request waits for a lock-backed Stop")
        helper.deliver(["error": ["code": "busy"]])
        check(granted == .some(nil) && !model.conversationCapture, "a cold launch cannot grant a lease while another owner holds capture")
        helper.hold = nil
        model.requestConversationCapture(stop: noStop) { granted = .some($0) }
        check((granted ?? nil) != nil && stopCount(helper) == 2, "retry checks release again and grants the lease when capture is free")
        if let lease = granted ?? nil { model.releaseConversationCapture(lease) }
        _ = model.quit()
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
        let (model, helper) = launched(releaseConfirmed: true)
        helper.hold = "start"
        model.start()
        helper.deliver(["error": ["code": "not_configured"]])
        check(model.capture.phase == .error && model.capture.releasedForCall, "a refused Start leaves capture released")
        let lease = model.claimConversationCapture(stop: noStop)
        check(lease != nil && model.capture.problem == cameraMessage("not_configured") && model.saved == nil,
              "a failed start allows call devices and preserves the stream error")
        model.releaseConversationCapture(lease!)
        model.startCamera()
        check(Array(helper.methods.suffix(2)) == ["configure", "start"],
              "after the camera service reports no settings, Try Again applies them before starting")
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
        let (model, helper) = launched(releaseConfirmed: true)
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
        helper.onFailure?(.disconnected)
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
        let roomID = conversation["room_id"] as! String
        camera.conversationReply = ["result": [
            "page_url": "https://call.example.test/r/" + roomID,
            "invitation_url": "https://call.example.test/r/" + roomID + "#fixture",
            "admission": ["room": conversation, "role": "host", "token": "host-fixture-grant"],
        ]]
        model.startConversationInBrowser()
        check(!model.hostInBrowser && opened == nil && model.conversationNotice?.problem == true,
              "browser hosting explains why it cannot use an occupied camera")
        camera.onEvent?(failedEvent("capture_failed"))
        model.hostConversationInBrowser()
        check(model.hostInBrowser && opened != nil && model.conversationCapture && model.capture.problem != nil && model.conversationNotice == nil,
              "a successful browser handoff clears the call warning and preserves the stream failure")
        model.closeConversation()
        _ = model.quit()
    }
}
