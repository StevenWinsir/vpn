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

/// The system VPN: the packet-tunnel handoff (the app's TunnelControl implementation) or, in tests
/// and the screenshot fixtures, a stand-in. The controller routes every connect through this seam.
public protocol TunnelControl: Sendable {
    func connect(generation: UInt64) async -> TunnelOutcome
    /// Ends the system tunnel, if one is up, and takes the session back into the app's own Core.
    /// Always safe and cheap when nothing runs (the controller calls it after every idle
    /// snapshot). True when a tunnel or a handed-out session was ended, so the Core the
    /// controller talks to has changed.
    @discardableResult func stop() async -> Bool
    /// Whether the system VPN configuration already exists. When it does not, the first connect
    /// explains the system dialog before asking for it.
    var configured: Bool { get async }
    /// The app came to the front or went away (the extension only polls the catalog while the app is seen).
    func foreground(_ visible: Bool) async
    /// Why the system VPN last stopped on its own, when the extension said (a quota or plan
    /// verdict, a line change), so the app can show the real reason instead of a generic one.
    func lastEndReason() async -> Notice?
    /// The app was killed or relaunched while the extension kept the tunnel and the session
    /// (D-L05): attach to that tunnel, so requests go to its Core, and say whether it really is
    /// connected. False leaves the tunnel closed and the app on its own Core (a normal launch).
    func resume() async -> Bool
    /// Whether a background stop can reach the user (notification permission). Asks the first time.
    func notificationsAllowed() async -> Bool
}

public extension TunnelControl {
    var configured: Bool { get async { true } }
    func foreground(_ visible: Bool) async {}
    func lastEndReason() async -> Notice? { nil }
    func resume() async -> Bool { false }
    func notificationsAllowed() async -> Bool { true }
}

/// Tells the user that the VPN ended without them asking (quota used up, plan ended, line
/// disabled, lease lost), even when no screen is open.
public protocol SessionAlerts: Sendable {
    func tunnelStopped(_ reason: Notice)
    /// A new connection (or sign-out) makes an earlier alert stale.
    func clear()
}

/// A connect straight to the app's own Core. On iOS the app process owns no tunnel, so a real Core
/// answers `tun_unavailable`; used by the Kit tests (a scripted Core) and fixtures only.
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

    public func stop() async -> Bool { false }
}

public struct NoAlerts: SessionAlerts {
    public init() {}
    public func tunnelStopped(_ reason: Notice) {}
    public func clear() {}
}
