import AVFoundation

/// Notices cameras and microphones being connected or removed, so the
/// lists in Settings follow without a Refresh button. It only lists
/// devices: it never opens one, so it can't start capture or ask for access.
final class DeviceWatcher: NSObject {
    private let discovery = AVCaptureDevice.DiscoverySession(
        deviceTypes: [.builtInWideAngleCamera, .external, .continuityCamera, .deskViewCamera, .microphone],
        mediaType: nil, position: .unspecified)
    private var observation: NSKeyValueObservation?
    private var pending: DispatchWorkItem?

    init(onChange: @escaping () -> Void) {
        super.init()
        observation = discovery.observe(\.devices, options: [.new]) { [weak self] _, _ in
            DispatchQueue.main.async { self?.schedule(onChange) }
        }
    }

    deinit { pending?.cancel() }

    /// A device often reports several changes while it connects. One refresh
    /// follows them.
    private func schedule(_ onChange: @escaping () -> Void) {
        pending?.cancel()
        let work = DispatchWorkItem(block: onChange)
        pending = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5, execute: work)
    }
}
