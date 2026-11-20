import Foundation

enum SharingMethod: String, Codable {
    case create = "sharing_create"
    case copyLink = "sharing_copy_link"
    case status = "sharing_status"
    case revoke = "sharing_revoke"
    case setup = "sharing_setup"
    case configure = "sharing_configure"
    case testRelay = "sharing_test_relay"
    case hostSleep = "sharing_host_sleep"
}

enum SharingDuration: Int, Codable {
    case fifteenMinutes = 900
    case oneHour = 3600
    case eightHours = 28800
}

struct NativeSharingOptions: Codable {
    var durationSeconds: SharingDuration?
    var viewerLimit: Int?

    enum CodingKeys: String, CodingKey {
        case durationSeconds = "duration_seconds"
        case viewerLimit = "viewer_limit"
    }
}

struct NativeSharingSettings: Codable {
    struct Ingress: Codable {
        var mode: String
        var publicBaseURL: String?
        var guestListen: String?

        enum CodingKeys: String, CodingKey {
            case mode
            case publicBaseURL = "public_base_url"
            case guestListen = "guest_listen"
        }
    }

    struct Relay: Codable {
        var mode: String
        var credentialRef: String?
        var urls: [String]?

        enum CodingKeys: String, CodingKey {
            case mode, urls
            case credentialRef = "credential_ref"
        }
    }

    var ingress: Ingress
    var relay: Relay
}

struct NativeSharingRequest: Codable {
    struct RelayCredentials: Codable, CustomStringConvertible, CustomDebugStringConvertible {
        var keyID: String? = nil
        var apiToken: String? = nil
        var sharedSecret: String? = nil

        var description: String { "[relay input redacted]" }
        var debugDescription: String { description }

        enum CodingKeys: String, CodingKey {
            case keyID = "key_id"
            case apiToken = "api_token"
            case sharedSecret = "shared_secret"
        }
    }

    struct Arguments: Codable {
        var cameraID: String? = nil
        var shareID: String? = nil
        var options: NativeSharingOptions? = nil
        var settings: NativeSharingSettings? = nil
        var relayCredentials: RelayCredentials? = nil

        enum CodingKeys: String, CodingKey {
            case cameraID = "camera_id"
            case shareID = "share_id"
            case options, settings
            case relayCredentials = "relay_credentials"
        }
    }

    let version: Int
    let id: String
    let method: SharingMethod
    let sharing: Arguments?
}

struct NativeSharingError: Decodable {
    let code: String
    let message: String
    let retryable: Bool
}

enum NativeSharingAudience: String, Decodable {
    case publicInternet = "public"
    case privateTailnet = "private_tailnet"
}

struct NativeSharingView: Decodable {
    enum State: String, Decodable { case stopped, starting, available, degraded, expired, revoked, error }
    enum Media: String, Decodable { case idle, connecting, live, reconnecting, unavailable }

    let shareID: String?
    let cameraID: String
    let state: State
    let media: Media
    let audience: NativeSharingAudience?
    let expiresAt: String?
    let viewers: Int
    let viewerLimit: Int
    let error: NativeSharingError?

    enum CodingKeys: String, CodingKey {
        case shareID = "share_id"
        case cameraID = "camera_id"
        case state, media, audience, viewers, error
        case expiresAt = "expires_at"
        case viewerLimit = "viewer_limit"
    }
}

struct NativeSharingLink: Decodable {
    let url: String
}

struct NativeSharingCreated: Decodable {
    let share: NativeSharingView
    let link: NativeSharingLink
}

struct NativeSharingSetup: Decodable {
    enum State: String, Decodable { case setupNeeded = "setup_needed", ready, error }
    struct IngressCapability: Decodable {
        let mode: String
        let audience: NativeSharingAudience
    }
    struct Saved: Decodable {
        let publicBaseURL: String?
        let guestListen: String?
        let relayMode: String
        let relayURLs: [String]?
        let relayCredentialSet: Bool

        enum CodingKeys: String, CodingKey {
            case publicBaseURL = "public_base_url"
            case guestListen = "guest_listen"
            case relayMode = "relay_mode"
            case relayURLs = "relay_urls"
            case relayCredentialSet = "relay_credential_set"
        }
    }

    let ingressModes: [IngressCapability]
    let ingressMode: String
    let ingressState: State
    let relayState: State
    let saved: Saved?
    let error: NativeSharingError?

    enum CodingKeys: String, CodingKey {
        case ingressModes = "ingress_modes"
        case ingressMode = "ingress_mode"
        case ingressState = "ingress_state"
        case relayState = "relay_state"
        case saved, error
    }
}

struct NativeSharingReady: Decodable {
    let protocolVersion: Int
    let preview: Int?
    let audio: Int?
    let sharing: Int?

    enum CodingKeys: String, CodingKey {
        case protocolVersion = "protocol"
        case preview, audio, sharing
    }
}

struct NativeSharingEvent<Result: Decodable>: Decodable {
    let version: Int
    let event: String
    let result: Result
}

struct NativeSharingReply<Result: Decodable>: Decodable {
    let version: Int
    let id: String
    let result: Result?
    let error: NativeSharingError?
}
