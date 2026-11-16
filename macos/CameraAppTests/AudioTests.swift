import Foundation

func runAudioTests(repo: URL, check: @escaping (Bool, String) -> Void) throws {
    let data = try Data(contentsOf: repo.appendingPathComponent("macos/fixtures/audio-protocol.json"))
    let fixtures = try JSONSerialization.jsonObject(with: data) as! [String: Any]
    func decode<T: Decodable>(_ object: Any) throws -> T { try JSONDecoder().decode(T.self, from: JSONSerialization.data(withJSONObject: object)) }
    let legacy: CameraConfig = try decode(fixtures["legacy_config"]!)
    let disabled: CameraConfig = try decode(fixtures["disabled_config"]!)
    let requests = fixtures["requests"] as! [[String: Any]]
    let enabled: CameraConfig = try decode(requests[1]["config"]!)
    check(!legacy.audioEnabled && legacy.audioDeviceID.isEmpty, "legacy config defaults audio off")
    check(!disabled.audioEnabled && disabled.audioDeviceID == "fixture: mic-1", "off preserves opaque saved ID")
    check(enabled.audioEnabled && enabled.audioDeviceID == "fixture: mic-1", "T03 snake-case configuration decodes")
    let oldObject = try disabled.helperObject(audioSupported: false)
    check(oldObject["audio_enabled"] == nil && oldObject["audio_device_id"] == nil, "old helper receives strictly legacy keys")
    let newObject = try enabled.helperObject(audioSupported: true)
    check(newObject["audio_enabled"] as? Bool == true && newObject["audio_device_id"] as? String == enabled.audioDeviceID, "new helper receives exact selected ID")
    for id in ["", "bad\n", String(repeating: "x", count: 1025)] { check(!MicrophoneDevice.validID(id), "invalid UID rejected") }
    check(MicrophoneDevice.validID("opaque: ID with spaces"), "UID bytes remain opaque")
    let inventory: [MicrophoneDevice] = try decode((fixtures["discovery_reply"] as! [String: Any])["result"]!)
    check(MicrophoneDevice.validInventory(inventory), "duplicate labels accepted")
    check(!MicrophoneDevice.validInventory([inventory[0], inventory[0]]), "duplicate IDs rejected")
    check(inventory[0].label(in: inventory) != inventory[1].label(in: inventory), "duplicate labels have distinguishable stable suffixes")
    check(inventory[0].label(in: inventory) == inventory[0].label(in: inventory.reversed()), "label stable across reorder")
    let composed = "fixture: \u{e9}", decomposed = "fixture: e\u{301}"
    check(composed == decomposed && MicrophoneID(composed) != MicrophoneID(decomposed), "opaque IDs resist Swift canonical equivalence")
    let unicodeDevices = [MicrophoneDevice(id: composed, name: "Same"), MicrophoneDevice(id: decomposed, name: "Same")]
    check(MicrophoneDevice.validInventory(unicodeDevices) && unicodeDevices[0] != unicodeDevices[1] && !unicodeDevices[0].matches(decomposed), "distinct Unicode byte IDs cannot replace each other")
    var byteConfig = enabled; byteConfig.audioDeviceID = composed
    var otherConfig = byteConfig; otherConfig.audioDeviceID = decomposed
    check(byteConfig != otherConfig, "byte-different ID requires a new Save")
    let roundtrip: CameraConfig = try decode(try byteConfig.helperObject(audioSupported: true))
    check(roundtrip.audioDeviceID.utf8.elementsEqual(composed.utf8), "JSON roundtrip preserves UID bytes")
    var capture = CaptureState()
    _ = capture.start(audioEnabled: true)
    capture.status("streaming", frames: 42)
    check(capture.phase == .starting, "video frames alone never make audio-on live")
    for row in fixtures["statuses"] as! [[String: Any]] {
        let audio: MicrophoneStatus = try decode(row["audio"]!)
        if audio.state == "live" { capture.status("streaming", frames: 42, audio: audio) }
    }
    check(capture.phase == .streaming && capture.audioPackets == 73, "T03 actual packet status makes audio live")
    capture.stop(); capture.status("streaming", frames: 50, audio: MicrophoneStatus(enabled: true, state: "live", packets: 90))
    check(capture.phase == .stopping && capture.audioState == "stopping", "late audio status cannot undo Stop")
    capture.stopped(); capture.captureEnded(cameraMessage("audio_stalled"))
    check(capture.phase == .stopped, "late failure after Stop stays stopped")

    let helper = FixtureCameraHelper(), permissions = FixturePermissions()
    let model = CameraModel(client: helper, permissions: permissions)
    model.launch()
    defer { _ = model.quit() }
    check(model.capture.phase == .stopped && permissions.requests.isEmpty && !helper.methods.contains("start"), "launch never requests permission or capture")
    check(!helper.methods.contains("audio_devices") && model.canStart, "audio off ignores microphone access and discovery")
    model.setMicrophoneEnabled(true)
    check(permissions.requests.isEmpty && !model.canStart && !model.canSave, "opt-in shows explicit permission action")
    model.requestMicrophonePermission()
    check(permissions.requests.count == 1 && model.requestingPermission && !model.canEditSettings, "one explicit permission request locks pending actions")
    model.requestMicrophonePermission()
    check(permissions.requests.count == 1, "pending permission cannot prompt twice")
    permissions.resolveAudio("denied")
    check(!model.canSave && model.microphoneSummary.contains("denied"), "denial is visible and blocks audio")
    model.requestMicrophonePermission()
    check(permissions.requests.count == 1, "denial does not reprompt")
    model.setMicrophoneEnabled(false)
    check(model.canStart && model.microphoneSummary == "Microphone off", "denied microphone does not block video-only")
    model.setMicrophoneEnabled(true)
    permissions.audio = "authorized"; model.tick()
    check(model.microphones.count == 2 && model.draft.audioDeviceID.isEmpty && !model.canSave, "grant recovery enumerates without selecting or starting")
    model.selectMicrophone(MicrophoneID("fixture: mic-2"))
    check(model.canSave && !model.canStart, "selection requires Save")
    model.save()
    check(model.canStart && model.capture.phase == .stopped && !helper.methods.contains("start"), "Save stays stopped")
    helper.inventory.reverse(); model.refreshMicrophones()
    check(model.draft.audioDeviceID == "fixture: mic-2" && model.canStart, "reorder cannot change saved selection")
    helper.inventory = [["id": "replacement", "name": "Microphone"], ["id": "prefix", "name": "Microphone Pro"]]
    model.refreshMicrophones()
    check(model.draft.audioDeviceID == "fixture: mic-2" && !model.canStart && model.microphoneValidation != nil, "same-name replacement cannot take over")
    helper.inventory = []; model.refreshMicrophones()
    check(model.microphonesLoaded && model.microphones.isEmpty && !model.canStart, "empty list remains actionable without fallback")
    helper.inventory = [["id": "duplicate", "name": "A"], ["id": "duplicate", "name": "B"]]; model.refreshMicrophones()
    check(!model.microphonesLoaded && model.microphoneDiscoveryError != nil, "ambiguous inventory fails closed")
    helper.inventory = [["id": "fixture: mic-2", "name": "Renamed microphone"]]; model.refreshMicrophones()
    check(model.canStart, "renaming the same ID preserves selection")
    permissions.audio = "restricted"; model.refreshPermission()
    check(!model.canStart && model.microphoneSummary.contains("restricted"), "restricted microphone blocks audio")
    permissions.audio = "authorized"; model.tick()
    model.start()
    check(model.capture.phase == .starting && !model.canEditSettings && !model.canSave, "explicit audio Start locks settings")
    model.setMicrophoneEnabled(false)
    check(model.draft.audioEnabled, "capture locks the microphone toggle")
    helper.status = ["state": "streaming", "frames": 20, "audio": ["enabled": true, "state": "live", "packets": 10]]
    model.tick()
    check(model.capture.phase == .streaming && model.microphoneSummary == "Microphone live", "packet status presents live audio")
    let count = helper.methods.filter { $0 == "start" }.count
    model.setWindowVisible(true); model.setWindowVisible(false); model.setWindowVisible(true)
    check(model.capture.phase == .streaming && helper.methods.filter { $0 == "start" }.count == count && !helper.methods.contains("stop"), "window reopen never restarts or stops capture")
    helper.hold = "status"; model.tick()
    model.stop(); model.start()
    helper.deliver(["result": ["state": "streaming", "frames": 99, "audio": ["enabled": true, "state": "live", "packets": 99]]])
    check(model.capture.phase == .starting && model.capture.audioPackets == 0, "old poll cannot make a newer Start live")
    helper.hold = nil
    helper.status = ["state": "streaming", "frames": 10]
    model.tick()
    check(model.capture.phase == .error && helper.methods.last == "stop", "missing audio status stops ownership before showing failure")
    model.start()
    helper.onEvent?(fixtures["failure_event"] as! [String: Any])
    check(model.capture.phase == .error && model.capture.problem == cameraMessage("audio_stalled"), "shared failure event maps to actionable native error")
    model.start(); permissions.audio = "denied"; model.tick()
    check(model.capture.phase == .error && helper.methods.last == "stop", "revoked microphone access stops active audio")
    model.setMicrophoneEnabled(false); model.save()
    check(model.canStart && !model.saved!.audioEnabled, "explicit disabled save recovers video-only after audio failure")

    let oldHelper = FixtureCameraHelper(), oldPermissions = FixturePermissions()
    oldHelper.audioVersion = 0
    let old = CameraModel(client: oldHelper, permissions: oldPermissions)
    old.launch(); defer { _ = old.quit() }
    old.setMicrophoneEnabled(true); old.requestMicrophonePermission()
    check(!old.audioSupported && !old.draft.audioEnabled && oldPermissions.requests.isEmpty, "old helper disables audio actions")
    check(old.canStart && !oldHelper.methods.contains("audio_devices"), "old helper retains video-only use")
    let savedHelper = FixtureCameraHelper(), savedPermissions = FixturePermissions()
    savedHelper.configuration = enabled; savedPermissions.audio = "authorized"
    let savedModel = CameraModel(client: savedHelper, permissions: savedPermissions)
    savedModel.launch(); defer { _ = savedModel.quit() }
    check(savedModel.canStart && savedModel.capture.phase == .stopped && !savedHelper.methods.contains("start"), "persisted opt-in never starts at launch")
    savedHelper.hold = "start"; savedModel.start(); savedModel.stop()
    savedHelper.deliver(["result": ["state": "starting"]])
    check(savedModel.capture.phase == .stopped, "late Start acknowledgement cannot undo Stop")

    let staleHelper = FixtureCameraHelper(), stalePermissions = FixturePermissions()
    stalePermissions.audio = "authorized"; staleHelper.configuration = enabled
    let stale = CameraModel(client: staleHelper, permissions: stalePermissions)
    stale.launch(); defer { _ = stale.quit() }
    staleHelper.hold = "audio_devices"; stale.refreshMicrophones()
    stalePermissions.audio = "denied"; stale.refreshPermission()
    staleHelper.deliver(["result": [["id": "fixture: mic-1", "name": "Late microphone"]]])
    check(!stale.microphonesLoaded && stale.microphones.isEmpty && !stale.canStart, "late discovery cannot override revoked permission")
    let pendingHelper = FixtureCameraHelper(), pendingPermissions = FixturePermissions()
    let pending = CameraModel(client: pendingHelper, permissions: pendingPermissions)
    pending.launch(); pending.setMicrophoneEnabled(true); pending.requestMicrophonePermission()
    _ = pending.quit()
    let beforeGrant = pendingHelper.methods.count
    pendingPermissions.resolveAudio("authorized")
    check(pendingHelper.methods.count == beforeGrant && !pendingHelper.methods.contains("start"), "permission completion after Quit cannot revive the helper")
    model.start()
    helper.status = ["state": "streaming", "frames": 10, "audio": ["enabled": true, "state": "live", "packets": 1]]
    model.tick()
    check(model.capture.phase == .error && helper.methods.last == "stop", "unexpected audio during video-only intent fails closed")

    // Swift transport against the live Go contract, without configure/discovery/Start.
    if let executable = ProcessInfo.processInfo.environment["CAMSTREAM_NATIVE_HELPER"] {
        let real = HelperClient()
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("camstream-t05-" + UUID().uuidString)
        var done = false, failed = false, stopped = false, legacyDefault = false
        real.onFailure = { failed = true }
        real.onExit = { done = true }
        real.onReady = {
            check(real.audioVersion == 1, "live Go helper advertises audio v1")
            real.request("config") { reply in
                legacyDefault = (reply["error"] as? [String: Any])?["code"] as? String == "not_configured"
                real.request("status") { reply in
                    let status = reply["result"] as? [String: Any]
                    stopped = status?["state"] as? String == "stopped" && (status?["audio"] as? [String: Any])?["enabled"] as? Bool == false
                    real.shutdown()
                }
            }
        }
        real.launch(executable: URL(fileURLWithPath: executable), directory: directory)
        let deadline = Date().addingTimeInterval(10)
        while !done && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.02)) }
        real.shutdown()
        check(done && !failed && stopped && legacyDefault, "live service remains stopped and audio-off")
        try? FileManager.default.removeItem(at: directory)
    }
}

func runAudioTransportTests(fixture: URL, check: @escaping (Bool, String) -> Void) throws {
    for mode in ["audio-contract", "legacy-contract", "future-audio-contract"] {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let directory = root.appendingPathComponent(mode)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let client = HelperClient()
        var done = false, failed = false, answered = false
        client.onFailure = { failed = true }
        client.onExit = { done = true }
        client.onReady = {
            check(client.audioVersion == (mode == "audio-contract" ? 1 : mode == "future-audio-contract" ? 2 : 0), "audio feature version from ready")
            var config = CameraConfig(); config.audioEnabled = mode == "audio-contract"; config.audioDeviceID = "fixture: mic-1"
            client.request("configure", config: config) { reply in
                let result = reply["result"] as? [String: Any]
                check(result != nil, "helper accepts matching configuration shape")
                if mode == "audio-contract" {
                    check(result?["audio_device_id"] as? String == "fixture: mic-1" && result?["audio_enabled"] as? Bool == true, "audio fields survive native transport")
                } else { check(result?["audio_enabled"] == nil && result?["audio_device_id"] == nil, "audio-off legacy shape omits unsupported fields") }
                client.request("audio_devices") { discovery in
                    if mode == "audio-contract" { check((discovery["result"] as? [[String: Any]])?.count == 2, "discovery crosses native transport") }
                    else { check((discovery["error"] as? [String: String])?["code"] == "audio_runtime_unsupported", "unsupported discovery refused locally") }
                    client.request("status") { reply in
                        answered = (reply["result"] as? [String: Any])?["state"] as? String == "stopped"
                        client.shutdown()
                    }
                }
            }
        }
        client.launch(executable: fixture, directory: directory)
        let deadline = Date().addingTimeInterval(8)
        while !done && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.02)) }
        check(done && !failed && answered, "audio capability negotiation preserves transport")
        try FileManager.default.removeItem(at: root)
    }
}
