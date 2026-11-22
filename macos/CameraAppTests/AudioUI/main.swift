import AppKit
import SwiftUI

// Standalone UI test executable. No live helper or AVFoundation provider exists
// in this composition; every action remains an in-memory fixture operation.
@MainActor
final class FixtureUI: NSObject, NSApplicationDelegate {
    let helper = FixtureCameraHelper()
    let permissions = FixturePermissions()
    var model: CameraModel!
    var window: NSWindow!
    var settings: SettingsWindowController!
    func applicationDidFinishLaunching(_ notification: Notification) {
        permissions.audio = "authorized"
        model = CameraModel(client: helper, permissions: permissions)
        let server = ServerMonitor(defaults: UserDefaults(suiteName: "com.goliatone.camstream.camera.audio-ui")!)
        // The console as the app hosts it, with its toolbar status and Settings button.
        let content = NSHostingController(rootView: CameraView(model: model, server: server))
        content.sceneBridgingOptions = [.toolbars]
        content.sizingOptions = [.minSize]
        window = NSWindow(contentViewController: content)
        window.styleMask = [.titled, .closable, .resizable, .miniaturizable]
        window.toolbarStyle = .unified
        window.title = "Camstream T05 UI Fixture — Sensors Disabled"
        window.isReleasedWhenClosed = false
        window.setContentSize(NSSize(width: 1000, height: 700))
        // The microphone controls are in Settings → Camera, opened at launch.
        settings = SettingsWindowController(model: model, server: server) { [weak self] in self?.model.screen = .call }
        model.showSettings = { [weak self] pane in self?.settings.show(pane) }
        window.center(); window.makeKeyAndOrderFront(nil)
        let menu = NSMenu(), item = NSMenuItem(), appMenu = NSMenu()
        menu.addItem(item); item.submenu = appMenu
        appMenu.addItem(withTitle: "Quit Fixture", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        NSApp.mainMenu = menu
        model.launch()
        settings.show(.camera)
        NSApp.activate(ignoringOtherApps: true)
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply { _ = model.quit(); return .terminateNow }
}
let app = NSApplication.shared
let delegate = MainActor.assumeIsolated { FixtureUI() }
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
