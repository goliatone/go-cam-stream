import AppKit
import AVFoundation
import Darwin
import SwiftUI

/// Owns a video-only capture session while a webcam picker is visible. Public
/// state is main-thread only. Camera I/O and the capture lock use the serial queue.
final class WebcamPreview: ObservableObject {
    enum Surface { case settings, welcome }
    enum Phase { case idle, opening, live(AVCaptureSession), failed(String) }

    @Published private(set) var phase = Phase.idle
    private(set) var surface: Surface?
    private(set) var ownsCapture = false
    private let queue = DispatchQueue(label: "camstream.webcam-preview")
    private let frames = DispatchQueue(label: "camstream.webcam-preview.frames")
    private var generation = 0
    private var watchdog: DispatchWorkItem?
    private var opening: DispatchWorkItem?

    // These properties belong to the capture queue.
    private var session: AVCaptureSession?
    private var output: AVCaptureVideoDataOutput?
    private var frameSignal: WebcamFrameSignal?
    private var lock: FileHandle?
    private var observers: [NSObjectProtocol] = []

    func open(_ selection: String, in surface: Surface, directory: URL) {
        generation += 1
        let token = generation
        self.surface = surface
        ownsCapture = true
        phase = .opening
        watch(token, seconds: 12, message: "No video arrived. Check that the selected camera is ready, then choose it again.")
        opening?.cancel()
        let work = DispatchWorkItem { [weak self] in
            self?.closeSession()
            self?.startSession(selection, directory: directory, token: token)
        }
        opening = work
        queue.async(execute: work)
    }

    func stop(in surface: Surface) {
        if self.surface == surface { stop() }
    }

    func stop(then completion: @escaping () -> Void = {}) {
        surface = nil
        phase = .idle
        guard ownsCapture else { completion(); return }
        release(then: completion)
    }

    private func release(then completion: @escaping () -> Void = {}) {
        opening?.cancel()
        opening = nil
        watchdog?.cancel()
        watchdog = nil
        generation += 1
        let token = generation
        queue.async { [self] in
            closeSession()
            DispatchQueue.main.async { [self] in
                if generation == token { ownsCapture = false }
                completion()
            }
        }
    }

    private func watch(_ token: Int, seconds: Double, message: String) {
        watchdog?.cancel()
        let timeout = DispatchWorkItem { [weak self] in self?.fail(message, token: token) }
        watchdog = timeout
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds, execute: timeout)
    }

    private func fail(_ message: String, token: Int) {
        guard generation == token else { return }
        phase = .failed(message)
        release()
    }

    private func startSession(_ selection: String, directory: URL, token: Int) {
        func report(_ message: String) {
            DispatchQueue.main.async { [weak self] in self?.fail(message, token: token) }
        }
        guard AVCaptureDevice.authorizationStatus(for: .video) == .authorized else {
            report("Allow camera access to preview this webcam."); return
        }
        let devices = AVCaptureDevice.DiscoverySession(
            deviceTypes: [.builtInWideAngleCamera, .external, .continuityCamera, .deskViewCamera],
            mediaType: .video, position: .unspecified).devices
        guard let device = Self.selectedDevice(selection, devices: devices) else {
            report("This webcam is unavailable. Reconnect it or choose another camera."); return
        }
        let path = directory.appendingPathComponent("webcam-preview.lock").path
        let fd = Darwin.open(path, O_CREAT | O_RDWR | O_NOFOLLOW | O_CLOEXEC, 0o600)
        guard fd >= 0 else { report("The webcam is unavailable for preview."); return }
        let lock = FileHandle(fileDescriptor: fd, closeOnDealloc: true)
        guard flock(fd, LOCK_EX | LOCK_NB) == 0 else {
            report("The webcam is in use by the camera service."); return
        }
        self.lock = lock
        guard let input = try? AVCaptureDeviceInput(device: device) else {
            report("Couldn’t open this webcam. Check that it’s connected and ready, then try again."); return
        }
        let session = AVCaptureSession()
        let output = AVCaptureVideoDataOutput()
        output.alwaysDiscardsLateVideoFrames = true
        session.beginConfiguration()
        guard session.canAddInput(input) else {
            session.commitConfiguration()
            report("This webcam can’t provide a video preview."); return
        }
        session.addInput(input)
        guard session.canAddOutput(output) else {
            session.commitConfiguration()
            report("This webcam can’t provide a video preview."); return
        }
        session.addOutput(output)
        session.commitConfiguration()

        self.session = session
        self.output = output
        let signal = WebcamFrameSignal { [weak self, weak session] in
            DispatchQueue.main.async {
                guard let self, self.generation == token, let session else { return }
                if case .opening = self.phase { self.phase = .live(session) }
                self.watch(token, seconds: 5, message: "The webcam stopped sending video. Choose it again to retry.")
            }
        }
        frameSignal = signal
        output.setSampleBufferDelegate(signal, queue: frames)
        let notifications = NotificationCenter.default
        observers = [
            notifications.addObserver(forName: AVCaptureDevice.wasDisconnectedNotification, object: device, queue: .main) { [weak self] _ in
                self?.fail("This webcam disconnected. Reconnect it or choose another camera.", token: token)
            },
            notifications.addObserver(forName: AVCaptureSession.runtimeErrorNotification, object: session, queue: .main) { [weak self] _ in
                self?.fail("The webcam stopped sending video. Check that it’s ready, then choose it again.", token: token)
            }
        ]
        session.startRunning()
    }

    private static func selectedDevice(_ selection: String, devices: [AVCaptureDevice]) -> AVCaptureDevice? {
        if selection == "auto" {
            let builtIn = devices.filter { $0.deviceType == .builtInWideAngleCamera && !$0.isContinuityCamera }
            return builtIn.count == 1 ? builtIn.first : nil
        }
        let matches: [AVCaptureDevice]
        if selection.hasPrefix("uid:") {
            var encoded = String(selection.dropFirst(4)).replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
            encoded += String(repeating: "=", count: (4 - encoded.utf8.count % 4) % 4)
            guard let data = Data(base64Encoded: encoded), let uid = String(data: data, encoding: .utf8) else { return nil }
            matches = devices.filter { $0.uniqueID == uid }
        } else {
            matches = devices.filter { $0.localizedName == selection }
        }
        return matches.count == 1 ? matches.first : nil
    }

    private func closeSession() {
        observers.forEach(NotificationCenter.default.removeObserver)
        observers.removeAll()
        output?.setSampleBufferDelegate(nil, queue: nil)
        session?.stopRunning()
        output = nil
        frameSignal = nil
        session = nil
        try? lock?.close()
        lock = nil
    }

}

private final class WebcamFrameSignal: NSObject, AVCaptureVideoDataOutputSampleBufferDelegate {
    private let onFrame: () -> Void
    private var lastSignal: TimeInterval?

    init(onFrame: @escaping () -> Void) { self.onFrame = onFrame }

    func captureOutput(_ output: AVCaptureOutput, didOutput sampleBuffer: CMSampleBuffer, from connection: AVCaptureConnection) {
        guard CMSampleBufferGetImageBuffer(sampleBuffer) != nil else { return }
        let now = ProcessInfo.processInfo.systemUptime
        if let lastSignal, now - lastSignal < 1 { return }
        lastSignal = now
        onFrame()
    }
}

struct WebcamPreviewView: NSViewRepresentable {
    let session: AVCaptureSession

    final class Surface: NSView {
        override func makeBackingLayer() -> CALayer { AVCaptureVideoPreviewLayer() }
    }

    func makeNSView(context: Context) -> Surface {
        let view = Surface()
        view.wantsLayer = true
        (view.layer as? AVCaptureVideoPreviewLayer)?.videoGravity = .resizeAspect
        return view
    }

    func updateNSView(_ view: Surface, context: Context) {
        guard let layer = view.layer as? AVCaptureVideoPreviewLayer else { return }
        if layer.session !== session { layer.session = session }
        if let connection = layer.connection, connection.isVideoMirroringSupported {
            connection.automaticallyAdjustsVideoMirroring = false
            connection.isVideoMirrored = false
        }
    }

    static func dismantleNSView(_ view: Surface, coordinator: ()) {
        (view.layer as? AVCaptureVideoPreviewLayer)?.session = nil
    }
}
