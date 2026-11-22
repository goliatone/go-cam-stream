import Combine
import Foundation
import Network

struct ServerEndpoint: Equatable {
    let viewer: URL
    let health: URL

    init?(_ address: String) {
        let value = address.trimmingCharacters(in: .whitespacesAndNewlines)
        guard var parts = URLComponents(string: value, encodingInvalidCharacters: false),
              let scheme = parts.scheme?.lowercased(), let host = parts.host?.lowercased(), !host.isEmpty,
              parts.user == nil, parts.password == nil, parts.query == nil, parts.fragment == nil,
              parts.percentEncodedPath.isEmpty || parts.percentEncodedPath == "/",
              parts.port.map({ (1...65535).contains($0) }) ?? true else { return nil }
        let loopback = host == "localhost" || host == "[::1]" || IPv4Address(host)?.rawValue.first == 127
        guard scheme == "https" || (scheme == "http" && loopback) else { return nil }
        parts.scheme = scheme
        parts.host = host
        parts.path = "/"
        guard let viewer = parts.url else { return nil }
        parts.path = loopback ? "/healthz" : "/api/health"
        guard let health = parts.url else { return nil }
        self.viewer = viewer
        self.health = health
    }
}

enum ServerStatus: Equatable {
    case unchecked, reachable, accessDenied, unavailable
}

enum ServerProbe {
    private struct Health: Decodable {
        let ok: Bool
        let playback_ready: Bool?
    }

    private final class NoRedirects: NSObject, URLSessionTaskDelegate {
        func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                        newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
            completionHandler(nil)
        }
    }

    private static let session: URLSession = {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 2
        configuration.timeoutIntervalForResource = 2
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.urlCache = nil
        configuration.httpShouldSetCookies = false
        configuration.urlCredentialStorage = nil
        return URLSession(configuration: configuration, delegate: NoRedirects(), delegateQueue: nil)
    }()

    static func check(_ url: URL) async -> ServerStatus {
        do {
            let (bytes, response) = try await session.bytes(from: url)
            defer { bytes.task.cancel() }
            guard let response = response as? HTTPURLResponse else { return .unavailable }
            switch response.statusCode {
            case 401, 403: return .accessDenied
            case 200: break
            default: return .unavailable
            }
            var body = Data()
            for try await byte in bytes {
                guard body.count < 4096 else { return .unavailable }
                body.append(byte)
            }
            let health = try JSONDecoder().decode(Health.self, from: body)
            guard health.ok, url.path != "/api/health" || health.playback_ready != nil else { return .unavailable }
            return .reachable
        } catch {
            return .unavailable
        }
    }
}

@MainActor
final class ServerMonitor: ObservableObject {
    static let defaultAddress = "http://127.0.0.1:8081/"
    private static let addressKey = "camstreamServerURL"

    @Published var address: String {
        didSet {
            guard address != oldValue else { return }
            if let endpoint {
                defaults.set(endpoint.viewer.absoluteString, forKey: Self.addressKey)
            } else if addressIsEmpty {
                defaults.set("", forKey: Self.addressKey)
            }
            guard endpoint != ServerEndpoint(oldValue) else { return }
            status = .unchecked
            schedule(after: .milliseconds(400))
        }
    }
    @Published private(set) var status: ServerStatus = .unchecked
    @Published private(set) var checking = false

    private let defaults: UserDefaults
    private let interval: Duration
    private let probe: (URL) async -> ServerStatus
    private var polling: Task<Void, Never>?
    private var visible = false

    init(defaults: UserDefaults = .standard, interval: Duration = .seconds(15),
         probe: @escaping (URL) async -> ServerStatus = ServerProbe.check) {
        self.defaults = defaults
        self.interval = interval
        self.probe = probe
        address = defaults.string(forKey: Self.addressKey) ?? Self.defaultAddress
    }

    deinit { polling?.cancel() }

    private var endpoint: ServerEndpoint? { ServerEndpoint(address) }
    private var addressIsEmpty: Bool { address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    var canCheck: Bool { endpoint != nil && !checking }
    var viewerURL: URL? { status == .reachable ? endpoint?.viewer : nil }

    var message: String {
        if addressIsEmpty { return "Enter your server’s address." }
        if endpoint == nil { return "Enter a local HTTP or an HTTPS server URL, without a path." }
        if checking { return "Checking the server…" }
        switch status {
        case .unchecked: return "Waiting to check the server."
        case .reachable: return "The server is reachable."
        case .accessDenied: return "Access denied. Check your server access and try again."
        case .unavailable: return "The server didn’t respond. Check the URL and try again."
        }
    }

    func setVisible(_ visible: Bool) {
        guard self.visible != visible else { return }
        self.visible = visible
        schedule()
    }

    func checkNow() {
        guard canCheck else { return }
        schedule()
    }

    private func schedule(after delay: Duration = .zero) {
        polling?.cancel()
        polling = nil
        checking = false
        guard visible, let endpoint else { return }
        polling = Task { [weak self, interval, probe] in
            do {
                if delay != .zero { try await Task.sleep(for: delay) }
                while !Task.isCancelled, self != nil {
                    self?.checking = true
                    let result = await probe(endpoint.health)
                    try Task.checkCancellation()
                    self?.status = result
                    self?.checking = false
                    try await Task.sleep(for: interval)
                }
            } catch {
                // A replacement task or a hidden window owns the next check.
            }
        }
    }
}
