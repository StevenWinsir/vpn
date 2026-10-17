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
