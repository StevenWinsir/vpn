import Foundation

public enum AppPhase: Sendable, Equatable {
    /// Starting the in-process Core.
    case starting
    case signedOut
    case signingIn
    case signedIn
}

/// Which long command is in flight; only a connect can be cancelled.
public enum SessionActivity: Sendable, Equatable {
    case none, connecting, disconnecting
}

/// Something to tell the user. A Core refusal carries the Core's own Chinese text (`text`); a
/// problem found on this side carries only a `code` the UI turns into a string (ErrorText).
/// Logic branches on `code`, never on text.
public struct Notice: Sendable, Equatable {
    public let code: String
    public let text: String?

    public init(code: String, text: String? = nil) { self.code = code; self.text = text }

    public static func core(_ code: String, _ message: String) -> Notice { Notice(code: code, text: message.isEmpty ? nil : message) }
    public static func local(_ code: String) -> Notice { Notice(code: code) }
}

/// Local notice codes (ErrorText maps them to Chinese).
public enum LocalCodes {
    public static let inputMissing = "input_missing"
    public static let emailTooLong = "email_too_long"
    public static let passwordTooLong = "password_too_long"
    public static let coreStartFailed = "core_start_failed"
    public static let coreLost = "core_lost"
    public static let cancelled = "connect_cancelled"
    public static let sessionEnded = "session_ended"
    public static let stateChanged = "state_changed"
    public static let operationFailed = "operation_failed"
    public static let vpnPermissionDenied = "vpn_permission_denied"
    public static let vpnStartFailed = "vpn_start_failed"
    public static let vpnStopping = "vpn_stopping"
    public static let connectTimeout = "connect_timeout"
    public static let credentialReadFailed = "credential_read_failed"
    public static let credentialSaveFailed = "credential_save_failed"
    public static let credentialDeleteFailed = "credential_delete_failed"
    public static let noPlan = "no_plan"
    public static let noLines = "no_lines"
    public static let selectLine = "select_line"
    public static let noMatch = "no_match"
    public static let syncing = "syncing_lines"
    public static let tunnelEnded = "tunnel_ended"
    public static let vpnRevoked = "vpn_revoked"
    public static let networkUnreachable = "network_unreachable"
    public static let tunnelUnreachable = TunnelBridge.unreachableCode
    public static let routeProbeFailed = "route_probe_failed"
}

/// Everything the screens render. Derived from the Core's snapshot; nothing here decides authorization.
public struct SessionState: Sendable, Equatable {
    public var phase: AppPhase = .starting
    public var snapshot: CoreSnapshot = .empty
    public var message: Notice?
    public var warning: Notice?
    public var busy = false
    public var activity: SessionActivity = .none
    /// The Core cannot be used at all (distinct from a business refusal).
    public var coreProblem: Notice?
    /// Prefills the login form: the remembered email, or the one last typed.
    public var email = ""
    public var rememberPassword = false
    /// The first connect needs the system to add the VPN configuration: explain it before asking.
    public var needsVPNSetup = false
    public var identity: CoreHello?
    /// Monotonic milliseconds when the Core first reported this tunnel connected (display only).
    public var connectedSinceMillis: Int64?
    /// The Core's metering counters when this tunnel came up: "this connection" starts here.
    public var connectionBaseline: MeterSample?

    public init() {}

    public var connection: ConnectionView { ConnectionView.from(snapshot, busy: busy, activity: activity) }

    /// Usage of the current connection only (nil unless connected).
    public var usage: ConnectionUsage? { ConnectionUsage.of(snapshot, baseline: connectionBaseline) }
}

/// Bytes carried by the current connection and what they cost. The Core's client-reported
/// counters are cumulative for the whole sign-in (they span connections, possibly at different
/// rates), so the connection's share is the difference to the baseline; a connection binds
/// exactly one rate (a changed rate ends it), so billed = physical × rate. Integers only.
public struct ConnectionUsage: Sendable, Equatable {
    public let uploadBytes: Int64
    public let downloadBytes: Int64
    public let ratePermille: Int64

    public var physicalBytes: Int64 { uploadBytes + downloadBytes }
    /// What the server deducts: bytes x permille, in units of 1/1000 byte (handoff_app_ios.md 9).
    public var chargedUnits: Int64 { Formatting.chargedUnits(physical: physicalBytes, ratePermille: ratePermille) }
    public var billedBytes: Int64 { Formatting.billedBytes(physical: physicalBytes, ratePermille: ratePermille) }

    public static func of(_ snapshot: CoreSnapshot, baseline: MeterSample?) -> ConnectionUsage? {
        guard let metering = snapshot.metering, snapshot.state == CoreSnapshot.State.connected else { return nil }
        let base = baseline ?? MeterSample()
        return ConnectionUsage(uploadBytes: max(0, metering.uploadBytes - base.upload),
                               downloadBytes: max(0, metering.downloadBytes - base.download),
                               ratePermille: metering.ratePermille)
    }

    /// Where a new connection's count starts. Client-reported counters continue from the last
    /// snapshot before the connect; a node-reported connection has its own local meter that
    /// starts at zero.
    public static func baseline(before: CoreSnapshot, connected: CoreSnapshot) -> MeterSample {
        let now = connected.metering
        if now?.source == "node_reported" { return MeterSample() }
        let prior = before.metering.flatMap { $0.source == now?.source ? $0 : nil } ?? now
        guard let prior else { return MeterSample() }
        return MeterSample(upload: prior.uploadBytes, download: prior.downloadBytes)
    }
}

public enum LoginInput {
    public static let maxEmailLength = 254
    public static let maxPasswordBytes = 72

    /// The Core's limits (native/core/internal/service/service.go); nil when acceptable.
    public static func validate(email: String, password: String) -> String? {
        let trimmed = email.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty || password.isEmpty { return LocalCodes.inputMissing }
        if trimmed.utf8.count > maxEmailLength { return LocalCodes.emailTooLong }
        if password.utf8.count > maxPasswordBytes { return LocalCodes.passwordTooLong }
        return nil
    }
}
