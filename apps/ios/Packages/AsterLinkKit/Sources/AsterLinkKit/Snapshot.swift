import Foundation

// Mirrors shared/contracts/snapshot-v1.json (Go: native/core/internal/managed/snapshot.go;
// Android: core/Snapshot.kt). The snapshot is redacted by construction: no field can carry a
// token, password, YAML, server address, port or node credential. Every integer is an Int64 so
// counters above 2^53 survive; nothing goes through a Double.

public struct UserView: Codable, Sendable, Equatable {
    public var id = ""
    public var email = ""
    public var name = ""
    public var role = ""

    public init(id: String = "", email: String = "", name: String = "", role: String = "") {
        self.id = id; self.email = email; self.name = name; self.role = role
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        email = try c.decodeIfPresent(String.self, forKey: .email) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        role = try c.decodeIfPresent(String.self, forKey: .role) ?? ""
    }
}

public struct NodeView: Codable, Sendable, Equatable, Identifiable {
    public var id = ""
    public var name = ""
    public var region = ""
    /// "dedicated" or "direct". "direct" is the ordinary routed line, NOT a Mihomo DIRECT bypass.
    public var lineType = ""
    public var ratePermille: Int64 = 0
    /// "node_reported" when billed from the node's own counters; empty otherwise.
    public var meteringSource = ""

    enum CodingKeys: String, CodingKey {
        case id, name, region
        case lineType = "line_type", ratePermille = "rate_permille", meteringSource = "metering_source"
    }

    public init(id: String = "", name: String = "", region: String = "", lineType: String = "", ratePermille: Int64 = 0, meteringSource: String = "") {
        self.id = id; self.name = name; self.region = region; self.lineType = lineType
        self.ratePermille = ratePermille; self.meteringSource = meteringSource
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        region = try c.decodeIfPresent(String.self, forKey: .region) ?? ""
        lineType = try c.decodeIfPresent(String.self, forKey: .lineType) ?? ""
        ratePermille = try c.decodeIfPresent(Int64.self, forKey: .ratePermille) ?? 0
        meteringSource = try c.decodeIfPresent(String.self, forKey: .meteringSource) ?? ""
    }

    public var isDedicated: Bool { lineType == "dedicated" }
}

public struct EntitlementView: Codable, Sendable, Equatable {
    public var planName = ""
    public var subscriptionExpiresAt: String?
    public var authorizationExpiresAt: String?
    public var leaseRemainingSeconds: Int64 = 0
    /// The account may use the proxy (ignores whether a line is chosen).
    public var accountAuthorized = false
    /// Every gate for pressing Connect right now is satisfied.
    public var canConnect = false
    public var reason = ""
    public var reasonMessage = ""
    public var totalBytes: Int64 = 0
    public var remainingBytes: Int64 = 0
    /// 1 unit = 1/1000 byte (a 1000 permille line charges 1000 units per byte).
    public var usedUnits: Int64 = 0

    enum CodingKeys: String, CodingKey {
        case planName = "plan_name", subscriptionExpiresAt = "subscription_expires_at"
        case authorizationExpiresAt = "authorization_expires_at", leaseRemainingSeconds = "lease_remaining_seconds"
        case accountAuthorized = "account_authorized", canConnect = "can_connect", reason
        case reasonMessage = "reason_message", totalBytes = "total_bytes", remainingBytes = "remaining_bytes"
        case usedUnits = "used_units"
    }

    public init(planName: String = "", subscriptionExpiresAt: String? = nil, authorizationExpiresAt: String? = nil,
                leaseRemainingSeconds: Int64 = 0, accountAuthorized: Bool = false, canConnect: Bool = false,
                reason: String = "", reasonMessage: String = "", totalBytes: Int64 = 0, remainingBytes: Int64 = 0, usedUnits: Int64 = 0) {
        self.planName = planName; self.subscriptionExpiresAt = subscriptionExpiresAt
        self.authorizationExpiresAt = authorizationExpiresAt; self.leaseRemainingSeconds = leaseRemainingSeconds
        self.accountAuthorized = accountAuthorized; self.canConnect = canConnect; self.reason = reason
        self.reasonMessage = reasonMessage; self.totalBytes = totalBytes; self.remainingBytes = remainingBytes
        self.usedUnits = usedUnits
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        planName = try c.decodeIfPresent(String.self, forKey: .planName) ?? ""
        subscriptionExpiresAt = try c.decodeIfPresent(String.self, forKey: .subscriptionExpiresAt)
        authorizationExpiresAt = try c.decodeIfPresent(String.self, forKey: .authorizationExpiresAt)
        leaseRemainingSeconds = try c.decodeIfPresent(Int64.self, forKey: .leaseRemainingSeconds) ?? 0
        accountAuthorized = try c.decodeIfPresent(Bool.self, forKey: .accountAuthorized) ?? false
        canConnect = try c.decodeIfPresent(Bool.self, forKey: .canConnect) ?? false
        reason = try c.decodeIfPresent(String.self, forKey: .reason) ?? ""
        reasonMessage = try c.decodeIfPresent(String.self, forKey: .reasonMessage) ?? ""
        totalBytes = try c.decodeIfPresent(Int64.self, forKey: .totalBytes) ?? 0
        remainingBytes = try c.decodeIfPresent(Int64.self, forKey: .remainingBytes) ?? 0
        usedUnits = try c.decodeIfPresent(Int64.self, forKey: .usedUnits) ?? 0
    }

    public var subscriptionExpires: Date? { Formatting.parseInstant(subscriptionExpiresAt) }
}

public struct MeterSample: Codable, Sendable, Equatable {
    public var upload: Int64 = 0
    public var download: Int64 = 0

    public init(upload: Int64 = 0, download: Int64 = 0) { self.upload = upload; self.download = download }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        upload = try c.decodeIfPresent(Int64.self, forKey: .upload) ?? 0
        download = try c.decodeIfPresent(Int64.self, forKey: .download) ?? 0
    }

    enum CodingKeys: String, CodingKey { case upload, download }
}

public struct MeteringView: Codable, Sendable, Equatable {
    public var source = ""
    public var uploadBytes: Int64 = 0
    public var downloadBytes: Int64 = 0
    public var lastSequence: Int64 = 0
    public var uploadBytesPerSecond: Int64 = 0
    public var downloadBytesPerSecond: Int64 = 0
    public var confirmedUploadBytes: Int64 = 0
    public var confirmedDownloadBytes: Int64 = 0
    public var confirmedRemainingBytes: Int64 = 0
    public var estimatedRemainingBytes: Int64 = 0
    public var ratePermille: Int64 = 0
    public var pending = false
    public var history: [MeterSample] = []

    enum CodingKeys: String, CodingKey {
        case source, pending, history
        case uploadBytes = "upload_bytes", downloadBytes = "download_bytes", lastSequence = "last_sequence"
        case uploadBytesPerSecond = "upload_bytes_per_second", downloadBytesPerSecond = "download_bytes_per_second"
        case confirmedUploadBytes = "confirmed_upload_bytes", confirmedDownloadBytes = "confirmed_download_bytes"
        case confirmedRemainingBytes = "confirmed_remaining_bytes", estimatedRemainingBytes = "estimated_remaining_bytes"
        case ratePermille = "rate_permille"
    }

    public init(source: String = "", uploadBytes: Int64 = 0, downloadBytes: Int64 = 0, uploadBytesPerSecond: Int64 = 0,
                downloadBytesPerSecond: Int64 = 0, confirmedRemainingBytes: Int64 = 0, estimatedRemainingBytes: Int64 = 0,
                ratePermille: Int64 = 0, pending: Bool = false, history: [MeterSample] = []) {
        self.source = source; self.uploadBytes = uploadBytes; self.downloadBytes = downloadBytes
        self.uploadBytesPerSecond = uploadBytesPerSecond; self.downloadBytesPerSecond = downloadBytesPerSecond
        self.confirmedRemainingBytes = confirmedRemainingBytes; self.estimatedRemainingBytes = estimatedRemainingBytes
        self.ratePermille = ratePermille; self.pending = pending; self.history = history
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func int(_ key: CodingKeys) throws -> Int64 { try c.decodeIfPresent(Int64.self, forKey: key) ?? 0 }
        source = try c.decodeIfPresent(String.self, forKey: .source) ?? ""
        uploadBytes = try int(.uploadBytes); downloadBytes = try int(.downloadBytes); lastSequence = try int(.lastSequence)
        uploadBytesPerSecond = try int(.uploadBytesPerSecond); downloadBytesPerSecond = try int(.downloadBytesPerSecond)
        confirmedUploadBytes = try int(.confirmedUploadBytes); confirmedDownloadBytes = try int(.confirmedDownloadBytes)
        confirmedRemainingBytes = try int(.confirmedRemainingBytes); estimatedRemainingBytes = try int(.estimatedRemainingBytes)
        ratePermille = try int(.ratePermille)
        pending = try c.decodeIfPresent(Bool.self, forKey: .pending) ?? false
        history = try c.decodeIfPresent([MeterSample].self, forKey: .history) ?? []
    }
}

public struct ErrorView: Codable, Sendable, Equatable {
    public var code = ""
    public var message = ""

    public init(code: String = "", message: String = "") { self.code = code; self.message = message }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        code = try c.decodeIfPresent(String.self, forKey: .code) ?? ""
        message = try c.decodeIfPresent(String.self, forKey: .message) ?? ""
    }

    enum CodingKeys: String, CodingKey { case code, message }
}

/// A closed authorization gate: the tunnel is up and holds every packet but forwards nothing
/// until the Core has re-confirmed online (after sleep, a network change, or a lapsed lease).
public struct GateView: Codable, Sendable, Equatable {
    public var reason = ""
    public var waitingNetwork = false
    public var closedSeconds: Int64 = 0
    public var attempts: Int64 = 0

    enum CodingKeys: String, CodingKey {
        case reason, attempts
        case waitingNetwork = "waiting_network", closedSeconds = "closed_seconds"
    }

    public init(reason: String = "", waitingNetwork: Bool = false, closedSeconds: Int64 = 0, attempts: Int64 = 0) {
        self.reason = reason; self.waitingNetwork = waitingNetwork; self.closedSeconds = closedSeconds; self.attempts = attempts
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        reason = try c.decodeIfPresent(String.self, forKey: .reason) ?? ""
        waitingNetwork = try c.decodeIfPresent(Bool.self, forKey: .waitingNetwork) ?? false
        closedSeconds = try c.decodeIfPresent(Int64.self, forKey: .closedSeconds) ?? 0
        attempts = try c.decodeIfPresent(Int64.self, forKey: .attempts) ?? 0
    }
}

public struct GateRecord: Codable, Sendable, Equatable {
    public var reason = ""
    public var outcome = ""
    public var closedMillis: Int64 = 0
    public var attempts: Int64 = 0
    public var totalClosures: Int64 = 0

    enum CodingKeys: String, CodingKey {
        case reason, outcome, attempts
        case closedMillis = "closed_ms", totalClosures = "total_closures"
    }
}

/// The complete UI state the Core returns. The app renders only this.
public struct CoreSnapshot: Codable, Sendable, Equatable {
    public enum State {
        public static let signedOut = "signed_out"
        public static let authenticating = "authenticating"
        public static let ready = "ready"
        public static let preparing = "preparing"
        public static let connecting = "connecting"
        public static let connected = "connected"
        public static let stopping = "stopping"
        public static let blocked = "blocked"
        public static let error = "error"
        /// This process handed its session to the other one (handoff-v1) and holds none.
        public static let released = "released"
    }

    public var generation: UInt64 = 0
    public var state = ""
    public var user: UserView?
    public var entitlement: EntitlementView?
    public var nodes: [NodeView] = []
    public var selectedNodeID = ""
    public var catalogRevision = ""
    public var profileVersion = ""
    public var configReady = false
    public var tunnelHealth = ""
    public var metering: MeteringView?
    public var lastError: ErrorView?
    public var coreVersion = ""
    /// Present only while the tunnel's authorization gate is closed (tunnel_health "gated").
    public var gate: GateView?
    public var lastGate: GateRecord?

    public static let empty = CoreSnapshot(state: State.signedOut)

    enum CodingKeys: String, CodingKey {
        case generation, state, user, entitlement, nodes, metering, gate
        case selectedNodeID = "selected_node_id", catalogRevision = "catalog_revision", profileVersion = "profile_version"
        case configReady = "config_ready", tunnelHealth = "tunnel_health", lastError = "last_error"
        case coreVersion = "core_version", lastGate = "last_gate"
    }

    public init(generation: UInt64 = 0, state: String = "", user: UserView? = nil, entitlement: EntitlementView? = nil,
                nodes: [NodeView] = [], selectedNodeID: String = "", catalogRevision: String = "", configReady: Bool = false,
                tunnelHealth: String = "", metering: MeteringView? = nil, lastError: ErrorView? = nil,
                gate: GateView? = nil) {
        self.generation = generation; self.state = state; self.user = user; self.entitlement = entitlement
        self.nodes = nodes; self.selectedNodeID = selectedNodeID; self.catalogRevision = catalogRevision
        self.configReady = configReady; self.tunnelHealth = tunnelHealth; self.metering = metering
        self.lastError = lastError; self.gate = gate
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        generation = try c.decodeIfPresent(UInt64.self, forKey: .generation) ?? 0
        state = try c.decodeIfPresent(String.self, forKey: .state) ?? ""
        user = try c.decodeIfPresent(UserView.self, forKey: .user)
        entitlement = try c.decodeIfPresent(EntitlementView.self, forKey: .entitlement)
        nodes = try c.decodeIfPresent([NodeView].self, forKey: .nodes) ?? []
        selectedNodeID = try c.decodeIfPresent(String.self, forKey: .selectedNodeID) ?? ""
        catalogRevision = try c.decodeIfPresent(String.self, forKey: .catalogRevision) ?? ""
        profileVersion = try c.decodeIfPresent(String.self, forKey: .profileVersion) ?? ""
        configReady = try c.decodeIfPresent(Bool.self, forKey: .configReady) ?? false
        tunnelHealth = try c.decodeIfPresent(String.self, forKey: .tunnelHealth) ?? ""
        metering = try c.decodeIfPresent(MeteringView.self, forKey: .metering)
        lastError = try c.decodeIfPresent(ErrorView.self, forKey: .lastError)
        coreVersion = try c.decodeIfPresent(String.self, forKey: .coreVersion) ?? ""
        gate = try c.decodeIfPresent(GateView.self, forKey: .gate)
        lastGate = try c.decodeIfPresent(GateRecord.self, forKey: .lastGate)
    }

    public var selectedNode: NodeView? {
        selectedNodeID.isEmpty ? nil : nodes.first { $0.id == selectedNodeID }
    }

    /// The data path is up or on its way; selection and sign-in changes wait for it.
    public var tunnelBusy: Bool {
        [State.preparing, State.connecting, State.connected, State.stopping].contains(state)
    }
}

/// What a state-changing command really produced. The Core answers a failed action with ok=true,
/// the new snapshot and an action_error, so "ok" alone never means the action succeeded.
public struct SnapshotReply: Sendable, Equatable {
    public let snapshot: CoreSnapshot
    public let actionError: ErrorView?

    public init(snapshot: CoreSnapshot, actionError: ErrorView? = nil) {
        self.snapshot = snapshot; self.actionError = actionError
    }

    public var succeeded: Bool { actionError == nil }

    static func parse(_ result: Data) throws -> SnapshotReply {
        struct Envelope: Decodable {
            let actionError: ErrorView?
            enum CodingKeys: String, CodingKey { case actionError = "action_error" }
        }
        let envelope = try JSONDecoder().decode(Envelope.self, from: result)
        let snapshot = try JSONDecoder().decode(CoreSnapshot.self, from: result)
        return SnapshotReply(snapshot: snapshot, actionError: envelope.actionError.flatMap { $0.code.isEmpty && $0.message.isEmpty ? nil : $0 })
    }
}
