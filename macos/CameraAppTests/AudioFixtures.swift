import Foundation

// These adapters have no device or subprocess access. They exercise the actual
// model/view without allowing a test interaction to activate either sensor.
final class FixturePermissions: CameraPermissions {
    var video = "authorized"
    var audio = "not_determined"
    var requests: [CameraMedia] = []
    private var completion: (() -> Void)?
    func status(_ media: CameraMedia) -> String { media == .audio ? audio : video }
    func request(_ media: CameraMedia, completion: @escaping () -> Void) {
        requests.append(media); self.completion = completion
    }
    func resolveAudio(_ status: String) { audio = status; let done = completion; completion = nil; done?() }
}

final class FixtureCameraHelper: CameraHelper {
    var onReady: (() -> Void)?
    var onEvent: (([String: Any]) -> Void)?
    var onFailure: (() -> Void)?
    var onExit: (() -> Void)?
    var previewVersion = 1
    var audioVersion = 1
    var idle: Bool { pending.isEmpty }
    var methods: [String] = []
    var configuration = CameraConfig()
    var inventory: [[String: Any]] = [["id": "fixture: mic-1", "name": "Microphone"], ["id": "fixture: mic-2", "name": "Microphone"]]
    var status: [String: Any] = ["state": "stopped"]
    var hold: String?
    var pending: [([String: Any]) -> Void] = []
    private var directory = URL(fileURLWithPath: "/tmp/unused")
    func launch(executable: URL, directory: URL) { self.directory = directory; onReady?() }
    func request(_ method: String, config: CameraConfig?, previewGeneration: String?, completion: @escaping ([String: Any]) -> Void) {
        methods.append(method)
        if hold == method { pending.append(completion); return }
        switch method {
        case "config": completion(["result": try! configuration.helperObject(audioSupported: audioVersion == 1)])
        case "configure": configuration = config!; completion(["result": try! configuration.helperObject(audioSupported: audioVersion == 1)])
        case "devices": completion(["result": [["index": "0", "name": "MacBook Pro Camera"]]])
        case "audio_devices": completion(["result": inventory])
        case "start": status = ["state": "starting"]; completion(["result": status])
        case "status": completion(["result": status])
        case "stop": status = ["state": "stopped"]; completion(["result": status])
        case "export": completion(["result": ["configuration": directory.appendingPathComponent("camstream-camera.yaml").path,
                                               "environment": directory.appendingPathComponent("camstream-camera.env").path]])
        case "preview_detach": completion(["result": ["detached": true]])
        default: completion(["error": ["code": "preview_unavailable"]])
        }
    }
    func deliver(_ reply: [String: Any]) { pending.removeFirst()(reply) }
    func shutdown() { pending = []; onExit?() }
}
