import AVFoundation

// Permission queries never create capture inputs. Only the explicit Allow
// actions invoke requestAccess; tests inject a provider with no AVFoundation I/O.
enum CameraMedia { case video, audio }
protocol CameraPermissions {
    func status(_ media: CameraMedia) -> String
    func request(_ media: CameraMedia, completion: @escaping () -> Void)
}
struct SystemCameraPermissions: CameraPermissions {
    func status(_ media: CameraMedia) -> String {
        switch AVCaptureDevice.authorizationStatus(for: media == .audio ? .audio : .video) {
        case .authorized: return "authorized"
        case .denied: return "denied"
        case .restricted: return "restricted"
        default: return "not_determined"
        }
    }
    func request(_ media: CameraMedia, completion: @escaping () -> Void) {
        AVCaptureDevice.requestAccess(for: media == .audio ? .audio : .video) { _ in
            DispatchQueue.main.async(execute: completion)
        }
    }
}
