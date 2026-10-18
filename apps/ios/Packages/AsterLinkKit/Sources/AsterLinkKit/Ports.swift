import CryptoKit
import Foundation

// The seams the SessionController is built on, so the same controller drives the real Core and
// Keychain in the app and scripted fakes in tests (the Swift mirror of Android's Ports.kt).

/// The in-process Core: `start` is idempotent; `stop` ends the session inside it (bounded settle and revoke).
public protocol CoreHost: Sendable {
    func start() async throws
    func stop() async
}

public struct CredentialStoreError: Error, Equatable, Sendable {
    public let status: Int32
    public init(status: Int32 = 0) { self.status = status }
}

/// The remembered password, kept in the Keychain (this device only). There is intentionally no
/// plaintext fallback: a failure surfaces as `CredentialStoreError`.
public protocol CredentialStore: Sendable {
    func save(account: String, password: String) async throws
    func read(account: String) async throws -> String?
    func delete(account: String) async throws
}

/// Items are scoped to the exact API base plus the lower-cased email (handoff_app_ios.md 6.3);
/// the account name is the SHA-256 of both so no address ends up in the Keychain's account field.
public enum CredentialAccount {
    public static func key(apiBase: String, email: String) -> String {
        var base = apiBase
        while base.hasSuffix("/") { base.removeLast() }
        let normalized = base + "|" + email.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return SHA256.hash(data: Data(normalized.utf8)).map { String(format: "%02x", $0) }.joined()
    }
}

/// Non-secret remembered choices (never the password).
public protocol LoginPreferences: Sendable {
    func rememberedEmail() -> String?
    func setRememberedEmail(_ email: String?)
}

/// A fixed per-installation random UUID (not IDFV, IDFA or a hardware identifier).
public protocol DeviceIDProvider: Sendable {
    func deviceID() async throws -> String
}

/// How a connect ended.
public enum TunnelOutcome: Sendable, Equatable {
    /// The Core answered the connect command.
    case replied(SnapshotReply)
    /// The Core refused it (`code` is a Core code with its `message`) or the tunnel could not run it (a local code).
    case refused(code: String, message: String = "")
}

/// The system VPN. In round 3 the connect goes straight to the app's own Core, which on iOS has
/// no tunnel and answers with an honest refusal (the packet-tunnel handoff arrives with the
/// round-4 connect flow); the controller already routes every connect through this seam.
public protocol TunnelControl: Sendable {
    func connect(generation: UInt64) async -> TunnelOutcome
    /// Stops the system tunnel, if one is up. Always safe.
    func stop() async
}

/// Tells the user that the VPN ended without them asking (quota used up, plan ended, line
/// disabled, lease lost), even when no screen is open.
public protocol SessionAlerts: Sendable {
    func tunnelStopped(_ reason: Notice)
    /// A new connection (or sign-out) makes an earlier alert stale.
    func clear()
}

/// Round 3's connect: straight to the app's own Core. On iOS the app process owns no tunnel, so
/// the Core answers `tun_unavailable` and the UI says so honestly; round 4 replaces this with the
/// packet-tunnel handoff (TunnelController) behind the same seam.
public struct DirectCoreTunnel: TunnelControl {
    private let client: CoreClient

    public init(client: CoreClient) { self.client = client }

    public func connect(generation: UInt64) async -> TunnelOutcome {
        do {
            return .replied(try await client.connect(generation: generation))
        } catch let error as CoreException {
            return .refused(code: error.code, message: error.message)
        } catch {
            return .refused(code: LocalCodes.operationFailed)
        }
    }

    public func stop() async {}
}

public struct NoAlerts: SessionAlerts {
    public init() {}
    public func tunnelStopped(_ reason: Notice) {}
    public func clear() {}
}
