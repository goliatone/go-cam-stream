import Foundation

enum ConversationMethod: String, Codable {
    case create = "conversation_create"
    case createLocal = "conversation_create_local"
    case checkRoute = "conversation_check_route"
    case connectorStatus = "conversation_connector_status"
    case reconnectConnector = "conversation_connector_reconnect"
    case openLink = "conversation_open_link"
    case status = "conversation_status"
    case end = "conversation_end"
    case leave = "conversation_leave"
}

struct NativeConversationRequest: Encodable, CustomStringConvertible, CustomDebugStringConvertible {
    let version = 1
    let id: String
    let method: ConversationMethod
    var roomID: String?
    var invitationURL: String?
    var setupProgress: Bool? = nil

    var description: String { "[conversation request redacted]" }
    var debugDescription: String { description }

    enum CodingKeys: String, CodingKey {
        case version, id, method
        case roomID = "room_id"
        case invitationURL = "invitation_url"
        case setupProgress = "setup_progress"
    }
}

enum ConversationRole: String, Codable { case host, guest }

enum ConversationState: String, Codable {
    case waiting, joining, connected, reconnecting, ended, expired, failed
}

struct ConversationPublication: Codable, Equatable {
    let microphone: Bool
    let camera: Bool
}

struct ConversationParticipant: Codable, Equatable {
    let role: ConversationRole
    let joined: Bool
    let publication: ConversationPublication
}

struct ConversationRoomView: Codable, Equatable {
    let roomID: String
    let version: Int
    let state: ConversationState
    let generation: UInt32
    let expiresAt: String
    let participants: [ConversationParticipant]

    enum CodingKeys: String, CodingKey {
        case roomID = "room_id"
        case version, state, generation, participants
        case expiresAt = "expires_at"
    }
}

struct ConversationAdmission: Codable, CustomStringConvertible, CustomDebugStringConvertible {
    let room: ConversationRoomView
    let role: ConversationRole
    let token: String

    var description: String { "[conversation admission redacted]" }
    var debugDescription: String { description }
}

struct ConversationPrejoin: Decodable, CustomStringConvertible, CustomDebugStringConvertible {
    let pageURL: URL
    let admission: ConversationAdmission
    let invitationURL: URL?

    var description: String { "[conversation prejoin redacted]" }
    var debugDescription: String { description }

    enum CodingKeys: String, CodingKey {
        case pageURL = "page_url"
        case invitationURL = "invitation_url"
        case admission
    }
}

protocol ConversationHelper: AnyObject {
    var conversationVersion: Int { get }
    var callRecoveryVersion: Int { get }
    var callConnectorVersion: Int { get }
    func requestConversation(_ method: ConversationMethod, roomID: String?, invitation: String?,
                             completion: @escaping ([String: Any]) -> Void)
}

extension ConversationHelper {
    var callRecoveryVersion: Int { 0 }
    var callConnectorVersion: Int { 0 }
}

enum ConversationSetupStage: String {
    case listener
    case publicRoute = "public_route"
    case relay
    case connector

    var message: String {
        switch self {
        case .listener: return "Checking the local call server…"
        case .publicRoute: return "Checking the guest link…"
        case .relay: return "Testing the call relay…"
        case .connector: return "Starting the call connector…"
        }
    }
}

/// Sanitized host setup classification. Older helpers omit it; unknown values
/// from newer helpers decode as nil so the client falls back to neutral text.
enum ConversationSetup: String {
    case notConfigured = "not_configured"
    case invalidConfiguration = "invalid_configuration"
    case runtimeUnavailable = "runtime_unavailable"
    case startFailed = "start_failed"
    case listenerInUse = "listener_in_use"
    case publicRouteUnreachable = "public_route_unreachable"
    case callAssetsUnavailable = "call_assets_unavailable"
    case relayConfigurationInvalid = "invalid_relay_configuration"
    case relayUnavailable = "relay_unavailable"
    case connectorUnavailable = "connector_unavailable"
    case connectorConfigurationInvalid = "invalid_connector_configuration"
}

enum CallConnectorStatus: String {
    case external, stopped, connecting, connected, unavailable
    case setupRequired = "setup_required"

    var title: String {
        switch self {
        case .external: return "External connection"
        case .stopped: return "Call connector stopped"
        case .connecting: return "Call connector connecting…"
        case .connected: return "Call connector connected"
        case .unavailable: return "Call connector unavailable"
        case .setupRequired: return "Call connector needs setup"
        }
    }
}

struct ConversationFault: Decodable {
    let code: String
    let retryable: Bool
    let setup: ConversationSetup?
    let port: UInt16?

    enum CodingKeys: String, CodingKey { case code, retryable, setup, port }
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        code = try container.decode(String.self, forKey: .code)
        retryable = try container.decodeIfPresent(Bool.self, forKey: .retryable) ?? false
        setup = (try? container.decodeIfPresent(String.self, forKey: .setup)).flatMap { $0.flatMap(ConversationSetup.init) }
        port = try? container.decodeIfPresent(UInt16.self, forKey: .port)
    }
}

/// Local call activity published by the trusted call page for one admitted
/// room. It carries no admission token or invitation, only role, phase,
/// whether each local input is on and optional local device names.
struct ConversationCallState: Equatable {
    enum Phase: String { case prejoin, waiting, connecting, connected, reconnecting }
    let role: ConversationRole
    let phase: Phase
    let camera: Bool
    let microphone: Bool
    let cameraLabel: String?
    let microphoneLabel: String?

    private static let keys: Set<String> = ["version", "room", "role", "phase", "camera", "microphone", "camera_label", "microphone_label"]

    init(role: ConversationRole, phase: Phase, camera: Bool, microphone: Bool, cameraLabel: String? = nil, microphoneLabel: String? = nil) {
        self.role = role; self.phase = phase; self.camera = camera; self.microphone = microphone
        self.cameraLabel = cameraLabel; self.microphoneLabel = microphoneLabel
    }

    /// Accepts only version 1 for the bound room and role, with a closed key
    /// set, real JSON booleans and bounded printable labels.
    init?(payload: Any?, room: String, role: ConversationRole) {
        guard let value = payload as? [String: Any], Set(value.keys).isSubset(of: Self.keys),
              Self.number(value["version"]) == 1.0, value["room"] as? String == room,
              (value["role"] as? String).flatMap(ConversationRole.init) == role,
              let phase = (value["phase"] as? String).flatMap(Phase.init),
              let camera = Self.boolean(value["camera"]), let microphone = Self.boolean(value["microphone"]),
              let cameraLabel = Self.label(value["camera_label"]), let microphoneLabel = Self.label(value["microphone_label"])
        else { return nil }
        self.init(role: role, phase: phase, camera: camera, microphone: microphone,
                  cameraLabel: cameraLabel, microphoneLabel: microphoneLabel)
    }

    private static func boolean(_ value: Any?) -> Bool? {
        guard let number = value as? NSNumber, CFGetTypeID(number) == CFBooleanGetTypeID() else { return nil }
        return number.boolValue
    }
    private static func number(_ value: Any?) -> Double? {
        guard let number = value as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID() else { return nil }
        return number.doubleValue
    }
    /// Outer nil rejects the payload. A missing label is valid; a present one
    /// must be short printable text.
    private static func label(_ value: Any?) -> String?? {
        guard let value else { return .some(nil) }
        guard let text = value as? String, text.utf8.count <= 128,
              !text.unicodeScalars.contains(where: { $0.properties.generalCategory == .control }) else { return nil }
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        return .some(trimmed.isEmpty ? nil : trimmed)
    }
}

/// What the native shell can truthfully say about the current conversation.
/// Nil inputs mean unknown, such as a browser-hosted room or a page that has
/// not reported yet while it holds the device lease.
struct ConversationActivity: Equatable {
    enum Phase: String { case prejoin, waiting, connecting, connected, reconnecting, unknown }
    let role: ConversationRole
    let phase: Phase
    let camera: Bool?
    let microphone: Bool?
    var cameraLabel: String? = nil
    var microphoneLabel: String? = nil
    var hostedInBrowser = false
}
