import Foundation

public enum BuildChannel: String, Codable, Sendable {
    case development
    case release
}

/// One ipc-v1 response document (shared/contracts/ipc-v1.json). The same JSON travels
/// through MobileCall in the app and, from round 2, through provider messages to the tunnel.
public struct CoreResponse: Decodable, Sendable {
    public struct Failure: Decodable, Sendable, Equatable {
        public let code: String
        public let message: String
    }

    public let requestID: String
    public let protocolVersion: Int
    public let generation: UInt64
    public let ok: Bool
    public let error: Failure?
    let result: RawJSON?

    enum CodingKeys: String, CodingKey {
        case requestID = "request_id", protocolVersion = "protocol_version", generation, ok, error, result
    }

    public func decodeResult<T: Decodable>(_ type: T.Type) throws -> T {
        guard ok, let result else { throw CoreProtocolError.noResult(error?.code) }
        return try JSONDecoder().decode(T.self, from: result.data)
    }
}

public enum CoreProtocolError: Error, Equatable {
    case noResult(String?)
}

/// Non-secret build identity the Core answers to hello.
public struct CoreHello: Decodable, Sendable, Equatable {
    public let component: String
    public let coreVersion: String
    public let protocolVersion: Int
    public let minProtocolVersion: Int
    public let maxProtocolVersion: Int
    public let mihomoRevision: String

    enum CodingKeys: String, CodingKey {
        case component, coreVersion = "core_version", protocolVersion = "protocol_version"
        case minProtocolVersion = "min_protocol_version", maxProtocolVersion = "max_protocol_version"
        case mihomoRevision = "mihomo_revision"
    }
}

public enum CoreRequest {
    public static let protocolVersion = 1

    /// The hello request body; it needs no running session.
    public static func hello(requestID: String, generation: UInt64 = 0) -> String {
        #"{"request_id":"\#(requestID)","protocol_version":\#(protocolVersion),"generation":\#(generation),"command":"hello"}"#
    }

    /// Any ipc-v1 command. The payload is serialised by JSONSerialization, so user input (an
    /// email, a password) can never break the document.
    public static func command(_ command: String, requestID: String, generation: UInt64 = 0, payload: [String: String]? = nil) -> String {
        var object: [String: Any] = ["request_id": requestID, "protocol_version": protocolVersion, "generation": generation, "command": command]
        if let payload { object["payload"] = payload }
        let data = (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data()
        return String(decoding: data, as: UTF8.self)
    }

    public static func login(requestID: String, email: String, password: String, apiBase: String, channel: BuildChannel,
                             deviceID: String, appVersion: String) -> String {
        command("login", requestID: requestID, payload: [
            "email": email, "password": password, "api_base": apiBase, "channel": channel.rawValue,
            "device_id": deviceID, "app_version": appVersion,
        ])
    }

    public static func selectNode(requestID: String, generation: UInt64, nodeID: String, catalogRevision: String) -> String {
        command("select_node", requestID: requestID, generation: generation, payload: ["node_id": nodeID, "catalog_revision": catalogRevision])
    }
}

/// The parts of a snapshot (shared/contracts/snapshot-v1.json) the session plumbing needs;
/// the full view model arrives with the round-3 interface. `released` means this process handed
/// its session to the other one (handoff-v1).
public struct SnapshotHead: Decodable, Sendable, Equatable {
    public struct Node: Decodable, Sendable, Equatable {
        public let id: String
        public let name: String
    }

    public let generation: UInt64
    public let state: String
    public let selectedNodeID: String
    public let catalogRevision: String
    public let tunnelHealth: String
    public let nodes: [Node]
    public let lastError: CoreResponse.Failure?
    public let actionError: CoreResponse.Failure?

    enum CodingKeys: String, CodingKey {
        case generation, state, nodes
        case selectedNodeID = "selected_node_id", catalogRevision = "catalog_revision", tunnelHealth = "tunnel_health"
        case lastError = "last_error", actionError = "action_error"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        generation = try c.decode(UInt64.self, forKey: .generation)
        state = try c.decode(String.self, forKey: .state)
        selectedNodeID = try c.decodeIfPresent(String.self, forKey: .selectedNodeID) ?? ""
        catalogRevision = try c.decodeIfPresent(String.self, forKey: .catalogRevision) ?? ""
        tunnelHealth = try c.decodeIfPresent(String.self, forKey: .tunnelHealth) ?? ""
        nodes = try c.decodeIfPresent([Node].self, forKey: .nodes) ?? []
        lastError = try c.decodeIfPresent(CoreResponse.Failure.self, forKey: .lastError)
        actionError = try c.decodeIfPresent(CoreResponse.Failure.self, forKey: .actionError)
    }
}

/// The result of ExportSession: the opaque, secret ticket (memory only).
public struct ExportedTicket: Decodable, Sendable {
    public let ticket: String
}

/// Keeps a nested JSON value as raw bytes so it can be decoded later into its own type.
struct RawJSON: Decodable, Sendable {
    let data: Data

    init(from decoder: Decoder) throws {
        let value = try decoder.singleValueContainer().decode(AnyJSON.self)
        data = try JSONSerialization.data(withJSONObject: value.object, options: [.fragmentsAllowed])
    }
}

private struct AnyJSON: Decodable {
    let object: Any

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { object = NSNull() }
        else if let v = try? c.decode(Bool.self) { object = v }
        else if let v = try? c.decode(Int64.self) { object = v }
        else if let v = try? c.decode(Double.self) { object = v }
        else if let v = try? c.decode(String.self) { object = v }
        else if let v = try? c.decode([AnyJSON].self) { object = v.map(\.object) }
        else { object = try c.decode([String: AnyJSON].self).mapValues(\.object) }
    }
}
