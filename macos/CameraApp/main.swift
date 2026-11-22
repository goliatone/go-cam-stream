import AppKit
import SwiftUI

@MainActor
final class CameraApp: NSObject, NSApplicationDelegate, NSWindowDelegate {
    static let guide = URL(string: "https://github.com/goliatone/go-cam-stream/blob/main/macos/README.md")!
    private let model = CameraModel()
    private let server = ServerMonitor()
    private var window: NSWindow!
    private var status: NSStatusItem!
    private var menus: AppMenus!
    private var settings: SettingsWindowController!
    private var devices: DeviceWatcher?
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
        settings = SettingsWindowController(model: model, server: server) { [weak self] in self?.showWindow() }
        settings.visibilityChanged = { [weak self] in self?.updateWindowVisibility() }
        model.showSettings = { [weak self] pane in self?.settings.show(pane) }
        model.confirm = { [weak self] confirmation, confirmed in self?.present(confirmation, then: confirmed) }
        menus = AppMenus(model: model)
        menus.openSettings = { [weak self] pane in self?.settings.show(pane) }
        menus.showWindow = { [weak self] in self?.showWindow() }
        menus.openHelp = { NSWorkspace.shared.open(CameraApp.guide) }
        NSApp.mainMenu = menus.main
        NSApp.windowsMenu = menus.windowMenu
        NSApp.helpMenu = menus.helpMenu
        // The SwiftUI toolbar (status pill, Settings) bridges into this window.
        let content = NSHostingController(rootView: CameraView(model: model, server: server))
        content.sceneBridgingOptions = [.toolbars]
        content.sizingOptions = [.minSize]
        window = NSWindow(contentViewController: content)
        window.styleMask = [.titled, .closable, .miniaturizable, .resizable]
        window.toolbarStyle = .unified
        window.title = "Camstream Camera"
        window.isReleasedWhenClosed = false
        window.delegate = self
        window.minSize = NSSize(width: 640, height: 520)
        window.setContentSize(NSSize(width: 1000, height: 700))
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
        devices = DeviceWatcher { [model] in model.devicesChanged() }
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
        NSApp.activate(ignoringOtherApps: true); updateWindowVisibility()
    }
    private func updateWindowVisibility() {
        setWindowVisible(window != nil && window.isVisible && !window.isMiniaturized && !NSApp.isHidden
                         && window.occlusionState.contains(.visible))
    }
    /// The preview follows the main window. The server status also polls
    /// while Settings is open, where Server shows it.
    private func setWindowVisible(_ visible: Bool) {
        model.setWindowVisible(visible)
        server.setVisible((visible || (settings?.isShowing == true && !NSApp.isHidden)) && !quitting)
    }

    /// Asks on the main window before a step that cuts something off:
    /// ending or leaving a call, or stopping the stream to start one. Keep
    /// Call and Cancel answer Return and Escape; the other choice runs
    /// `confirmed` once.
    private func present(_ confirmation: Confirmation, then confirmed: @escaping () -> Void) {
        showWindow()
        guard window.attachedSheet == nil else { return }
        let alert = confirmation.alert()
        let escape = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak alert] event in
            guard event.keyCode == 53, let sheet = alert?.window, sheet.isKeyWindow else { return event }
            sheet.sheetParent?.endSheet(sheet, returnCode: .alertFirstButtonReturn)
            return nil
        }
        alert.beginSheetModal(for: window) { response in
            if let escape { NSEvent.removeMonitor(escape) }
            guard Confirmation.confirmed(response) else { return }
            confirmed()
        }
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        guard let confirmation = model.endConfirmation else { return true }
        present(confirmation) { [weak self] in
            self?.model.closeConversation()
            self?.window.close()
        }
        return false
    }
    func windowWillClose(_ notification: Notification) {
        setWindowVisible(false)
        // Ordinary closes keep the stream and share. A call reaches here
        // only after confirmation, or while one is still opening.
        model.closeConversation()
    }
    func windowDidMiniaturize(_ notification: Notification) { setWindowVisible(false) }
    func windowDidDeminiaturize(_ notification: Notification) { updateWindowVisibility() }
    func windowDidChangeOcclusionState(_ notification: Notification) { updateWindowVisibility() }
    func applicationDidHide(_ notification: Notification) { setWindowVisible(false) }
    func applicationDidUnhide(_ notification: Notification) { updateWindowVisibility() }
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
        server.setVisible(false)
        return model.quit() ? .terminateNow : .terminateLater
    }
}

let app = NSApplication.shared
let delegate = MainActor.assumeIsolated { CameraApp() }
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
