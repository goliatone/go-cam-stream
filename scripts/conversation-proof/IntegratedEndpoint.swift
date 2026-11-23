import AppKit
import WebKit

@main
enum Main {
    static func main() {
        guard CommandLine.arguments.count == 4 || CommandLine.arguments.count == 5, let input = readLine(),
              let data = input.data(using: .utf8),
              let prejoin = try? JSONDecoder().decode(ConversationPrejoin.self, from: data),
              prejoin.pageURL.scheme == "http", prejoin.pageURL.host == "127.0.0.1",
              let script = try? String(contentsOfFile: CommandLine.arguments[1], encoding: .utf8) else { exit(2) }
        let app = NSApplication.shared
        let camera = CameraModel(permissions: SyntheticPermissions(),
                                 directory: URL(fileURLWithPath: CommandLine.arguments[3], isDirectory: true),
                                 helper: URL(fileURLWithPath: CommandLine.arguments[2]))
        let delegate = Endpoint(prejoin: prejoin, script: script, startMuted: CommandLine.arguments.last == "--start-muted", camera: camera)
        app.delegate = delegate
        app.setActivationPolicy(.accessory)
        app.run()
    }
}

// The helper verifies the real capture lock. WebKit receives synthetic tracks
// and the proof never asks macOS to open a physical input.
private struct SyntheticPermissions: CameraPermissions {
    func status(_ media: CameraMedia) -> String { "denied" }
    func request(_ media: CameraMedia, completion: @escaping () -> Void) { completion() }
}

private final class Endpoint: NSObject, NSApplicationDelegate {
    private let prejoin: ConversationPrejoin
    private let script: String
    private let startMuted: Bool
    private let camera: CameraModel
    private var surface: ConversationMediaSurface?
    private var window: NSWindow?
    private var timer: Timer?
    private var inFlight = false
    private var joining = false
    private var received = false
    private var closed = false
    private let deadline = Date().addingTimeInterval(28)
    private var diagnosing = false

    init(prejoin: ConversationPrejoin, script: String, startMuted: Bool, camera: CameraModel) {
        self.prejoin = prejoin
        self.script = script
        self.startMuted = startMuted
        self.camera = camera
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        camera.didFinishQuit = { NSApp.reply(toApplicationShouldTerminate: true) }
        camera.launch()
        guard let surface = ConversationMediaSurface(prejoin: prejoin, camera: camera, startMuted: startMuted, ended: { [weak self] problem in
            self?.closed = true
            if let problem { fputs(problem + "\n", stderr) }
        }) else { exit(3) }
        let view = surface.view
        view.configuration.userContentController.addUserScript(
            WKUserScript(source: script, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        let panel = NSWindow(contentRect: NSRect(x: 20, y: 20, width: 600, height: 300),
                             styleMask: [.titled], backing: .buffered, defer: false)
        panel.title = "Camstream Integrated Conversation Proof"
        panel.contentView = view
        panel.orderFrontRegardless()
        window = panel
        self.surface = surface
        surface.loadPrejoin()
        timer = Timer.scheduledTimer(withTimeInterval: 0.2, repeats: true) { [weak self] _ in self?.probe() }
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        camera.quit() ? .terminateNow : .terminateLater
    }

    private func probe() {
        if !received && !closed && Date() >= deadline, !diagnosing {
            diagnosing = true
            surface?.view.evaluateJavaScript("JSON.stringify(window.integratedProof?.status() ?? {state:document.readyState,title:document.querySelector('h1')?.textContent,error:document.querySelector('[role=alert]')?.textContent})") { [weak self] value, _ in
                self?.report(["problem": (value as? String) ?? "WebKit did not return diagnostic state"])
                NSApp.terminate(nil)
            }
            return
        }
        guard !inFlight, let view = surface?.view else { return }
        if closed {
            guard !camera.conversationCapture else { return }
            report(["closed": true])
            NSApp.terminate(nil)
            return
        }
        guard !received, camera.ready, !camera.working else { return }
        inFlight = true
        if !joining && !startMuted {
            view.callAsyncJavaScript("return document.querySelector('#join-call') ? await integratedProof.start() : false",
                                     in: nil, in: .page) { [weak self] result in
                guard let self else { return }
                self.inFlight = false
                if case .success(let value) = result { self.joining = value as? Bool == true }
            }
            return
        }
        let script = startMuted ? "return integratedProof.cameraIdle() ? await integratedProof.sample() : null" : "return await integratedProof.sample()"
        view.callAsyncJavaScript(script, in: nil, in: .page) { [weak self] result in
            guard let self else { return }
            self.inFlight = false
            guard case .success(let value) = result, let stats = value as? [String: Int],
                  (self.startMuted || (stats["audio"] ?? 0) > 0), (stats["video"] ?? 0) > 0,
                  self.camera.conversationCapture != self.startMuted else { return }
            self.received = true
            self.report(["received": stats])
            self.nextCommand()
        }
    }

    private func nextCommand() {
        DispatchQueue.global().async {
            let action = readLine()
            DispatchQueue.main.async { [weak self] in
                guard let self, let view = self.surface?.view else { return }
                guard let action else {
                    view.callAsyncJavaScript("await integratedProof.leave()", in: nil, in: .page) { _ in }
                    return
                }
                view.callAsyncJavaScript("await integratedProof.step(action); return true", arguments: ["action": action],
                                         in: nil, in: .page) { [weak self] result in
                    guard let self else { return }
                    switch result {
                    case .success: self.report(["ok": true])
                    case .failure(let error):
                        view.evaluateJavaScript("JSON.stringify(integratedProof.status())") { value, _ in
                            self.report(["problem": "\(error.localizedDescription) \(value as? String ?? "")"])
                        }
                    }
                    self.nextCommand()
                }
            }
        }
    }

    private func report(_ value: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: value) else { exit(4) }
        FileHandle.standardOutput.write(data)
        FileHandle.standardOutput.write(Data([10]))
    }
}
