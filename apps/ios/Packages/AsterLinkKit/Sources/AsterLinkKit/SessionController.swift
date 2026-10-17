import Foundation
import Observation

/// Drives the login and the three tabs (a port of the Android SessionController, itself a port
/// of the Windows one). It owns no authorization logic: every verdict comes from the Core's
/// snapshot and the UI only renders it. A password lives in a local variable for one login call
/// and is never kept here.
///
/// Everything runs on the main actor, so the fields below need no locks; Core calls leave the
/// actor inside `CoreClient`. Semantics kept from the other clients: results that raced a user
/// action are discarded (operation version), snapshots of an older generation are late news,
/// `action_error` is a failed action with fresh state, `stale_generation` re-reads the state,
/// one silent re-login after a `native_session_*` expiry (never after a refused password), and
/// a lost Core ends the session (the user signs in online again). A foreground refresh of the
/// catalog is throttled; a tunnel that ends without the user asking raises an alert.
@MainActor
@Observable
public final class SessionController {
    public private(set) var state = SessionState()

    @ObservationIgnored private let client: CoreClient
    @ObservationIgnored private let core: any CoreHost
    @ObservationIgnored private let target: LoginTarget
    @ObservationIgnored private let credentials: any CredentialStore
    @ObservationIgnored private let preferences: any LoginPreferences
    @ObservationIgnored private let devices: any DeviceIDProvider
    @ObservationIgnored private let tunnel: any TunnelControl
    @ObservationIgnored private let alerts: any SessionAlerts
    /// Monotonic milliseconds.
    @ObservationIgnored private let clock: @Sendable () -> Int64
    @ObservationIgnored private let idleInterval: Duration
    @ObservationIgnored private let liveInterval: Duration
    @ObservationIgnored private let refreshThrottleMillis: Int64

    @ObservationIgnored private var polling: Task<Void, Never>?
    @ObservationIgnored private var autoRelogins = 0
    @ObservationIgnored private var operationVersion = 0
    @ObservationIgnored private var cancelRequested = false
    @ObservationIgnored private var lastRefreshMillis: Int64?
    /// The Core reported this tunnel connected and nobody here has asked it to stop since.
    @ObservationIgnored private var tunnelUp = false

    public init(client: CoreClient, core: any CoreHost, target: LoginTarget, credentials: any CredentialStore,
                preferences: any LoginPreferences, devices: any DeviceIDProvider, tunnel: any TunnelControl,
                alerts: any SessionAlerts = NoAlerts(), clock: @escaping @Sendable () -> Int64 = SessionController.monotonicMillis,
                idleInterval: Duration = .seconds(2), liveInterval: Duration = .seconds(1), refreshThrottleMillis: Int64 = 10_000) {
        self.client = client; self.core = core; self.target = target; self.credentials = credentials
        self.preferences = preferences; self.devices = devices; self.tunnel = tunnel; self.alerts = alerts
        self.clock = clock; self.idleInterval = idleInterval; self.liveInterval = liveInterval
        self.refreshThrottleMillis = refreshThrottleMillis
    }

    public nonisolated static let monotonicMillis: @Sendable () -> Int64 = { Int64(DispatchTime.now().uptimeNanoseconds / 1_000_000) }

    private func accountFor(_ email: String) -> String { CredentialAccount.key(apiBase: target.apiBase, email: email) }

    // MARK: launch

    /// Called once when the UI first appears. A remembered password only saves typing: the Core
    /// still authenticates online, so an expired plan or a changed password is detected now.
    public func launch() async {
        guard state.phase == .starting else { return }
        state.email = preferences.rememberedEmail() ?? ""
        let started = await startCore()
        state.phase = .signedOut
        guard started, !state.email.isEmpty else { return }
        let email = state.email
        let saved: String?
        do { saved = try await credentials.read(account: accountFor(email)) } catch {
            state.warning = .local(LocalCodes.credentialReadFailed)
            return
        }
        if let saved, !saved.isEmpty {
            state.rememberPassword = true
            await signIn(email: email, password: saved, remember: true, automatic: true)
        }
    }

    /// Starts the Core (idempotent) and reads its identity once. False when it cannot run.
    private func startCore() async -> Bool {
        do {
            try await core.start()
            if state.identity == nil { state.identity = try await client.hello() }
            state.coreProblem = nil
            return true
        } catch {
            state.coreProblem = .local(LocalCodes.coreStartFailed)
            return false
        }
    }

    // MARK: sign in / out

    public func signIn(email rawEmail: String, password: String, remember: Bool, automatic: Bool = false) async {
        let email = rawEmail.trimmingCharacters(in: .whitespacesAndNewlines)
        if let problem = LoginInput.validate(email: email, password: password) {
            state.message = .local(problem)
            return
        }
        if state.busy { return }
        operationVersion += 1
        state.busy = true; state.message = nil; state.warning = nil; state.phase = .signingIn; state.email = email
        var signedIn = false
        do {
            if await startCore() {
                let reply = try await client.login(email: email, password: password, deviceID: try await devices.deviceID(), target: target)
                state.snapshot = reply.snapshot
                if let failure = reply.actionError {
                    state.phase = .signedOut
                    state.message = .core(failure.code, failure.message)
                    // A saved password the server rejects is stale: stop offering it.
                    if failure.code == "invalid_credentials" { await tryDelete(accountFor(email)) }
                } else {
                    state.phase = .signedIn; state.connectedSinceMillis = nil
                    await persist(email: email, password: password, remember: remember)
                    // Only a deliberate sign-in re-arms the single silent re-login, so a server
                    // that keeps expiring sessions cannot make the app hammer the login endpoint.
                    if !automatic { autoRelogins = 0 }
                    startPolling()
                    signedIn = true
                }
            } else {
                state.phase = .signedOut; state.message = .local(LocalCodes.coreStartFailed)
            }
        } catch let error as CoreException {
            state.phase = .signedOut; state.message = describe(error)
        } catch {
            // The device identity could not be stored, or the Core binding failed.
            state.phase = .signedOut; state.message = .local(LocalCodes.operationFailed)
        }
        state.busy = false
        // The Core's watcher delivers the first catalog on its own schedule (about 2 s); ask for
        // it at once so the lines appear immediately.
        if signedIn { await quietRefresh() }
    }

    /// A Keychain failure never downgrades to plaintext: the user is told it was not remembered.
    private func persist(email: String, password: String, remember: Bool) async {
        let key = accountFor(email)
        if remember {
            do {
                try await credentials.save(account: key, password: password)
                preferences.setRememberedEmail(email)
                state.rememberPassword = true
            } catch {
                state.warning = .local(LocalCodes.credentialSaveFailed); state.rememberPassword = false
                await tryDelete(key)
            }
        } else {
            await tryDelete(key)
            state.rememberPassword = false
            if let remembered = preferences.rememberedEmail(), remembered.caseInsensitiveCompare(email) == .orderedSame {
                preferences.setRememberedEmail(nil)
            }
        }
    }

    private func tryDelete(_ key: String) async {
        do { try await credentials.delete(account: key) } catch { state.warning = .local(LocalCodes.credentialDeleteFailed) }
    }

    /// Explicit sign-out: stop and settle, then forget the saved password for this account.
    public func signOut() async {
        guard state.phase == .signedIn, !state.busy else { return }
        stopPolling()
        operationVersion += 1
        tunnelUp = false
        alerts.clear()
        let known = state.snapshot.user?.email ?? ""
        let key = accountFor(known.isEmpty ? state.email : known)
        state.phase = .signedOut; state.busy = true
        let after = (try? await client.logout()) ?? .empty
        await tunnel.stop()
        await tryDelete(key)
        preferences.setRememberedEmail(nil)
        state.snapshot = after; state.busy = false; state.rememberPassword = false; state.message = nil
        state.connectedSinceMillis = nil; state.connectionBaseline = nil
    }

    // MARK: actions

    public func refresh() async {
        guard state.phase == .signedIn, !state.busy else { return }
        lastRefreshMillis = clock()
        let generation = state.snapshot.generation
        await run { try await self.client.refreshCatalog(generation: generation) }
    }

    /// The app came to the foreground: re-read the catalog at most every `refreshThrottleMillis`.
    /// Quiet (no busy state, so buttons do not flicker) and discarded if a user action raced it.
    public func onForeground() async {
        guard state.phase == .signedIn, !state.busy else { return }
        if let last = lastRefreshMillis, clock() - last < refreshThrottleMillis { return }
        await quietRefresh()
    }

    /// One refresh_catalog that never shows as busy and loses to any user action that races it.
    private func quietRefresh() async {
        lastRefreshMillis = clock()
        let version = operationVersion
        do {
            let reply = try await client.refreshCatalog(generation: state.snapshot.generation)
            if version != operationVersion || state.phase != .signedIn { return }
            apply(reply.snapshot)
        } catch let error as CoreException {
            if version != operationVersion { return }
            if error.code == CoreCodes.notStarted { coreLost() }
            // Anything else (stale_generation, a network refusal) is left to the next poll.
        } catch {}
    }

    public func select(_ nodeID: String) async {
        let snapshot = state.snapshot
        guard state.phase == .signedIn, !state.busy, !snapshot.tunnelBusy else { return }
        if nodeID == snapshot.selectedNodeID && snapshot.configReady { return }
        await run { try await self.client.selectNode(generation: snapshot.generation, nodeID: nodeID, catalogRevision: snapshot.catalogRevision) }
    }

    /// The user confirmed switching lines while connected: stop and settle the old segment at its
    /// own rate, then select the new line and stay disconnected until Connect (no seamless switch).
    public func switchTo(_ nodeID: String) async {
        guard state.phase == .signedIn, !state.busy else { return }
        if state.snapshot.state == CoreSnapshot.State.connected { await disconnect() }
        if state.snapshot.tunnelBusy { return }
        await select(nodeID)
    }

    public func connect() async {
        guard state.phase == .signedIn, !state.busy, state.connection.canConnect else { return }
        cancelRequested = false
        operationVersion += 1
        alerts.clear()
        state.busy = true; state.activity = .connecting; state.message = nil
        switch await tunnel.connect(generation: state.snapshot.generation) {
        case .replied(let reply):
            if cancelRequested {
                cancelledByUser()
            } else {
                apply(reply.snapshot)
                state.message = reply.actionError.map { .core($0.code, $0.message) }
            }
        case .refused(let code, let message):
            if cancelRequested { cancelledByUser() }
            else if code == CoreCodes.staleGeneration { await staleGeneration(message) }
            else if code == CoreCodes.notStarted { coreLost() }
            else if !message.isEmpty && !Self.ipcCodes.contains(code) { state.message = .core(code, message) }
            else { state.message = .local(code) }
        }
        state.busy = false; state.activity = .none
    }

    /// The user declined the system dialog for adding the VPN configuration: stay ready and say why.
    public func vpnConsentDenied() { state.message = .local(LocalCodes.vpnPermissionDenied) }

    /// The system took the VPN away (another VPN app, or the user in Settings). The extension
    /// already closed the data path and settled; this only says why.
    public func vpnRevoked() {
        guard state.phase == .signedIn else { return }
        let wasUp = tunnelUp || state.snapshot.tunnelBusy
        tunnelUp = false
        guard wasUp else { return }
        let notice = Notice.local(LocalCodes.vpnRevoked)
        alerts.tunnelStopped(notice)
        state.message = notice
    }

    public func disconnect() async {
        guard state.phase == .signedIn, !state.busy else { return }
        operationVersion += 1
        // Asked for: the end of this tunnel is no news to alert about.
        tunnelUp = false
        state.busy = true; state.activity = .disconnecting
        do {
            // Closing the data path also settles the last traffic batch (a few seconds on a slow network).
            let updated = try await client.disconnect()
            apply(updated)
            state.message = updated.lastError.flatMap { $0.message.isEmpty ? nil : Notice.core($0.code, $0.message) }
        } catch let error as CoreException {
            if error.code == CoreCodes.notStarted { coreLost() } else { state.message = describe(error) }
        } catch {}
        state.busy = false; state.activity = .none
        await tunnel.stop()
    }

    /// Abandons a connect that takes too long. The command is already inside the Core, so the
    /// only reliable way back to a known state is to end the whole session: the Core is stopped
    /// (it settles and revokes), and the user signs in again.
    public func cancelConnect() async {
        guard state.busy, state.phase == .signedIn, state.activity == .connecting else { return }
        cancelRequested = true
        tunnelUp = false
        stopPolling()
        await core.stop()
        await tunnel.stop()
    }

    public func dismissMessage() { state.message = nil; state.warning = nil }

    private func run(_ action: () async throws -> SnapshotReply) async {
        cancelRequested = false
        operationVersion += 1
        state.busy = true
        do {
            let reply = try await action()
            apply(reply.snapshot)
            state.message = reply.actionError.map { .core($0.code, $0.message) }
        } catch let error as CoreException {
            if error.code == CoreCodes.staleGeneration { await staleGeneration(error.message) }
            else if error.code == CoreCodes.notStarted { coreLost() }
            else { state.message = describe(error) }
        } catch {}
        state.busy = false
    }

    /// The state changed under the click: show the new state and let the user decide again.
    private func staleGeneration(_ message: String) async {
        if let latest = try? await client.readSnapshot() { apply(latest) }
        state.message = message.isEmpty ? .local(LocalCodes.stateChanged) : .core(CoreCodes.staleGeneration, message)
    }

    // MARK: polling

    private func startPolling() {
        stopPolling()
        polling = Task { [weak self] in
            while !Task.isCancelled {
                // Live rates, and the moment a tunnel stops, are worth a faster read while a tunnel exists.
                guard let interval = self.map({ $0.state.snapshot.tunnelBusy ? $0.liveInterval : $0.idleInterval }) else { return }
                try? await Task.sleep(for: interval)
                if Task.isCancelled { return }
                await self?.poll()
            }
        }
    }

    private func stopPolling() {
        polling?.cancel()
        polling = nil
    }

    /// One snapshot read. A result that raced a user action is discarded, never applied over newer state.
    public func poll() async {
        guard state.phase == .signedIn, !state.busy else { return }
        let version = operationVersion
        do {
            let latest = try await client.readSnapshot()
            if version != operationVersion || state.phase != .signedIn { return }
            state.coreProblem = nil
            apply(latest)
        } catch let error as CoreException {
            if version != operationVersion { return }
            if error.code == CoreCodes.notStarted { coreLost() } else { state.coreProblem = describe(error) }
        } catch {}
    }

    /// The Core stopped (the user cancelled a connect, or it failed). The session, token and
    /// tunnel lived only inside it, so nothing is resumed: the user signs in online again.
    private func coreLost() {
        stopPolling()
        if tunnelUp { alerts.tunnelStopped(.local(LocalCodes.coreLost)) }
        tunnelUp = false
        state.phase = .signedOut; state.snapshot = .empty; state.message = .local(LocalCodes.coreLost)
        state.connectedSinceMillis = nil; state.connectionBaseline = nil
        Task { await tunnel.stop() }
    }

    private func cancelledByUser() {
        stopPolling()
        tunnelUp = false
        state.phase = .signedOut; state.snapshot = .empty; state.message = .local(LocalCodes.cancelled)
        state.connectedSinceMillis = nil; state.connectionBaseline = nil
        Task { await tunnel.stop() }
    }

    private func apply(_ latest: CoreSnapshot) {
        let previous = state.snapshot
        // A snapshot from an older generation of the same session is late news, not state.
        if state.phase == .signedIn && latest.state != CoreSnapshot.State.signedOut && latest.generation < previous.generation { return }
        let continuing = latest.state == CoreSnapshot.State.connected && previous.state == CoreSnapshot.State.connected
        let since: Int64? = latest.state != CoreSnapshot.State.connected ? nil
            : (continuing ? state.connectedSinceMillis : nil) ?? clock()
        let baseline: MeterSample? = latest.state != CoreSnapshot.State.connected ? nil
            : (continuing ? state.connectionBaseline : nil) ?? ConnectionUsage.baseline(before: previous, connected: latest)
        state.snapshot = latest; state.connectedSinceMillis = since; state.connectionBaseline = baseline
        if latest.state == CoreSnapshot.State.connected {
            tunnelUp = true
        } else if tunnelUp && !latest.tunnelBusy {
            // The Core ended the tunnel by itself (quota, plan, a changed or disabled line, lost
            // lease) or the session ended: say why, even with no screen open.
            tunnelUp = false
            let error = latest.lastError.flatMap { $0.message.isEmpty ? nil : $0 }
            let verdict = latest.entitlement.flatMap { !$0.accountAuthorized && !$0.reasonMessage.isEmpty ? $0 : nil }
            if let error { alerts.tunnelStopped(.core(error.code, error.message)) }
            else if let verdict { alerts.tunnelStopped(.core(verdict.reason, verdict.reasonMessage)) }
            else if latest.state == CoreSnapshot.State.signedOut { alerts.tunnelStopped(.local(LocalCodes.sessionEnded)) }
            else { alerts.tunnelStopped(.local(LocalCodes.tunnelEnded)) }
        }
        if state.activity == .none && !latest.tunnelBusy { Task { await tunnel.stop() } }
        guard state.phase == .signedIn, latest.state == CoreSnapshot.State.signedOut, previous.state != CoreSnapshot.State.signedOut else { return }

        // The Core ended the session (expired, revoked, plan changed).
        stopPolling()
        let error = latest.lastError
        state.phase = .signedOut
        state.message = (error.map { !$0.message.isEmpty } ?? false) ? .core(error!.code, error!.message) : .local(LocalCodes.sessionEnded)
        guard let code = error?.code, code.hasPrefix("native_session_"), autoRelogins < 1 else { return }
        autoRelogins += 1
        Task {
            // One silent re-login for an expired session; a refused password is never retried.
            let email = state.email
            if let saved = try? await credentials.read(account: accountFor(email)), !saved.isEmpty {
                await signIn(email: email, password: saved, remember: true, automatic: true)
            }
        }
    }

    // MARK: error classification

    /// Refusals of the command channel itself; their messages are English internals, not user text.
    private static let ipcCodes: Set<String> = [
        CoreCodes.invalidRequest, CoreCodes.notStarted, "payload_too_large", "unknown_command", "protocol_unsupported",
        "not_implemented", LocalCodes.vpnStartFailed, LocalCodes.vpnStopping, LocalCodes.connectTimeout,
    ]

    private func describe(_ error: CoreException) -> Notice {
        if Self.ipcCodes.contains(error.code) { return .local(error.code) }
        return error.message.isEmpty ? .local(LocalCodes.operationFailed) : .core(error.code, error.message)
    }

    #if DEBUG
    /// UI fixtures only (the screenshot suite): show a prepared state without a Core.
    public func showFixture(_ change: (inout SessionState) -> Void) {
        stopPolling()
        change(&state)
    }
    #endif
}
