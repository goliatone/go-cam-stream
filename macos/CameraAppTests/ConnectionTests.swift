import Foundation

/// Connection Details: the server checklist leads with the export, marks only
/// steps the app can observe, and the RTSP address copy points to the password.
func runConnectionTests(check: @escaping (Bool, String) -> Void) throws {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: directory) }
    let helper = FixtureCameraHelper()
    let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory)
    var copied: [String] = []
    model.writeClipboard = { copied.append($0) }
    model.launch()
    defer { _ = model.quit() }

    var steps = model.connectionSteps
    check(steps.map(\.title) == ["Export the connection files",
                                 "On your server, choose Add Camera → Camstream Mac app",
                                 "Choose Continue, then Check connection, Save camera and Watch now",
                                 "Keep this camera running while you test and watch"],
          "Connection Details starts with the export, followed by the server steps")
    check(steps[0].done == false && steps[1].done == nil && steps[2].done == nil && steps[3].done == false,
          "only the export and a running camera can be marked, and neither is yet")
    check(steps[0].detail?.contains("keep it private") == true, "the export step says the .env file holds the password")

    model.export()
    steps = model.connectionSteps
    check(helper.methods.last == "export" && model.exportCurrent && steps[0].done == true && steps[1].done == nil && steps[2].done == nil,
          "a successful export marks only its own step")
    _ = model.capture.start()
    model.capture.status("streaming", frames: 1)
    check(model.connectionSteps[3].done == true && model.connectionSteps[3].detail == "The camera is running.",
          "a running camera marks the last step")

    model.copyEndpoint()
    check(copied == [model.saved!.endpoint] && copied[0].hasPrefix("rtsp://"),
          "Copy RTSP Address copies only the address")
    check(model.notice == "RTSP address copied. The password is only in the exported .env file.",
          "the copy notice points to the exported password")

    model.stop()
    model.portText = "8555"
    model.save()
    check(model.saved?.port == 8555 && !model.exportCurrent && model.connectionSteps[0].done == false,
          "saving new settings asks for a fresh export")
}
