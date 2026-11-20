import Foundation

var checks = 0
func check(_ value: @autoclosure () -> Bool, _ message: String) {
    precondition(value(), message); checks += 1
}
var state = CaptureState()
check(state.phase == .stopped && !state.active, "launch must be stopped")
let first = state.start()
state.status("stopped", frames: 0)
check(state.phase == .starting, "socket startup gap must retain starting")
state.status("streaming", frames: 0)
check(state.phase == .starting, "streaming requires actual frames")
state.status("streaming", frames: 15)
check(state.phase == .streaming && state.frames == 15, "real progress becomes live")
state.stop()
state.started(first)
state.status("streaming", frames: 100)
check(state.phase == .stopping, "late acknowledgement/poll cannot undo Stop")
state.captureEnded(nil)
check(state.phase == .stopping, "await Stop reply before enabling new Start")
state.stopped()
check(!state.active && state.frames == 0, "Stop completion resets progress")
let retry = state.start()
state.captureEnded(cameraMessage("capture_failed"))
state.started(retry)
state.status("stopped", frames: 0)
check(state.phase == .error && state.problem != nil, "failure persists through late ack and idle poll")
_ = state.start()
check(state.phase == .starting && state.problem == nil, "explicit retry clears prior error")
state.failed(cameraMessage("busy"))
check(state.problem!.contains("other Camstream session"), "busy has actionable guidance")
let config = CameraConfig()
let bytes = try JSONEncoder().encode(config)
let decoded = try JSONDecoder().decode(CameraConfig.self, from: bytes)
check(config == decoded && decoded.endpoint == "rtsp://localhost:8554/macbook", "config preserves protocol shape")
check(!String(data: bytes, encoding: .utf8)!.contains("password"), "native config contains no credentials")
print("PASS: \(checks) native state/protocol checks (no camera access)")

let model = CameraModel()
check(!model.canStart && !model.canSave && !model.canExport, "unready helper disables mutations")
model.ready = true
model.permission = "denied"
check(!model.canStart && !model.canSave, "permission denial blocks setup/capture")
model.permission = "restricted"
check(!model.canStart && !model.canSave, "restricted permission blocks setup/capture")
model.permission = "authorized"
check(model.canSave && !model.canStart, "first setup can save but cannot start implicitly")
model.saved = model.config
check(model.canStart && model.canExport && !model.canSave, "saved settings enable explicit capture and export")
model.portText = "80"
check(model.validation != nil && !model.canSave && !model.canStart, "invalid and dirty port disables capture/save")
model.portText = "8554"
model.draft.path = "bad name"
check(model.validation != nil && !model.canSave, "stream name validation")
model.draft.path = "macbook"
model.draft.listen = "0.0.0.0"
check(model.validation != nil && !model.canSave, "network mode requires reachable host")
model.draft.host = "camera.local"
check(model.canSave && !model.canStart, "network changes require explicit save before capture")
model.saved = model.config
_ = model.capture.start()
check(model.canStop && !model.canSave && !model.canExport, "startup locks settings and secrets export but allows Stop")
model.capture.status("streaming", frames: 30)
check(model.canStop && !model.canStart && model.canExport, "live capture allows connection export while settings remain locked")
model.capture.stop()
check(!model.canStop && !model.canStart && !model.canExport, "cleanup blocks repeat actions")
model.capture.stopped()
model.working = true
check(!model.canSave && !model.canStart && !model.canExport, "pending device/config work cannot trigger capture")
model.working = false
check(model.canStart, "idle controls recover")

do {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: directory) }
    let helper = FixtureCameraHelper()
    let live = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
    live.launch()
    _ = live.capture.start()
    live.capture.status("streaming", frames: 42)
    let generation = live.capture.generation
    check(live.canExport && live.exported.isEmpty, "live export available without previous export history")
    live.export()
    check(helper.methods.last == "export" && live.capture.phase == .streaming && live.capture.generation == generation, "live export preserves capture and generation")
    helper.hold = "export"
    live.export()
    check(live.canStop && !live.canExport, "pending export allows explicit Stop and blocks duplicate export")
    helper.deliver(["error": ["code": "export_failed", "message": "PRIVATE-DETAIL"]])
    check(live.capture.phase == .streaming && live.capture.frames == 42 && live.canStop && live.canExport, "export failure leaves capture live and retryable")
    check(live.notice != nil && !live.notice!.contains("PRIVATE-DETAIL"), "export failure uses safe notice")
    for name in ["camstream-camera.yaml", "camstream-camera.env"] {
        let path = directory.appendingPathComponent(name)
        try Data("fixture".utf8).write(to: path)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
    }
    let relaunched = CameraModel(directory: directory)
    relaunched.saved = live.saved
    check(relaunched.exported.count == 2, "existing private files can be revealed after relaunch without export")
    let env = directory.appendingPathComponent("camstream-camera.env")
    try FileManager.default.setAttributes([.posixPermissions: 0o644], ofItemAtPath: env.path)
    check(relaunched.exported.isEmpty, "reveal rejects non-private exports")
    try FileManager.default.removeItem(at: env)
    try FileManager.default.createSymbolicLink(at: env, withDestinationURL: directory.appendingPathComponent("camstream-camera.yaml"))
    check(relaunched.exported.isEmpty, "reveal rejects symlink exports")
    check(!helper.methods.contains("stop"), "export never implicitly stops capture")
    _ = live.quit()
}

let fixture = URL(fileURLWithPath: CommandLine.arguments[1])
for mode in ["normal", "bad-version", "wrong-id", "oversized", "eof", "exit-before-eof"] {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    let directory = root.appendingPathComponent(mode)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    let client = HelperClient()
    var failures = 0, replies: [Int] = [], exits = 0
    client.onFailure = {
        failures += 1
        // UI failure handling may synchronously cancel/reconcile another action.
        if failures == 1 { client.request("status") { _ in replies.append(99) } }
    }
    client.onExit = { exits += 1 }
    client.onReady = {
        if mode == "eof" || mode == "exit-before-eof" { return }
        for index in 0..<3 {
            client.request("status") { _ in
                replies.append(index)
                if replies.count == 3 { client.shutdown() }
            }
        }
    }
    client.launch(executable: fixture, directory: directory)
    let deadline = Date().addingTimeInterval(8)
    while exits == 0 && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.02)) }
    check(exits == 1, "\(mode) helper exits within deadline exactly once")
    // A queued UI action after helper death must not revive the transport or
    // report failure/exit again, regardless of EOF/termination callback order.
    client.request("status") { _ in replies.append(99) }
    RunLoop.current.run(until: Date().addingTimeInterval(0.1))
    check(exits == 1 && !client.idle, "\(mode) remains closed after exit and late request")
    if mode == "normal" {
        check(replies == [0, 1, 2] && failures == 0, "serialized correlated replies")
        check(FileManager.default.fileExists(atPath: directory.appendingPathComponent("cleaned").path), "Quit drains stdout until helper cleanup")
    } else { check(failures == 1 && replies.isEmpty, "\(mode) fails closed without delivering untrusted response") }
    try FileManager.default.removeItem(at: root)
}
print("PASS: \(checks) total native state/transport checks (no camera access)")

// Shared Go/Swift wire fixture and hostile header boundaries.
func fromHex(_ value: String) -> Data {
    var bytes = Data(); var index = value.startIndex
    while index < value.endIndex {
        let next = value.index(index, offsetBy: 2)
        bytes.append(UInt8(value[index..<next], radix: 16)!); index = next
    }
    return bytes
}
let repo = fixture.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
let wireURL = repo.appendingPathComponent("internal/maccamera/testdata/preview-frame-v1.json")
let wire = try JSONSerialization.jsonObject(with: Data(contentsOf: wireURL)) as! [String: String]
let headerBytes = fromHex(wire["header_hex"]!)
let generation = UInt64(wire["generation"]!, radix: 16)!
let header = PreviewHeader(headerBytes, generation: generation, after: 0)
check(header?.width == 2 && header?.height == 1 && header?.size == 8, "Go wire fixture decodes")
check(header?.image(fromHex(wire["payload_hex"]!)) != nil, "BGRA fixture renders")
check(header?.image(Data([0])) == nil, "partial pixels cannot render")
let frameTime = Double(header!.timestamp) / 1000
check(header!.isFresh(at: Date(timeIntervalSince1970: frameTime + 4.999)), "frame remains fresh inside age limit")
check(!header!.isFresh(at: Date(timeIntervalSince1970: frameTime + 5)), "frame expires at age limit")
check(header!.isFresh(at: Date(timeIntervalSince1970: frameTime - 5)), "bounded clock skew accepted")
check(!header!.isFresh(at: Date(timeIntervalSince1970: frameTime - 5.001)), "excessive future timestamp rejected")
check(PreviewHeader(headerBytes.prefix(47), generation: generation, after: 0) == nil, "partial header rejected")
check(PreviewHeader(headerBytes, generation: generation + 1, after: 0) == nil, "stale generation rejected")
check(PreviewHeader(headerBytes, generation: generation, after: 42) == nil, "replayed sequence rejected")
for offset in [0, 4, 6, 32, 36, 40, 44] {
    var corrupt = headerBytes; corrupt[offset] = 255
    check(PreviewHeader(corrupt, generation: generation, after: 0) == nil, "malformed header at \(offset) rejected before pixels")
}

// Delay attach across visibility/capture changes; old replies must detach first.
let preview = PreviewController()
var requests: [(String, String?, ([String: Any]) -> Void)] = []
preview.request = { method, generation, done in requests.append((method, generation, done)) }
preview.update(wanted: false, ready: true, supported: true, captureGeneration: 1)
check(requests.isEmpty, "opening stopped preview cannot start capture")
preview.update(wanted: true, ready: true, supported: true, captureGeneration: 1)
check(requests.count == 1 && requests[0].0 == "preview_attach", "live visible preview attaches")
preview.update(wanted: false, ready: true, supported: true, captureGeneration: 2)
let endpoint: [String: Any] = ["version": 1, "socket": "/tmp/unused", "generation": "0123456789abcdef"]
requests.removeFirst().2(["result": endpoint])
check(requests.count == 1 && requests[0].0 == "preview_detach" && preview.image == nil, "late attach after Stop only detaches")
preview.update(wanted: true, ready: true, supported: true, captureGeneration: 3)
check(requests.count == 1, "reopen waits for detach")
requests.removeFirst().2(["result": ["detached": true]])
check(requests.count == 1 && requests[0].0 == "preview_attach", "reopen attaches after cleanup")
requests.removeFirst().2(["error": ["code": "preview_unavailable"]])
check(preview.phase == .unavailable && requests.isEmpty, "preview failure has no automatic retry loop")
preview.retry()
check(requests.count == 1 && preview.phase == .connecting, "explicit retry attaches")
preview.update(wanted: false, ready: false, supported: false, captureGeneration: 4)
requests.removeFirst().2(["error": ["code": "transport"]])
check(preview.image == nil && preview.phase == .idle, "helper loss clears preview")

// Real Unix socket reads fragment both header and payload. No camera involved.
for mode in ["fragmented", "malformed", "partial", "cancel", "aged-payload", "queued-stale"] {
    let folder = URL(fileURLWithPath: "/tmp/camstream-camera-\(getuid())-test\(UUID().uuidString.prefix(6))")
    try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
    let socket = folder.appendingPathComponent("preview-0123456789abcdef.sock")
    let process = Process(), output = Pipe()
    process.executableURL = fixture.deletingLastPathComponent().appendingPathComponent("fake-preview.py")
    process.arguments = [socket.path, mode]; process.standardOutput = output
    try process.run()
    _ = output.fileHandleForReading.availableData
    var frames = 0, failures = 0
    let reader = PreviewReader(onFrame: { image in frames += 1; check(image.width == 2 && image.height == 1, "fragmented payload dimensions") }, onFailure: { failures += 1 })
    reader.start(PreviewEndpoint(version: 1, socket: socket.path, generation: "0123456789abcdef"))
    if mode == "cancel" { reader.stop() }
    // Let a valid frame expire while its delivery is queued on the main thread.
    if mode == "queued-stale" {
        _ = output.fileHandleForReading.availableData
        Thread.sleep(forTimeInterval: 1.2)
    }
    let deadline = Date().addingTimeInterval(7)
    while failures == 0 && process.isRunning && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.02)) }
    RunLoop.current.run(until: Date().addingTimeInterval(0.1))
    reader.stop()
    if process.isRunning { process.terminate() }
    process.waitUntilExit()
    check(mode == "fragmented" ? frames == 1 : frames == 0, "\(mode) only complete valid frames render")
    check(mode == "cancel" ? failures == 0 : failures == 1, "\(mode) failure/cancel classification")
    try FileManager.default.removeItem(at: folder)
}
print("PASS: \(checks) total native checks including preview framing and lifecycle (no camera access)")

try runAudioTests(repo: repo, check: { check($0, $1) })
print("PASS: \(checks) native checks including T03 audio fixtures and microphone flows (capture off)")
try runAudioTransportTests(fixture: fixture, check: { check($0, $1) })
print("PASS: \(checks) total native checks including old/new helper audio negotiation (capture off)")

try runSharingTests(repo: repo, check: { check($0, $1) })
print("PASS: \(checks) native checks including T08 sharing states and published contract fixtures (capture off)")
try runSharingTransportTests(fixture: fixture, check: { check($0, $1) })
let conversationExamples = try Data(contentsOf: repo.appendingPathComponent("data/api/conversation_examples.json"))
let conversationViews = try JSONSerialization.jsonObject(with: conversationExamples) as! [String: Any]
for (name, state) in [("waiting", ConversationState.waiting), ("connected", .connected),
                      ("reconnecting", .reconnecting), ("ended", .ended)] {
    let data = try JSONSerialization.data(withJSONObject: conversationViews[name]!)
    let room = try JSONDecoder().decode(ConversationRoomView.self, from: data)
    check(room.state == state && room.version == 1, "conversation state decodes")
}
runCaptureHandoffTests(conversation: conversationViews["waiting"] as! [String: Any], check: { check($0, $1) })
print("PASS: \(checks) native checks including call capture handoff after stream stop or failure")
runCallActivityTests(examples: conversationViews, check: { check($0, $1) })
print("PASS: \(checks) native checks including call activity and host setup state")
let sharingExamplesForActivity = try JSONSerialization.jsonObject(with: Data(contentsOf: repo.appendingPathComponent("data/api/sharing_examples.json"))) as! [String: Any]
runActivityTests(conversations: conversationViews, sharing: sharingExamplesForActivity, check: { check($0, $1) })
print("PASS: \(checks) native checks including shared activity status, menus and end confirmation")
runRecoveryTests(conversations: conversationViews, check: { check($0, $1) })
print("PASS: \(checks) native checks including first-run reasons and recovery actions")
runInvitationTests(conversations: conversationViews, check: { check($0, $1) })
print("PASS: \(checks) native checks including host invitation copy and browser-hosted room state")
try runConnectionTests(check: { check($0, $1) })
print("PASS: \(checks) native checks including the connection checklist and RTSP address copy")
do {
    let fixtureCamera = FixtureSharingCamera()
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    var browserURL: URL?
    let app = CameraModel(client: fixtureCamera, permissions: FixturePermissions(), directory: directory,
                          openBrowser: { browserURL = $0; return true })
    let invitation = "camstream://conversation/join?v=1#fixture"
    let sampleRoom = conversationViews["waiting"] as! [String: Any]
    let sampleRoomID = sampleRoom["room_id"] as! String
    fixtureCamera.conversationReply = ["result": [
        "page_url": "https://call.example.test/r/" + sampleRoomID,
        "admission": ["room": sampleRoom, "role": "guest", "token": "fixture-grant"],
    ]]
    app.openConversationInvitation(invitation)
    check(fixtureCamera.incomingInvitations.isEmpty, "incoming invitation waits for helper readiness")
    app.launch()
    check(fixtureCamera.incomingInvitations == [invitation], "ready helper receives the pending invitation once")
    check(app.conversationPrejoin?.admission.role == .guest && app.conversationPrejoin?.pageURL.fragment == nil,
          "invitation returns a clean prejoin URL and scoped guest admission")
    check(app.section == 3 && app.conversationSurface != nil, "incoming invitation opens the native conversation panel")
    check(!ConversationMediaSurface.validPage(URL(string: "https://call.example.test/r/other")!, room: sampleRoomID),
          "native prejoin rejects another room")
    check(!ConversationMediaSurface.validPage(URL(string: "http://call.example.test/r/" + sampleRoomID)!, room: sampleRoomID),
          "native prejoin rejects insecure remote pages")
    check(!app.conversationCapture && !fixtureCamera.methods.contains("start"), "prejoin leaves the camera stopped")
    app.openConversationInvitation(invitation)
    check(fixtureCamera.incomingInvitations.count == 1, "reopening a pending prejoin does not redeem twice")
    guard let lease = app.claimConversationCapture(stop: {}) else { preconditionFailure("stopped camera should allow a call lease") }
    check(app.conversationCapture && !app.canStart, "call capture excludes independent RTSP start")
    check(app.claimConversationCapture(stop: {}) == nil, "another call cannot take over the capture lease")
    app.releaseConversationCapture(UUID())
    check(app.conversationCapture, "an old owner cannot release the active lease")
    app.releaseConversationCapture(lease)
    check(!app.conversationCapture, "media teardown releases the lease")
    app.closeConversation()
    check(fixtureCamera.methods.contains("conversation_leave"), "closing native guest prejoin releases its seat via the owner pipe")
    fixtureCamera.heldConversationMethods = [.openLink]
    app.openConversationInvitation(invitation)
    app.closeConversation()
    let oldLeaves = fixtureCamera.methods.filter { $0 == "conversation_leave" }.count
    fixtureCamera.pendingConversation.removeValue(forKey: .openLink)?(fixtureCamera.conversationReply)
    fixtureCamera.heldConversationMethods = []
    check(app.conversationSurface == nil && fixtureCamera.methods.filter { $0 == "conversation_leave" }.count == oldLeaves + 1,
          "late redemption after close releases the seat without reopening the panel")
    fixtureCamera.conversationReply = ["result": [
        "page_url": "https://call.example.test/r/" + sampleRoomID,
        "invitation_url": "https://call.example.test/r/" + sampleRoomID + "#fixture",
        "admission": ["room": sampleRoom, "role": "host", "token": "host-fixture-grant"],
    ]]
    app.startConversation()
    check(app.canCopyConversation && app.conversationSurface != nil && !app.conversationCapture,
          "starting a conversation opens the Mac room immediately with capture idle")
    app.closeConversation()
    check(fixtureCamera.methods.contains("conversation_end"), "closing a host view ends the room through its owner")
    let created = fixtureCamera.conversationReply
    app.startConversationInBrowser()
    check(app.hostInBrowser && browserURL?.fragment == "host=host-fixture-grant", "host grants stay in the handoff fragment")
    check(app.conversationCapture && !app.canStart, "browser host reserves the local camera pipeline")
    fixtureCamera.conversationReply = ["result": conversationViews["ended"]!]
    app.tick()
    check(!app.hostInBrowser && app.conversationPrejoin == nil && !app.conversationCapture,
          "browser End returns the Mac host to the chooser and releases capture ownership")

    fixtureCamera.conversationReply = created
    app.prepareConversation()
    check(app.conversationSurface != nil && !app.conversationCapture, "optional device setup opens prejoin with capture idle")
    fixtureCamera.heldConversationMethods = [.end]
    fixtureCamera.sharing.replies[.hostSleep] = ["result": [:]]
    var sleepAcknowledged = false
    app.prepareForSleep { sleepAcknowledged = true }
    check(!sleepAcknowledged && fixtureCamera.pendingConversation[.end] != nil,
          "sleep waits for outstanding conversation revocation")
    fixtureCamera.pendingConversation.removeValue(forKey: .end)?(["result": [:]])
    let sleepDeadline = Date().addingTimeInterval(1)
    while !sleepAcknowledged && Date() < sleepDeadline {
        RunLoop.current.run(until: Date().addingTimeInterval(0.01))
    }
    check(sleepAcknowledged, "sleep acknowledges after conversation and sharing teardown")
    _ = app.quit()
    check(app.conversationPrejoin == nil && app.conversationSurface == nil, "quitting closes the prejoin view and discards admission")
}
print("PASS: \(checks) total native checks including sharing helper transport (capture off)")
