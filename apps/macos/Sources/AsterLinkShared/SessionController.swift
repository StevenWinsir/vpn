import Foundation
import Observation

public enum AppPhase: Equatable, Sendable {
    /// Waiting for the Core/Helper to be reachable.
    case starting
    case signedOut
    case signingIn
    case signedIn
}

/// Drives the three screens. It owns no authorization logic: every verdict comes from
/// the Core's snapshot, and the UI only renders it. Passwords live in a local variable for
/// the duration of one login call and are never stored on this object.
@MainActor
@Observable
public final class SessionController {
    public private(set) var phase: AppPhase = .starting
    public private(set) var snapshot: CoreSnapshot = .signedOut
    public private(set) var message: String?
    public private(set) var warning: String?
    public private(set) var busy = false
    public var email = ""
    public var rememberPassword = true
    /// Set when the Core/Helper cannot be reached at all (distinct from a business refusal).
    public private(set) var serviceProblem: String?

    private let client: CoreClient
    private let environment: ClientEnvironment
    private let credentials: CredentialStore
    private var preferences: LoginPreferences
    private let devices: DeviceIdentityProvider
    private var pollTask: Task<Void, Never>?
    private var autoRelogins = 0
    /// Bumped by every user action that changes the session; a result or a scheduled silent
    /// re-login that started under an older version lost the race and is discarded.
    private var operationVersion = 0
    private let pollInterval: Duration

    public init(client: CoreClient, environment: ClientEnvironment, credentials: CredentialStore,
                preferences: LoginPreferences, devices: DeviceIdentityProvider, pollInterval: Duration = .seconds(2)) {
        self.client = client
        self.environment = environment
        self.credentials = credentials
        self.preferences = preferences
        self.devices = devices
        self.pollInterval = pollInterval
        email = preferences.rememberedEmail ?? ""
    }

    public var websiteBase: URL { environment.websiteBase }
    public var plansURL: URL { environment.websiteBase.appendingPathComponent("plans") }
    public var registerURL: URL { environment.websiteBase.appendingPathComponent("register") }
    public var forgotPasswordURL: URL { environment.websiteBase.appendingPathComponent("forgot-password") }

    private func account(for email: String) -> String { CredentialAccount.key(apiBase: environment.apiBase, email: email) }

    // MARK: launch

    /// Called once at start. A remembered password only saves typing: the Core still
    /// authenticates online, so an expired plan or a changed password is detected now.
    public func launch() async {
        guard phase == .starting else { return }
        do {
            try await client.start()
            serviceProblem = nil
        } catch {
            serviceProblem = Self.describe(error)
            phase = .signedOut
            return
        }
        phase = .signedOut
        let saved = email.isEmpty ? nil : (try? credentials.password(for: account(for: email))) ?? nil
        if let saved, !saved.isEmpty {
            await signIn(email: email, password: saved, remember: true, automatic: true)
        }
    }

    /// The Helper was just approved (first run) after `launch` found it unreachable:
    /// retry the start-up once, including the remembered-password sign-in, so the user
    /// does not have to press anything else. A deliberate sign-in in progress wins.
    public func serviceBecameAvailable() async {
        guard phase == .signedOut, serviceProblem != nil, !busy else { return }
        serviceProblem = nil
        message = nil
        phase = .starting
        await launch()
    }

    // MARK: sign in / out

    public func signIn(email rawEmail: String, password: String, remember: Bool, automatic: Bool = false) async {
        let email = rawEmail.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !email.isEmpty, !password.isEmpty else {
            message = "请输入邮箱和密码"
            return
        }
        guard !busy else { return }
        operationVersion += 1
        busy = true
        message = nil
        warning = nil
        phase = .signingIn
        defer { busy = false }
        self.email = email
        do {
            try await client.start()
            serviceProblem = nil
            let reply = try await client.login(email: email, password: password, deviceID: try devices.deviceID())
            snapshot = reply.snapshot
            if let failure = reply.actionError {
                phase = .signedOut
                message = failure.message
                if failure.code == "invalid_credentials" { forgetRejected(email: email, password: password) }
                return
            }
            phase = .signedIn
            persist(email: email, password: password, remember: remember)
            // Only a deliberate sign-in re-arms the single silent re-login, so a server that
            // keeps expiring sessions cannot make the app hammer the login endpoint.
            if !automatic { autoRelogins = 0 }
            startPolling()
        } catch {
            phase = .signedOut
            let text = Self.describe(error)
            serviceProblem = text
            message = text
        }
    }

    /// Keychain failure never downgrades to a file: the user is told it was not remembered.
    private func persist(email: String, password: String, remember: Bool) {
        let key = account(for: email)
        if remember {
            do {
                try credentials.save(account: key, password: password)
                preferences.rememberedEmail = email
            } catch {
                warning = "无法保存到系统钥匙串，本次未记住密码"
                try? credentials.delete(account: key)
            }
        } else {
            try? credentials.delete(account: key)
            if preferences.rememberedEmail?.lowercased() == email.lowercased() { preferences.rememberedEmail = nil }
        }
    }

    /// A saved password the server rejects is stale (changed on the website, for example): stop
    /// offering it. Only the item that still holds exactly the rejected password goes, so a typo
    /// in a manual login never drops a good saved password, and a password saved meanwhile by a
    /// newer login survives. The device identity is stored apart and is never touched.
    private func forgetRejected(email: String, password: String) {
        let key = account(for: email)
        guard let saved = (try? credentials.password(for: key)) ?? nil, saved == password else { return }
        try? credentials.delete(account: key)
    }

    /// Explicit sign-out also forgets the saved password for this account.
    public func signOut() async {
        stopPolling()
        operationVersion += 1
        let key = account(for: snapshot.user?.email ?? email)
        phase = .signedOut
        do { snapshot = try await client.logout() } catch { snapshot = .signedOut }
        try? credentials.delete(account: key)
        preferences.rememberedEmail = nil
        message = nil
    }

    /// Normal app exit: revoke and stop, but keep a password the user chose to remember.
    public func shutdown() async {
        stopPolling()
        await client.stop()
    }

    // MARK: actions

    public func refresh() async {
        guard phase == .signedIn else { return }
        await run { try await self.client.refreshCatalog(generation: self.snapshot.generation) }
    }

    public func select(_ nodeID: String) async {
        guard phase == .signedIn, !busy else { return }
        await run { try await self.client.selectNode(generation: self.snapshot.generation, nodeID: nodeID, catalogRevision: self.snapshot.catalogRevision) }
    }

    public func connect() async {
        guard phase == .signedIn, !busy else { return }
        await run { try await self.client.connect(generation: self.snapshot.generation) }
    }

    public func disconnect() async {
        guard phase == .signedIn, !busy else { return }
        busy = true
        defer { busy = false }
        // Closing the data path also acknowledges the last traffic batch, which can take a
        // few seconds on a slow network; the Core reports any problem in `lastError`.
        do {
            let updated = try await client.disconnect()
            apply(updated)
            message = updated.lastError?.message
        } catch let error as CoreCommandError where error.code == HelperErrorCode.workerFailed.rawValue {
            workerLost()
        } catch {}
    }

    private func run(_ action: @escaping () async throws -> CoreActionReply) async {
        operationVersion += 1
        let version = operationVersion
        busy = true
        defer { busy = false }
        do {
            let reply = try await action()
            guard version == operationVersion, phase == .signedIn else { return }
            apply(reply.snapshot)
            message = reply.actionError?.message
        } catch let error as CoreCommandError where error.code == HelperErrorCode.workerFailed.rawValue {
            workerLost()
        } catch let error as CoreCommandError where error.code == "stale_generation" {
            if let latest = try? await client.readSnapshot() { apply(latest) }
            message = error.message
        } catch {
            message = Self.describe(error)
        }
    }

    // MARK: polling

    private func startPolling() {
        stopPolling()
        pollTask = Task { [weak self, pollInterval] in
            while !Task.isCancelled {
                // Live rates are only worth a faster read while a tunnel is carrying traffic.
                let live = await self?.snapshot.state == .connected
                try? await Task.sleep(for: live ? .seconds(1) : pollInterval)
                guard let self, !Task.isCancelled else { return }
                await self.poll()
            }
        }
    }

    private func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }

    private func poll() async {
        guard phase == .signedIn, !busy else { return }
        let version = operationVersion
        do {
            let latest = try await client.readSnapshot()
            // A sign-out, a new sign-in or an action raced this read: its snapshot is old news.
            guard version == operationVersion, phase == .signedIn else { return }
            serviceProblem = nil
            apply(latest)
        } catch let error as CoreCommandError where error.code == HelperErrorCode.workerFailed.rawValue {
            guard version == operationVersion else { return }
            workerLost()
        } catch {
            guard version == operationVersion else { return }
            serviceProblem = Self.describe(error)
        }
    }

    /// The Helper reported that the Core exited or its pipe broke. The session, token and
    /// tunnel lived only in that process, so nothing is resumed: the user signs in online
    /// again, which starts a fresh Core (handoff_app.md section 6.3).
    private func workerLost() {
        stopPolling()
        phase = .signedOut
        snapshot = .signedOut
        message = "连接已中断，请重新登录"
    }

    private func apply(_ latest: CoreSnapshot) {
        let previous = snapshot
        snapshot = latest
        guard phase == .signedIn, latest.state == .signedOut, previous.state != .signedOut else { return }
        // The Core ended the session (expired, revoked, plan changed).
        stopPolling()
        phase = .signedOut
        message = latest.lastError?.message ?? "登录已失效，请重新登录"
        let code = latest.lastError?.code ?? ""
        let email = self.email
        guard code.hasPrefix("native_session_"), autoRelogins < 1,
              let saved = (try? credentials.password(for: account(for: email))) ?? nil, !saved.isEmpty else { return }
        // One silent re-login for an expired session; a refused password is never retried. The
        // account and password are fixed now: if the user signs in, switches account or signs out
        // before it runs, the re-login is dropped instead of acting on the newer state.
        autoRelogins += 1
        let version = operationVersion
        Task {
            guard version == self.operationVersion, self.phase == .signedOut, !self.busy else { return }
            await self.signIn(email: email, password: saved, remember: true, automatic: true)
        }
    }

    static func describe(_ error: Error) -> String {
        if let error = error as? CoreCommandError { return error.message }
        if let error = error as? HelperUnavailable { return error.message }
        return "无法连接后台服务，请在“账户”页检查后台服务状态"
    }
}

/// The Helper/Core could not be reached (not enabled, not running, version mismatch).
public struct HelperUnavailable: Error, Equatable {
    public var message: String
    public init(_ message: String) { self.message = message }
}
