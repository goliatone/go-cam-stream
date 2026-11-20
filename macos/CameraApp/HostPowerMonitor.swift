import CoreFoundation
import IOKit
import IOKit.pwr_mgt

/// Delivers system power messages on the app's main run loop. The app can
/// acknowledge an impending sleep after the helper has revoked guest access.
final class HostPowerMonitor {
    private var root: io_connect_t = 0
    private var notifier: io_object_t = 0
    private var port: IONotificationPortRef?
    private var source: CFRunLoopSource?
    private let onSleep: (@escaping () -> Void) -> Void
    private let onWake: () -> Void

    init?(onSleep: @escaping (@escaping () -> Void) -> Void, onWake: @escaping () -> Void) {
        self.onSleep = onSleep
        self.onWake = onWake
        root = IORegisterForSystemPower(Unmanaged.passUnretained(self).toOpaque(), &port, { context, _, message, argument in
            guard let context else { return }
            let monitor = Unmanaged<HostPowerMonitor>.fromOpaque(context).takeUnretainedValue()
            monitor.receive(message, notification: argument.map { Int(bitPattern: $0) } ?? 0)
        }, &notifier)
        guard root != 0, let port, let source = IONotificationPortGetRunLoopSource(port)?.takeUnretainedValue() else { return nil }
        self.source = source
        CFRunLoopAddSource(CFRunLoopGetMain(), source, .commonModes)
    }

    private func receive(_ message: UInt32, notification: Int) {
        switch message {
        case UInt32(CamstreamCanSystemSleep):
            IOAllowPowerChange(root, notification)
        case UInt32(CamstreamSystemWillSleep):
            onSleep { [weak self] in
                if let self { IOAllowPowerChange(self.root, notification) }
            }
        case UInt32(CamstreamSystemHasPoweredOn):
            onWake()
        default:
            break
        }
    }

    deinit {
        if let source { CFRunLoopRemoveSource(CFRunLoopGetMain(), source, .commonModes) }
        if notifier != 0 { IODeregisterForSystemPower(&notifier) }
        if let port { IONotificationPortDestroy(port) }
        if root != 0 { IOServiceClose(root) }
    }
}
