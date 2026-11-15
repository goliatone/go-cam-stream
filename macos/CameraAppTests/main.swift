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
check(model.canStop && !model.canStart && !model.canExport, "live controls remain consistent")
model.capture.stop()
check(!model.canStop && !model.canStart && !model.canExport, "cleanup blocks repeat actions")
model.capture.stopped()
model.working = true
check(!model.canSave && !model.canStart && !model.canExport, "pending device/config work cannot trigger capture")
model.working = false
check(model.canStart, "idle controls recover")

let fixture = URL(fileURLWithPath: CommandLine.arguments[1])
for mode in ["normal", "bad-version", "wrong-id", "oversized", "eof"] {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    let directory = root.appendingPathComponent(mode)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    let client = HelperClient()
    var failures = 0, replies: [Int] = [], exited = false
    client.onFailure = { failures += 1 }
    client.onExit = { exited = true }
    client.onReady = {
        if mode == "eof" { return }
        for index in 0..<3 {
            client.request("status") { _ in
                replies.append(index)
                if replies.count == 3 { client.shutdown() }
            }
        }
    }
    client.launch(executable: fixture, directory: directory)
    let deadline = Date().addingTimeInterval(8)
    while !exited && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.02)) }
    check(exited, "\(mode) helper exits within deadline")
    if mode == "normal" {
        check(replies == [0, 1, 2] && failures == 0, "serialized correlated replies")
        check(FileManager.default.fileExists(atPath: directory.appendingPathComponent("cleaned").path), "Quit drains stdout until helper cleanup")
    } else { check(failures == 1 && replies.isEmpty, "\(mode) fails closed without delivering untrusted response") }
    try FileManager.default.removeItem(at: root)
}
print("PASS: \(checks) total native state/transport checks (no camera access)")
