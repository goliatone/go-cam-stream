import AppKit
import SwiftUI

final class CameraApp: NSObject, NSApplicationDelegate, NSWindowDelegate {
    static let guide = URL(string: "https://github.com/goliatone/go-cam-stream/blob/main/macos/README.md")!
    private let model = CameraModel()
    private var window: NSWindow!
    private var status: NSStatusItem!
    private var menus: AppMenus!
    private var hostPower: HostPowerMonitor?
    private var quitting = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        if let identity = Bundle.main.bundleIdentifier,
           let other = NSRunningApplication.runningApplications(withBundleIdentifier: identity)
            .first(where: { $0.processIdentifier != ProcessInfo.processInfo.processIdentifier }) {
            other.activate(options: [.activateAllWindows])
            NSApp.terminate(nil)
            return
        }
        menus = AppMenus(model: model)
        menus.showSection = { [weak self] section in self?.model.section = section; self?.showWindow() }
        menus.confirmEnd = { [weak self] in self?.confirmEnd(closingWindow: false) }
        menus.openHelp = { NSWorkspace.shared.open(CameraApp.guide) }
        NSApp.mainMenu = menus.main
        NSApp.windowsMenu = menus.windowMenu
        NSApp.helpMenu = menus.helpMenu
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1000, height: 740),
            styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        window.title = "Camstream Camera"
        window.isReleasedWhenClosed = false
        window.delegate = self
        window.minSize = NSSize(width: 640, height: 520)
        window.contentView = NSHostingView(rootView: CameraView(model: model))
        window.center()
        status = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        status.isVisible = true
        status.button?.title = ""
        status.button?.imagePosition = .imageOnly
        status.menu = menus.status
        model.didChange = { [weak self] in self?.updateMenu() }
        model.didExit = { [weak self] in if self?.quitting == true { NSApp.reply(toApplicationShouldTerminate: true) } }
        hostPower = HostPowerMonitor(onSleep: { [model] acknowledge in
            model.prepareForSleep(acknowledge: acknowledge)
        }, onWake: { [model] in model.wakeFromSleep() })
        model.sharing.observesHostPower = hostPower != nil
        model.launch()
        updateMenu()
        showWindow()
    }
    private func updateMenu() {
        guard status != nil else { return }
        menus.update()
        let activity = model.activity
        status.button?.image = activity.statusImage
        status.button?.toolTip = activity.tooltip
        status.button?.setAccessibilityLabel(activity.label)
    }
    private func showWindow() {
        window?.deminiaturize(nil); window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true); updatePreviewVisibility()
    }
    private func updatePreviewVisibility() {
        model.setWindowVisible(window != nil && window.isVisible && !window.isMiniaturized && window.occlusionState.contains(.visible))
    }

    /// Asks before a conversation ends or the guest leaves. Keep Call and
    /// Cancel are the default for Return and Escape; End and Leave use the
    /// existing teardown once.
    private func confirmEnd(closingWindow: Bool) {
        guard let confirmation = model.endConfirmation else {
            if closingWindow { window.close() }
            return
        }
        if !closingWindow { showWindow() }
        guard window.attachedSheet == nil else { return }
        let alert = confirmation.alert()
        let escape = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak alert] event in
            guard event.keyCode == 53, let sheet = alert?.window, sheet.isKeyWindow else { return event }
            sheet.sheetParent?.endSheet(sheet, returnCode: .alertFirstButtonReturn)
            return nil
        }
        alert.beginSheetModal(for: window) { [weak self] response in
            if let escape { NSEvent.removeMonitor(escape) }
            guard let self, EndConfirmation.confirmed(response) else { return }
            self.model.closeConversation()
            if closingWindow { self.window.close() }
        }
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        guard model.endConfirmation != nil else { return true }
        confirmEnd(closingWindow: true)
        return false
    }
    func windowWillClose(_ notification: Notification) {
        model.setWindowVisible(false)
        // Ordinary closes keep the stream and share. A conversation reaches
        // here only after confirmation, or while one is still opening.
        model.closeConversation()
    }
    func windowDidMiniaturize(_ notification: Notification) { model.setWindowVisible(false) }
    func windowDidDeminiaturize(_ notification: Notification) { updatePreviewVisibility() }
    func windowDidChangeOcclusionState(_ notification: Notification) { updatePreviewVisibility() }
    func applicationDidHide(_ notification: Notification) { model.setWindowVisible(false) }
    func applicationDidUnhide(_ notification: Notification) { updatePreviewVisibility() }
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool { showWindow(); return true }
    func application(_ application: NSApplication, open urls: [URL]) {
        guard let invitation = urls.first(where: { $0.scheme == "camstream" }) else { return }
        model.openConversationInvitation(invitation.absoluteString)
        if window != nil { showWindow() }
    }
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
