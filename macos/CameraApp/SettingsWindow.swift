import AppKit
import SwiftUI

extension SettingsPane {
    static let width: CGFloat = 560
    /// Each pane's window height. Longer content scrolls inside the form.
    var height: CGFloat {
        switch self {
        case .general: return 140
        case .camera: return 500
        case .network: return 330
        case .server: return 600
        case .sharing: return 620
        case .calls: return 520
        }
    }
}

/// The standard Settings window (⌘,): app-wide choices and one-time setup
/// in six toolbar panes. Leaving a pane or closing the window applies valid
/// camera changes.
@MainActor
final class SettingsWindowController: NSWindowController, NSWindowDelegate {
    private let model: CameraModel
    private let tabs = SettingsTabsController()
    /// The window opened or closed; the server status polls while it shows.
    var visibilityChanged: () -> Void = {}
    var isShowing: Bool { window?.isVisible == true && window?.isMiniaturized == false }

    init(model: CameraModel, server: ServerMonitor, showWindow: @escaping () -> Void) {
        self.model = model
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: SettingsPane.width, height: SettingsPane.general.height),
                              styleMask: [.titled, .closable, .miniaturizable], backing: .buffered, defer: true)
        window.isReleasedWhenClosed = false
        window.toolbarStyle = .preference
        super.init(window: window)
        tabs.tabStyle = .toolbar
        tabs.transitionOptions = []
        for pane in SettingsPane.allCases {
            let hosting = NSHostingController(rootView: SettingsPaneView(pane: pane, model: model, server: server, showWindow: showWindow))
            hosting.sizingOptions = []
            hosting.title = pane.title
            let item = NSTabViewItem(viewController: hosting)
            item.label = pane.title
            item.image = NSImage(systemSymbolName: pane.symbol, accessibilityDescription: pane.title)
            // Toolbar-style tabs use this as the toolbar item identifier, so it is a string.
            item.identifier = "settings-\(pane)"
            tabs.addTabViewItem(item)
        }
        tabs.willLeavePane = { [weak self] in self?.model.applySettings() }
        tabs.didSelectPane = { [weak self] in self?.paneChanged() }
        window.contentViewController = tabs
        window.delegate = self
        // Setting the content sizes the window to the tab view, not the first pane.
        tabs.fit(animate: false)
        window.title = selectedPane.title
    }

    required init?(coder: NSCoder) { fatalError("SettingsWindowController is created in code") }

    var selectedPane: SettingsPane { SettingsPane(rawValue: tabs.selectedTabViewItemIndex) ?? .general }

    /// Shows the window at a pane, or at the pane it was left on.
    func show(_ pane: SettingsPane?) {
        guard let window else { return }
        if let pane { select(pane) }
        let opening = !window.isVisible
        tabs.fit(animate: false)
        if opening { window.center() }
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        paneChanged()
    }

    /// Selects a pane without bringing the window forward.
    func select(_ pane: SettingsPane) {
        if pane != selectedPane { tabs.selectedTabViewItemIndex = pane.rawValue }
    }

    private func paneChanged() {
        window?.title = selectedPane.title
        if !isShowing || selectedPane != .camera { model.webcamPreview.stop(in: .settings) }
        model.callSettingsVisible = isShowing && selectedPane == .calls
        visibilityChanged()
    }

    func windowWillClose(_ notification: Notification) {
        model.webcamPreview.stop(in: .settings)
        model.applySettings()
        model.callSettingsVisible = false
        DispatchQueue.main.async { [weak self] in self?.visibilityChanged() }
    }
    func windowDidMiniaturize(_ notification: Notification) { paneChanged() }
    func windowDidDeminiaturize(_ notification: Notification) { paneChanged() }
}

/// Resizes the window to each pane, keeping its top edge in place.
private final class SettingsTabsController: NSTabViewController {
    var willLeavePane: () -> Void = {}
    var didSelectPane: () -> Void = {}

    override func tabView(_ tabView: NSTabView, willSelect tabViewItem: NSTabViewItem?) {
        if tabViewItem != tabView.selectedTabViewItem { willLeavePane() }
        super.tabView(tabView, willSelect: tabViewItem)
    }

    override func tabView(_ tabView: NSTabView, didSelect tabViewItem: NSTabViewItem?) {
        super.tabView(tabView, didSelect: tabViewItem)
        fit(animate: view.window?.isVisible == true)
        didSelectPane()
    }

    func fit(animate: Bool) {
        guard let window = view.window,
              let pane = SettingsPane(rawValue: selectedTabViewItemIndex) else { return }
        let size = NSSize(width: SettingsPane.width, height: pane.height)
        let content = window.contentRect(forFrameRect: window.frame)
        guard content.size != size else { return }
        var frame = window.frameRect(forContentRect: NSRect(origin: content.origin, size: size))
        frame.origin.y = window.frame.maxY - frame.height
        window.setFrame(frame, display: true, animate: animate)
    }
}

struct SettingsPaneView: View {
    let pane: SettingsPane
    @ObservedObject var model: CameraModel
    @ObservedObject var server: ServerMonitor
    var showWindow: () -> Void = {}

    var body: some View {
        Group {
            switch pane {
            case .general: GeneralSettingsPane(model: model)
            case .camera: CameraSettingsPane(model: model)
            case .network: NetworkSettingsPane(model: model)
            case .server: ServerSettingsPane(model: model, server: server)
            case .sharing: SharingSettingsPane(sharing: model.sharing)
            case .calls: CallSettingsPane(model: model, showWindow: showWindow)
            }
        }
        .formStyle(.grouped)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// Why a pane's fields are unavailable: the camera service, or a camera
/// or call that is using them. Shown at the top of the pane.
struct SettingsStatusSection: View {
    @ObservedObject var model: CameraModel

    var body: some View {
        if !model.ready {
            Section {
                HStack(alignment: .firstTextBaseline) {
                    Label(model.helperFeedback?.title ?? model.helperBlockedReason ?? "Starting the camera service…",
                          systemImage: model.helperFailure == nil ? "hourglass" : "exclamationmark.triangle.fill")
                        .foregroundStyle(model.helperFailure == nil ? Color.secondary : Color.orange)
                    Spacer()
                    if model.canRetryHelper { Button(Feedback.Action.retryHelper.title, action: model.retryHelper) }
                }
            } footer: {
                SettingsFootnote("Settings need the camera service. They’re kept while it reconnects.")
            }
        } else if let lock = model.settingsLockReason {
            Section {
                HStack(alignment: .firstTextBaseline) {
                    Label(lock, systemImage: "lock.fill").foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true).accessibilityIdentifier("settings-locked")
                    Spacer()
                    if model.capture.active { Button("Stop Camera", action: model.stop).disabled(!model.canStop) }
                }
            }
        }
    }
}

struct SettingsFootnote: View {
    let text: String
    init(_ text: String) { self.text = text }
    var body: some View {
        Text(text).font(.footnote).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
    }
}

/// A value that can't apply yet, shown under its field.
struct FieldProblem: View {
    let text: String
    init(_ text: String) { self.text = text }
    var body: some View {
        Label(text, systemImage: "exclamationmark.circle").font(.callout).foregroundStyle(.orange)
            .fixedSize(horizontal: false, vertical: true)
    }
}
