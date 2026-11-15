import AppKit
import SwiftUI

final class CameraApp: NSObject, NSApplicationDelegate, NSMenuDelegate, NSWindowDelegate {
    private let model = CameraModel()
    private var window: NSWindow!
    private var status: NSStatusItem!
    private var toggle: NSMenuItem!
    private var stateItem: NSMenuItem!
    private var appToggle: NSMenuItem!
    private var quitting = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        if let identity = Bundle.main.bundleIdentifier,
           let other = NSRunningApplication.runningApplications(withBundleIdentifier: identity)
            .first(where: { $0.processIdentifier != ProcessInfo.processInfo.processIdentifier }) {
            other.activate(options: [.activateAllWindows])
            NSApp.terminate(nil)
            return
        }
        let main = NSMenu(), application = NSMenuItem()
        main.addItem(application)
        let appMenu = NSMenu()
        appMenu.autoenablesItems = false
        appMenu.delegate = self
        application.submenu = appMenu
        appToggle = NSMenuItem(title: "Start Camera", action: #selector(toggleCapture), keyEquivalent: "")
        appToggle.target = self; appMenu.addItem(appToggle)
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "Settings…", action: #selector(settings), keyEquivalent: ",").target = self
        appMenu.addItem(withTitle: "Connection Details", action: #selector(connection), keyEquivalent: "l").target = self
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "Quit Camstream Camera", action: #selector(quit), keyEquivalent: "q").target = self
        let editItem = NSMenuItem(), edit = NSMenu(title: "Edit")
        editItem.submenu = edit; main.addItem(editItem)
        for (title, action, key) in [("Cut", "cut:", "x"), ("Copy", "copy:", "c"), ("Paste", "paste:", "v"), ("Select All", "selectAll:", "a")] {
            edit.addItem(withTitle: title, action: Selector(action), keyEquivalent: key)
        }
        NSApp.mainMenu = main
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1000, height: 740),
            styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        window.title = "Camstream Camera"
        window.isReleasedWhenClosed = false
        window.delegate = self
        window.minSize = NSSize(width: 900, height: 680)
        window.contentView = NSHostingView(rootView: CameraView(model: model))
        window.center()
        status = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        status.isVisible = true
        status.button?.title = ""
        status.button?.imagePosition = .imageOnly
        let menu = NSMenu(); menu.autoenablesItems = false; menu.delegate = self
        stateItem = NSMenuItem(title: "Camera stopped", action: nil, keyEquivalent: ""); stateItem.isEnabled = false
        menu.addItem(stateItem); menu.addItem(.separator())
        toggle = NSMenuItem(title: "Start Camera", action: #selector(toggleCapture), keyEquivalent: "")
        toggle.target = self; menu.addItem(toggle)
        for (title, action) in [("Connection Details…", #selector(connection)), ("Settings…", #selector(settings))] {
            let item = NSMenuItem(title: title, action: action, keyEquivalent: ""); item.target = self; menu.addItem(item)
        }
        menu.addItem(.separator())
        let quit = NSMenuItem(title: "Quit Camstream Camera", action: #selector(quit), keyEquivalent: "q")
        quit.target = self; menu.addItem(quit)
        status.menu = menu
        model.didChange = { [weak self] in self?.updateMenu() }
        model.didExit = { [weak self] in if self?.quitting == true { NSApp.reply(toApplicationShouldTerminate: true) } }
        model.launch()
        showWindow()
    }
    func menuWillOpen(_ menu: NSMenu) { model.refreshPermission(); updateMenu() }
    private func updateMenu() {
        guard status != nil else { return }
        stateItem.title = model.title
        toggle.title = model.capture.active ? "Stop Camera" : "Start Camera"
        toggle.isEnabled = model.capture.active ? model.canStop : model.canStart
        appToggle.title = toggle.title
        appToggle.isEnabled = toggle.isEnabled
        let symbol = model.capture.phase == .streaming ? "video.fill" : "video"
        let icon = NSImage(systemSymbolName: symbol, accessibilityDescription: "Camstream Camera")?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 17, weight: .regular))
        icon?.isTemplate = true
        status.button?.image = icon
        status.button?.toolTip = "Camstream Camera: \(model.title)"
        status.button?.setAccessibilityLabel("Camstream Camera: \(model.title)")
    }
    private func showWindow() {
        window?.deminiaturize(nil); window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true); updatePreviewVisibility()
    }
    private func updatePreviewVisibility() {
        model.setWindowVisible(window != nil && window.isVisible && !window.isMiniaturized && window.occlusionState.contains(.visible))
    }
    func windowWillClose(_ notification: Notification) { model.setWindowVisible(false) }
    func windowDidMiniaturize(_ notification: Notification) { model.setWindowVisible(false) }
    func windowDidDeminiaturize(_ notification: Notification) { updatePreviewVisibility() }
    func windowDidChangeOcclusionState(_ notification: Notification) { updatePreviewVisibility() }
    func applicationDidHide(_ notification: Notification) { model.setWindowVisible(false) }
    func applicationDidUnhide(_ notification: Notification) { updatePreviewVisibility() }
    @objc private func settings() { model.section = 0; showWindow() }
    @objc private func connection() { model.section = 1; showWindow() }
    @objc private func toggleCapture() { if model.capture.active { model.stop() } else { model.start() } }
    @objc private func quit() { NSApp.terminate(nil) }
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool { showWindow(); return true }
    func applicationDidBecomeActive(_ notification: Notification) { model.refreshPermission() }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard window != nil else { return .terminateNow }
        quitting = true
        return model.quit() ? .terminateNow : .terminateLater
    }
}

let app = NSApplication.shared
let delegate = CameraApp()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
