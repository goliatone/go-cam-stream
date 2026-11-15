import Foundation
import CoreGraphics
import Darwin

struct PreviewEndpoint: Decodable {
    let version: Int
    let socket: String
    let generation: String
    var identifier: UInt64? {
        guard version == 1, generation.count == 16,
              let value = UInt64(generation, radix: 16), value != 0 else { return nil }
        return value
    }
}

struct PreviewHeader {
    let generation: UInt64
    let sequence: UInt64
    let timestamp: UInt64
    let width: Int
    let height: Int
    let stride: Int
    let size: Int
    init?(_ data: Data, generation expected: UInt64, after sequence: UInt64) {
        guard data.count == 48 else { return nil }
        let bytes = Array(data)
        func number(_ start: Int, _ count: Int) -> UInt64 {
            bytes[start..<(start + count)].reduce(0) { ($0 << 8) | UInt64($1) }
        }
        guard Array(bytes[0..<4]) == Array("CSPR".utf8), number(4, 2) == 1, number(6, 2) == 48,
              number(8, 8) == expected, number(16, 8) > sequence,
              (1...640).contains(number(32, 4)), (1...360).contains(number(36, 4)) else { return nil }
        generation = expected; self.sequence = number(16, 8); timestamp = number(24, 8)
        width = Int(number(32, 4)); height = Int(number(36, 4)); stride = Int(number(40, 4)); size = Int(number(44, 4))
        guard stride == width * 4, size == stride * height, size <= 921600 else { return nil }
    }
    func image(_ pixels: Data) -> CGImage? {
        guard pixels.count == size, let provider = CGDataProvider(data: pixels as CFData) else { return nil }
        return CGImage(width: width, height: height, bitsPerComponent: 8, bitsPerPixel: 32,
                       bytesPerRow: stride, space: CGColorSpaceCreateDeviceRGB(),
                       bitmapInfo: CGBitmapInfo.byteOrder32Little.union(CGBitmapInfo(rawValue: CGImageAlphaInfo.premultipliedFirst.rawValue)),
                       provider: provider, decode: nil, shouldInterpolate: true, intent: .defaultIntent)
    }
}

// One background reader, one pending image and at most one main-queue delivery.
// Stop uses shutdown; only the worker closes the descriptor, preventing fd reuse races.
final class PreviewReader {
    private let lock = NSLock()
    private var descriptor: Int32 = -1
    private var running = true
    private var pending: CGImage?
    private var scheduled = false
    private let onFrame: (CGImage) -> Void
    private let onFailure: () -> Void
    init(onFrame: @escaping (CGImage) -> Void, onFailure: @escaping () -> Void) {
        self.onFrame = onFrame; self.onFailure = onFailure
    }
    func start(_ endpoint: PreviewEndpoint) {
        DispatchQueue.global(qos: .userInitiated).async { [self] in
            read(endpoint)
            lock.lock(); let failed = running; lock.unlock()
            if failed { DispatchQueue.main.async { [weak self] in
                guard let self = self, self.isRunning else { return }
                self.onFailure()
            } }
        }
    }
    func stop() {
        lock.lock(); running = false; pending = nil
        if descriptor >= 0 { _ = Darwin.shutdown(descriptor, SHUT_RDWR) }
        lock.unlock()
    }
    private var isRunning: Bool {
        lock.lock(); defer { lock.unlock() }; return running
    }
    private func read(_ endpoint: PreviewEndpoint) {
        guard let generation = endpoint.identifier,
              endpoint.socket.hasPrefix("/tmp/camstream-camera-\(getuid())-"),
              URL(fileURLWithPath: endpoint.socket).lastPathComponent == "preview-\(endpoint.generation).sock" else { return }
        var address = sockaddr_un()
        let path = Array(endpoint.socket.utf8CString)
        guard path.count <= MemoryLayout.size(ofValue: address.sun_path) else { return }
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutableBytes(of: &address.sun_path) { target in
            for (index, byte) in path.enumerated() { target[index] = UInt8(bitPattern: byte) }
        }
        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { return }
        lock.lock()
        if !running { lock.unlock(); Darwin.close(fd); return }
        descriptor = fd; lock.unlock()
        defer { lock.lock(); descriptor = -1; Darwin.close(fd); lock.unlock() }
        var timeout = timeval(tv_sec: 8, tv_usec: 0)
        guard setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size)) == 0 else { return }
        let connected = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard connected == 0, isRunning else { return }
        var sequence: UInt64 = 0
        while isRunning, let bytes = readExactly(fd, count: 48),
              let header = PreviewHeader(bytes, generation: generation, after: sequence) {
            let age = Date().timeIntervalSince1970 - Double(header.timestamp) / 1000
            guard age >= -5, age < 5, let pixels = readExactly(fd, count: header.size),
                  let image = header.image(pixels) else { return }
            sequence = header.sequence
            timeout.tv_sec = 5
            guard setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size)) == 0 else { return }
            deliver(image)
        }
    }
    private func readExactly(_ fd: Int32, count: Int) -> Data? {
        var bytes = Data(count: count)
        var offset = 0
        let deadline = ProcessInfo.processInfo.systemUptime + 8
        while offset < count {
            guard isRunning, ProcessInfo.processInfo.systemUptime < deadline else { return nil }
            let amount = bytes.withUnsafeMutableBytes { data in
                Darwin.read(fd, data.baseAddress!.advanced(by: offset), count - offset)
            }
            if amount < 0 && errno == EINTR { continue }
            guard amount > 0 else { return nil }
            offset += amount
        }
        return bytes
    }
    private func deliver(_ image: CGImage) {
        lock.lock()
        guard running else { lock.unlock(); return }
        pending = image
        if scheduled { lock.unlock(); return }
        scheduled = true; lock.unlock()
        DispatchQueue.main.async { [weak self] in
            guard let self = self else { return }
            self.lock.lock()
            let image = self.running ? self.pending : nil
            self.pending = nil; self.scheduled = false; self.lock.unlock()
            if let image = image { self.onFrame(image) }
        }
    }
}
