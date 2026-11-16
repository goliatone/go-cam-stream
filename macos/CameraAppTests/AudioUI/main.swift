import AppKit
import SwiftUI

// Standalone UI test executable. No live helper or AVFoundation provider exists
// in this composition; every action remains an in-memory fixture operation.
final class FixtureUI: NSObject, NSApplicationDelegate {
    let helper = FixtureCameraHelper()
    let permissions = FixturePermissions()
    var model: CameraModel!
    var window: NSWindow!
    func applicationDidFinishLaunching(_ notification: Notification) {
        permissions.audio = "authorized"
        model = CameraModel(client: helper, permissions: permissions)
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1040, height: 860),
                          styleMask: [.titled, .closable, .resizable, .miniaturizable], backing: .buffered, defer: false)
        window.title = "Camstream T05 UI Fixture — Sensors Disabled"
        window.isReleasedWhenClosed = false
        window.contentView = NSHostingView(rootView: CameraView(model: model))
        window.center(); window.makeKeyAndOrderFront(nil)
        let menu = NSMenu(), item = NSMenuItem(), appMenu = NSMenu()
        menu.addItem(item); item.submenu = appMenu
        appMenu.addItem(withTitle: "Quit Fixture", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        NSApp.mainMenu = menu
        model.launch()
        NSApp.activate(ignoringOtherApps: true)
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply { _ = model.quit(); return .terminateNow }
}
let app = NSApplication.shared
let delegate = FixtureUI()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
