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
    @ObservationIgnored private let heartbeatInterval: Duration
    @ObservationIgnored private var heartbeat: Task<Void, Never>?

    @ObservationIgnored private var polling: Task<Void, Never>?
    @ObservationIgnored private var autoRelogins = 0
    @ObservationIgnored private var operationVersion = 0
    @ObservationIgnored private var cancelRequested = false
    @ObservationIgnored private var lastRefreshMillis: Int64?
    /// The Core reported this tunnel connected and nobody here has asked it to stop since.
    @ObservationIgnored private var tunnelUp = false
    /// The user has read the explanation of the system's "add VPN configuration" dialog.
    @ObservationIgnored private var setupExplained = false
    /// The session is moving from the extension back to the app's Core: reading either now would
    /// mistake the hand-over for a lost session.
    @ObservationIgnored private var reclaiming = false

    public init(client: CoreClient, core: any CoreHost, target: LoginTarget, credentials: any CredentialStore,
                preferences: any LoginPreferences, devices: any DeviceIDProvider, tunnel: any TunnelControl,
                alerts: any SessionAlerts = NoAlerts(), clock: @escaping @Sendable () -> Int64 = SessionController.monotonicMillis,
                idleInterval: Duration = .seconds(2), liveInterval: Duration = .seconds(1), refreshThrottleMillis: Int64 = 10_000,
                heartbeatInterval: Duration = ForegroundLease.heartbeat) {
        self.client = client; self.core = core; self.target = target; self.credentials = credentials
        self.preferences = preferences; self.devices = devices; self.tunnel = tunnel; self.alerts = alerts
        self.clock = clock; self.idleInterval = idleInterval; self.liveInterval = liveInterval
        self.refreshThrottleMillis = refreshThrottleMillis; self.heartbeatInterval = heartbeatInterval
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
        // The extension may still hold a live session from before the app was closed or killed
        // (D-L05): rejoin it. Signing in again here would open a second session for the device.
        if started, await rejoinRunningTunnel() { return }
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

    /// A tunnel left running by an earlier launch: no login, the extension's snapshot is the truth.
    /// Anything short of a connected session with a known user closes that tunnel instead, so the
    /// ordinary sign-in below starts from a clean state.
    private func rejoinRunningTunnel() async -> Bool {
        guard await tunnel.resume() else { return false }
        guard let latest = try? await client.readSnapshot(), latest.state == CoreSnapshot.State.connected, latest.user != nil else {
            await tunnel.stop()
            return false
        }
        state.rememberPassword = preferences.rememberedEmail() != nil
        if let email = latest.user?.email, !email.isEmpty { state.email = email }
        state.phase = .signedIn
        apply(latest, newOwner: true)
        tunnelUp = true
        startPolling()
        await tunnel.foreground(true)
        startHeartbeat()
        return true
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
            else if error.code == LocalCodes.tunnelUnreachable { await tunnelGone() }
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
        // The system asks once to add the VPN configuration: say why before it does.
        if !setupExplained, !(await tunnel.configured) {
            state.needsVPNSetup = true
            return
        }
        cancelRequested = false
        operationVersion += 1
        alerts.clear()
        state.busy = true; state.activity = .connecting; state.message = nil
        switch await tunnel.connect(generation: state.snapshot.generation) {
        case .replied(let reply):
            if cancelRequested {
                cancelledByUser()
            } else {
                apply(reply.snapshot, newOwner: reply.snapshot.state == CoreSnapshot.State.connected)
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
        if state.snapshot.state == CoreSnapshot.State.connected { await warnIfNotificationsOff() }
    }

    /// A VPN that stops in the background (quota, plan, another VPN) tells the user by notification;
    /// with permission refused it can only say so on the next launch, so say that up front.
    private func warnIfNotificationsOff() async {
        if await tunnel.notificationsAllowed() { return }
        if state.warning == nil { state.warning = .local(LocalCodes.notificationsOff) }
    }

    /// The user read the explanation and chose to continue: connect, which raises the system dialog.
    public func continueVPNSetup() async {
        setupExplained = true
        state.needsVPNSetup = false
        await connect()
    }

    public func cancelVPNSetup() { state.needsVPNSetup = false }

    /// The app moved to or from the foreground (the extension only polls the catalog while seen).
    /// While visible it is repeated: the extension treats the hint as a lease, so an app that is
    /// killed or suspended stops renewing it and the extension drops to the background rhythm.
    public func setForeground(_ visible: Bool) async {
        // Let a repeat that is already on its way finish first, so "hidden" is always the last word.
        let running = heartbeat
        stopHeartbeat()
        await running?.value
        await tunnel.foreground(visible)
        if visible { startHeartbeat() }
    }

    private func startHeartbeat() {
        stopHeartbeat()
        heartbeat = Task { [weak self, heartbeatInterval] in
            while !Task.isCancelled {
                try? await Task.sleep(for: heartbeatInterval)
                if Task.isCancelled { return }
                guard let self else { return }
                await self.tunnel.foreground(true)
            }
        }
    }

    private func stopHeartbeat() {
        heartbeat?.cancel()
        heartbeat = nil
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
        // Hands the session back to this process (and closes the system tunnel); the Core the
        // controller talks to is then the app's own again, so read its state.
        let moved = await tunnel.stop()
        if moved, state.phase == .signedIn, let latest = try? await client.readSnapshot() { apply(latest, newOwner: true) }
        state.busy = false; state.activity = .none
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

    /// A request could not reach the extension: the system took the VPN away (Settings, another
    /// VPN, a crash). The extension already closed the data path; take the session back if the
    /// server still honours it, otherwise the released Core is noticed on the next read.
    private func tunnelGone() async {
        let wasUp = tunnelUp || state.snapshot.tunnelBusy
        tunnelUp = false
        state.connectedSinceMillis = nil; state.connectionBaseline = nil
        let reason = await tunnel.lastEndReason()
        await tunnel.stop()
        guard state.phase == .signedIn else { return }
        if wasUp {
            let notice = reason ?? Notice.local(LocalCodes.vpnRevoked)
            alerts.tunnelStopped(notice)
            state.message = notice
        }
        if let latest = try? await client.readSnapshot() { apply(latest, newOwner: true) }
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
            else if error.code == LocalCodes.tunnelUnreachable { await tunnelGone() }
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
        guard state.phase == .signedIn, !state.busy, !reclaiming else { return }
        let version = operationVersion
        do {
            let latest = try await client.readSnapshot()
            if version != operationVersion || state.phase != .signedIn { return }
            state.coreProblem = nil
            apply(latest)
        } catch let error as CoreException {
            if version != operationVersion { return }
            if error.code == CoreCodes.notStarted { coreLost() }
            else if error.code == LocalCodes.tunnelUnreachable { await tunnelGone() }
            else { state.coreProblem = describe(error) }
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

    /// The tunnel ended by itself: close the system VPN and take the session back (a no-op when
    /// the app's own Core already holds it), then show the Core that now owns the session.
    private func reclaimTunnel() {
        reclaiming = true
        Task {
            let moved = await tunnel.stop()
            reclaiming = false
            if moved, state.phase == .signedIn, let latest = try? await client.readSnapshot() { apply(latest, newOwner: true) }
        }
    }

    private func cancelledByUser() {
        stopPolling()
        tunnelUp = false
        state.phase = .signedOut; state.snapshot = .empty; state.message = .local(LocalCodes.cancelled)
        state.connectedSinceMillis = nil; state.connectionBaseline = nil
        Task { await tunnel.stop() }
    }

    /// `owner` is true for the first snapshot after the session moved between the app's Core and
    /// the extension's: the two count generations separately, so the old number says nothing.
    private func apply(_ latest: CoreSnapshot, newOwner: Bool = false) {
        let previous = state.snapshot
        // A snapshot from an older generation of the same session is late news, not state.
        if !newOwner && state.phase == .signedIn && latest.state != CoreSnapshot.State.signedOut && latest.generation < previous.generation { return }
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
            // The extension holds the session: close the system VPN and take it back.
            if state.activity == .none { reclaimTunnel() }
        }
        // "released": this process handed its session to the extension and the extension could not
        // hand it back (the system stopped it and the server no longer honours the session).
        let released = latest.state == CoreSnapshot.State.released
        guard state.phase == .signedIn, latest.state == CoreSnapshot.State.signedOut || released,
              previous.state != latest.state else { return }

        // The Core ended the session (expired, revoked, plan changed).
        stopPolling()
        let error = latest.lastError
        state.phase = .signedOut
        let kept = released ? state.message : nil
        state.message = kept ?? ((error.map { !$0.message.isEmpty } ?? false) ? .core(error!.code, error!.message) : .local(LocalCodes.sessionEnded))
        guard released || (error?.code.hasPrefix("native_session_") ?? false), autoRelogins < 1 else { return }
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
        LocalCodes.tunnelUnreachable, LocalCodes.routeProbeFailed, "handoff_required",
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
