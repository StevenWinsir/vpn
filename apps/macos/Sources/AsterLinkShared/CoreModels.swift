import Foundation

// Wire types for the Core's redacted snapshot (shared/contracts/snapshot-v1.json).
// There is deliberately no field for a token, password, YAML, server or port.

public enum CoreState: String, Codable, Sendable {
    case signedOut = "signed_out"
    case authenticating
    case ready
    case preparing
    case connecting
    case connected
    case stopping
    case blocked
    case error
}

public struct CoreUser: Codable, Sendable, Equatable {
    public var id: String
    public var email: String
    public var name: String
    public var role: String
}

public struct CoreNode: Codable, Sendable, Equatable, Identifiable {
    public var id: String
    public var name: String
    public var region: String
    public var lineType: String
    public var ratePermille: Int64

    enum CodingKeys: String, CodingKey {
        case id, name, region, lineType = "line_type", ratePermille = "rate_permille"
    }

    /// `direct` is the ordinary routed line, NOT a Clash DIRECT bypass.
    public var isDedicated: Bool { lineType == "dedicated" }
    public var lineLabel: String { isDedicated ? "专线" : "普通线路" }
    /// 500‰ → "0.5×", 1000‰ → "1×".
    public var rateLabel: String { Self.formatRate(ratePermille) }

    public static func formatRate(_ permille: Int64) -> String {
        let whole = permille / 1000
        let fraction = permille % 1000
        if fraction == 0 { return "\(whole)×" }
        var digits = String(format: "%03d", fraction)
        while digits.hasSuffix("0") { digits.removeLast() }
        return "\(whole).\(digits)×"
    }
}

public struct CoreEntitlement: Codable, Sendable, Equatable {
    public var planName: String
    public var subscriptionExpiresAt: Date?
    public var authorizationExpiresAt: Date?
    public var leaseRemainingSeconds: Int64
    public var accountAuthorized: Bool
    public var canConnect: Bool
    public var reason: String
    public var reasonMessage: String
    public var totalBytes: Int64
    public var remainingBytes: Int64
    public var usedUnits: Int64

    enum CodingKeys: String, CodingKey {
        case planName = "plan_name", subscriptionExpiresAt = "subscription_expires_at"
        case authorizationExpiresAt = "authorization_expires_at", leaseRemainingSeconds = "lease_remaining_seconds"
        case accountAuthorized = "account_authorized", canConnect = "can_connect", reason
        case reasonMessage = "reason_message", totalBytes = "total_bytes", remainingBytes = "remaining_bytes"
        case usedUnits = "used_units"
    }
}

public struct CoreMetering: Codable, Sendable, Equatable {
    public var source: String
    public var uploadBytes: Int64
    public var downloadBytes: Int64
    public var lastSequence: Int64
    public var uploadBytesPerSecond: Int64
    public var downloadBytesPerSecond: Int64

    enum CodingKeys: String, CodingKey {
        case source, uploadBytes = "upload_bytes", downloadBytes = "download_bytes", lastSequence = "last_sequence"
        case uploadBytesPerSecond = "upload_bytes_per_second", downloadBytesPerSecond = "download_bytes_per_second"
    }
}

public struct CoreSnapshot: Codable, Sendable, Equatable {
    public var generation: UInt64
    public var state: CoreState
    public var user: CoreUser?
    public var entitlement: CoreEntitlement?
    public var nodes: [CoreNode]
    public var selectedNodeID: String
    public var catalogRevision: String
    public var profileVersion: String
    public var configReady: Bool
    public var tunnelHealth: String
    public var metering: CoreMetering?
    public var lastError: CoreError?
    public var coreVersion: String

    enum CodingKeys: String, CodingKey {
        case generation, state, user, entitlement, nodes
        case selectedNodeID = "selected_node_id", catalogRevision = "catalog_revision", profileVersion = "profile_version"
        case configReady = "config_ready", tunnelHealth = "tunnel_health", metering
        case lastError = "last_error", coreVersion = "core_version"
    }

    public static let signedOut = CoreSnapshot(
        generation: 0, state: .signedOut, user: nil, entitlement: nil, nodes: [], selectedNodeID: "",
        catalogRevision: "", profileVersion: "", configReady: false, tunnelHealth: "idle", metering: nil,
        lastError: nil, coreVersion: "")

    public var selectedNode: CoreNode? { nodes.first { $0.id == selectedNodeID } }
}

/// What a state-changing command returns: the new snapshot plus, when the action itself
/// was refused, the reason. The snapshot is always present so the UI never shows stale state.
public struct CoreActionReply: Decodable, Sendable {
    public var snapshot: CoreSnapshot
    public var actionError: CoreError?

    enum ExtraKeys: String, CodingKey { case actionError = "action_error" }

    public init(from decoder: Decoder) throws {
        snapshot = try CoreSnapshot(from: decoder)
        actionError = try decoder.container(keyedBy: ExtraKeys.self).decodeIfPresent(CoreError.self, forKey: .actionError)
    }
}

/// One Core response frame. `Result` is the command-specific payload.
public struct CoreEnvelope<Result: Decodable & Sendable>: Decodable, Sendable {
    public var requestID: String
    public var protocolVersion: Int
    public var generation: UInt64
    public var ok: Bool
    public var result: Result?
    public var error: CoreError?

    enum CodingKeys: String, CodingKey {
        case requestID = "request_id", protocolVersion = "protocol_version", generation, ok, result, error
    }
}

public enum CoreClientError: Error, Equatable {
    case malformedReply
    case transport(String)
    case protocolMismatch
}
