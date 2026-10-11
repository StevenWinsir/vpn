import Foundation

public enum IPCFrameError: Error, Equatable {
    case empty
    case tooLarge(Int)
    case truncated
}

/// 4-byte big-endian length + one JSON document; mirrors native/core/internal/ipc.
public enum IPCFrame {
    public static func encode(_ body: Data) throws -> Data {
        guard !body.isEmpty else { throw IPCFrameError.empty }
        guard body.count <= AsterLinkIdentity.maxFrame else { throw IPCFrameError.tooLarge(body.count) }
        var length = UInt32(body.count).bigEndian
        var frame = Data(bytes: &length, count: 4)
        frame.append(body)
        return frame
    }

    /// Returns the declared body length after validating it, so a caller never reads
    /// or allocates more than the limit.
    public static func declaredLength(header: Data) throws -> Int {
        guard header.count == 4 else { throw IPCFrameError.truncated }
        let size = header.withUnsafeBytes { UInt32(bigEndian: $0.loadUnaligned(as: UInt32.self)) }
        guard size > 0 else { throw IPCFrameError.empty }
        guard size <= UInt32(AsterLinkIdentity.maxFrame) else { throw IPCFrameError.tooLarge(Int(size)) }
        return Int(size)
    }

    /// Decodes exactly one complete frame from a buffer.
    public static func decode(_ data: Data) throws -> Data {
        guard data.count >= 4 else { throw IPCFrameError.truncated }
        let length = try declaredLength(header: data.prefix(4))
        guard data.count == 4 + length else { throw IPCFrameError.truncated }
        return data.suffix(from: data.startIndex + 4)
    }
}

public struct CoreRequest: Codable, Sendable {
    public var requestID: String
    public var protocolVersion: Int
    public var generation: UInt64
    public var command: String

    enum CodingKeys: String, CodingKey {
        case requestID = "request_id", protocolVersion = "protocol_version", generation, command
    }

    public init(requestID: String, command: String, generation: UInt64 = 0,
                protocolVersion: Int = AsterLinkIdentity.protocolVersion) {
        self.requestID = requestID
        self.command = command
        self.generation = generation
        self.protocolVersion = protocolVersion
    }
}

public struct CoreError: Codable, Sendable, Equatable {
    public var code: String
    public var message: String
}

public struct CoreHello: Codable, Sendable, Equatable {
    public var component: String
    public var coreVersion: String
    public var protocolVersion: Int
    public var minProtocolVersion: Int
    public var maxProtocolVersion: Int
    public var mihomoRevision: String

    enum CodingKeys: String, CodingKey {
        case component, coreVersion = "core_version", protocolVersion = "protocol_version"
        case minProtocolVersion = "min_protocol_version", maxProtocolVersion = "max_protocol_version"
        case mihomoRevision = "mihomo_revision"
    }
}

public struct CoreResponse: Codable, Sendable {
    public var requestID: String
    public var protocolVersion: Int
    public var generation: UInt64
    public var ok: Bool
    public var result: CoreHello?
    public var error: CoreError?

    enum CodingKeys: String, CodingKey {
        case requestID = "request_id", protocolVersion = "protocol_version", generation, ok, result, error
    }
}
