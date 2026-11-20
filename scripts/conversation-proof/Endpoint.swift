import AppKit
import WebKit

final class Endpoint: NSObject, NSApplicationDelegate, WKNavigationDelegate, WKUIDelegate {
    let url: URL
    var window: NSWindow?
    var web: WKWebView?
    var timer: Timer?
    var received = false
    var leaving = false
    var finished = false

    init(url: URL) { self.url = url }

    func applicationDidFinishLaunching(_ notification: Notification) {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        configuration.mediaTypesRequiringUserActionForPlayback = []
        let view = WKWebView(frame: .zero, configuration: configuration)
        view.navigationDelegate = self
        view.uiDelegate = self
        let panel = NSWindow(contentRect: NSRect(x: 50, y: 50, width: 400, height: 320),
                             styleMask: [.titled], backing: .buffered, defer: false)
        panel.title = "Synthetic Media Proof"
        panel.contentView = view
        panel.orderFrontRegardless()
        window = panel
        web = view
        view.load(URLRequest(url: url))
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        let target = navigationAction.request.url
        decisionHandler(target?.scheme == url.scheme && target?.host == url.host &&
                        target?.port == url.port && target?.path == url.path ? .allow : .cancel)
    }

    func webView(_ webView: WKWebView, requestMediaCapturePermissionFor origin: WKSecurityOrigin,
                 initiatedByFrame frame: WKFrameInfo, type: WKMediaCaptureType,
                 decisionHandler: @escaping (WKPermissionDecision) -> Void) {
        decisionHandler(.deny)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        webView.evaluateJavaScript("document.querySelector('#join').click()")
        timer = Timer.scheduledTimer(withTimeInterval: 0.2, repeats: true) { [weak self] _ in
            self?.check()
        }
    }

    func check() {
        web?.evaluateJavaScript("JSON.stringify({received:window.proofReceived,closed:window.proofClosed,status:document.querySelector('#status').textContent})") { [weak self] value, _ in
            guard let self, let value = value as? String,
                  let result = try? JSONSerialization.jsonObject(with: Data(value.utf8)) as? [String: Any] else { return }
            if !self.received, result["received"] != nil {
                self.received = true
                print(value)
                fflush(stdout)
                // The Go proof asks this endpoint to leave only after both peers receive.
                self.timer?.invalidate()
                DispatchQueue.global().async {
                    _ = readLine()
                    DispatchQueue.main.async {
                        self.leaving = true
                        self.web?.evaluateJavaScript("document.querySelector('#leave').click()")
                        self.timer = Timer.scheduledTimer(withTimeInterval: 0.2, repeats: true) { _ in self.check() }
                    }
                }
            } else if self.leaving, !self.finished, result["closed"] as? Bool == true {
                self.finished = true
                self.timer?.invalidate()
                print(value)
                fflush(stdout)
                NSApp.terminate(nil)
            }
        }
    }
}

guard CommandLine.arguments.count == 2, let url = URL(string: CommandLine.arguments[1]),
      url.scheme == "http", url.host == "127.0.0.1", url.port != nil else { exit(2) }
let app = NSApplication.shared
let endpoint = Endpoint(url: url)
app.delegate = endpoint
app.setActivationPolicy(.accessory)
app.run()
