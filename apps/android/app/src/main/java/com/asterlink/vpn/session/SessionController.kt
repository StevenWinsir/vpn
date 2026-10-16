package com.asterlink.vpn.session

import com.asterlink.vpn.core.CoreClient
import com.asterlink.vpn.core.CoreException
import com.asterlink.vpn.core.CoreProtocol
import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.core.CoreSnapshot.State
import com.asterlink.vpn.core.LoginTarget
import com.asterlink.vpn.core.SnapshotReply
import kotlin.coroutines.cancellation.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * Drives the login and the three tabs (a port of the Windows SessionController, PR #23). It owns
 * no authorization logic: every verdict comes from the Core's snapshot and the UI only renders
 * it. A password lives in a local variable for one login call and is never kept here.
 *
 * Threading: every method runs on [scope], which must be single-threaded (Dispatchers.Main in
 * the app, a test dispatcher in the JVM tests), so the fields below need no locks. Core calls
 * switch to the IO dispatcher inside [CoreClient]. The controller is application-scoped: it
 * keeps watching the session while the VPN runs with no screen open.
 *
 * Semantics kept from Windows: results that raced a user action are discarded (operation
 * version), snapshots of an older generation are late news, action_error is a failed action
 * with fresh state, stale_generation re-reads the state, one silent re-login after a
 * native_session_* expiry (never after a refused password), and a lost Core ends the session
 * (the user signs in online again). Android adds the VPN hand-off ([TunnelControl]) and a
 * throttled catalog refresh when the app comes to the foreground.
 */
class SessionController(
    private val client: CoreClient,
    private val core: CoreLifecycle,
    private val target: LoginTarget,
    private val credentials: CredentialStore,
    private val preferences: LoginPreferences,
    private val devices: DeviceIdentity,
    private val tunnel: TunnelControl,
    private val scope: CoroutineScope,
    /** Monotonic milliseconds (SystemClock.elapsedRealtime in the app). */
    private val clock: () -> Long,
    private val idleIntervalMillis: Long = 2_000,
    private val liveIntervalMillis: Long = 1_000,
    private val refreshThrottleMillis: Long = 10_000,
) {
    private val mutable = MutableStateFlow(SessionState())
    val state: StateFlow<SessionState> = mutable.asStateFlow()

    private val current: SessionState get() = mutable.value
    private var polling: Job? = null
    private var autoRelogins = 0
    private var operationVersion = 0L
    private var cancelRequested = false
    private var lastRefreshMillis: Long? = null

    private fun accountFor(email: String) = CredentialAccount.key(target.apiBase, email)

    private inline fun set(change: (SessionState) -> SessionState) = mutable.update(change)

    // ---- launch ------------------------------------------------------------------------------

    fun launch(): Job = scope.launch { launchNow() }

    /**
     * Called once when the UI first appears. A remembered password only saves typing: the Core
     * still authenticates online, so an expired plan or a changed password is detected now.
     */
    suspend fun launchNow() {
        if (current.phase != AppPhase.Starting) return
        val remembered = try {
            preferences.rememberedEmail()
        } catch (error: CancellationException) {
            throw error
        } catch (error: Exception) {
            null
        }
        set { it.copy(email = remembered.orEmpty()) }
        if (!startCore()) {
            set { it.copy(phase = AppPhase.SignedOut) }
            return
        }
        set { it.copy(phase = AppPhase.SignedOut) }
        val email = current.email
        if (email.isEmpty()) return
        val saved = try {
            credentials.read(accountFor(email))
        } catch (error: CredentialStoreException) {
            set { it.copy(warning = Notice.local(LocalCodes.CREDENTIAL_READ_FAILED)) }
            return
        }
        if (!saved.isNullOrEmpty()) {
            set { it.copy(rememberPassword = true) }
            signInNow(email, saved, remember = true, automatic = true)
        }
    }

    /** Starts the Core (idempotent) and reads its identity once. False when it cannot run. */
    private suspend fun startCore(): Boolean = try {
        core.start()
        if (current.identity == null) {
            val hello = client.hello()
            set { it.copy(identity = hello) }
        }
        set { it.copy(coreProblem = null) }
        true
    } catch (error: CancellationException) {
        throw error
    } catch (error: Exception) {
        set { it.copy(coreProblem = Notice.local(LocalCodes.CORE_START_FAILED)) }
        false
    }

    // ---- sign in / out -----------------------------------------------------------------------

    fun signIn(email: String, password: String, remember: Boolean): Job = scope.launch { signInNow(email, password, remember, automatic = false) }

    suspend fun signInNow(rawEmail: String, password: String, remember: Boolean, automatic: Boolean) {
        val email = rawEmail.trim()
        LoginInput.validate(email, password)?.let { problem ->
            set { it.copy(message = Notice.local(problem)) }
            return
        }
        if (current.busy) return
        operationVersion++
        set { it.copy(busy = true, message = null, warning = null, phase = AppPhase.SigningIn, email = email) }
        try {
            if (!startCore()) {
                set { it.copy(phase = AppPhase.SignedOut, message = Notice.local(LocalCodes.CORE_START_FAILED)) }
                return
            }
            val reply = client.login(email, password, devices.deviceId(), target)
            set { it.copy(snapshot = reply.snapshot) }
            val failure = reply.actionError
            if (failure != null) {
                set { it.copy(phase = AppPhase.SignedOut, message = Notice.core(failure.code, failure.message)) }
                // A saved password the server rejects is stale: stop offering it.
                if (failure.code == "invalid_credentials") tryDelete(accountFor(email))
                return
            }
            set { it.copy(phase = AppPhase.SignedIn, connectedSinceMillis = null) }
            persist(email, password, remember)
            // Only a deliberate sign-in re-arms the single silent re-login, so a server that
            // keeps expiring sessions cannot make the app hammer the login endpoint.
            if (!automatic) autoRelogins = 0
            startPolling()
        } catch (error: CoreException) {
            set { it.copy(phase = AppPhase.SignedOut, message = describe(error)) }
        } catch (error: CancellationException) {
            throw error
        } catch (error: Exception) {
            // The device identity file could not be written, or the Core binding failed.
            set { it.copy(phase = AppPhase.SignedOut, message = Notice.local(LocalCodes.OPERATION_FAILED)) }
        } finally {
            set { it.copy(busy = false) }
        }
        // The Core's watcher delivers the first catalog on its own schedule (about 2 s); ask for
        // it at once, as the desktop clients do after sign-in, so the lines appear immediately.
        if (current.phase == AppPhase.SignedIn) quietRefresh()
    }

    /** A Keystore failure never downgrades to plaintext: the user is told it was not remembered. */
    private suspend fun persist(email: String, password: String, remember: Boolean) {
        val key = accountFor(email)
        if (remember) {
            try {
                credentials.save(key, password)
                preferences.setRememberedEmail(email)
                set { it.copy(rememberPassword = true) }
            } catch (error: CredentialStoreException) {
                set { it.copy(warning = Notice.local(LocalCodes.CREDENTIAL_SAVE_FAILED), rememberPassword = false) }
                tryDelete(key)
            }
        } else {
            tryDelete(key)
            set { it.copy(rememberPassword = false) }
            val remembered = runCatching { preferences.rememberedEmail() }.getOrNull()
            if (remembered != null && remembered.equals(email, ignoreCase = true)) runCatching { preferences.setRememberedEmail(null) }
        }
    }

    private suspend fun tryDelete(key: String) {
        try {
            credentials.delete(key)
        } catch (error: CredentialStoreException) {
            set { it.copy(warning = Notice.local(LocalCodes.CREDENTIAL_DELETE_FAILED)) }
        }
    }

    fun signOut(): Job = scope.launch { signOutNow() }

    /** Explicit sign-out: stop and settle, then forget the saved password for this account. */
    suspend fun signOutNow() {
        if (current.phase != AppPhase.SignedIn || current.busy) return
        stopPolling()
        operationVersion++
        val known = current.snapshot.user?.email.orEmpty()
        val key = accountFor(known.ifEmpty { current.email })
        set { it.copy(phase = AppPhase.SignedOut, busy = true) }
        val after = try {
            client.logout()
        } catch (error: CoreException) {
            CoreSnapshot.Empty
        }
        if (tunnel.running) tunnel.stop()
        tryDelete(key)
        runCatching { preferences.setRememberedEmail(null) }
        set { it.copy(snapshot = after, busy = false, rememberPassword = false, message = null, connectedSinceMillis = null) }
    }

    // ---- actions -----------------------------------------------------------------------------

    fun refresh(): Job = scope.launch { refreshNow() }

    suspend fun refreshNow() {
        if (current.phase != AppPhase.SignedIn || current.busy) return
        lastRefreshMillis = clock()
        run { client.refreshCatalog(current.snapshot.generation) }
    }

    fun onForeground(): Job = scope.launch { onForegroundNow() }

    /**
     * The app came to the foreground: re-read the catalog at most every [refreshThrottleMillis].
     * Quiet (no busy state, so buttons do not flicker) and discarded if a user action raced it.
     */
    suspend fun onForegroundNow() {
        if (current.phase != AppPhase.SignedIn || current.busy) return
        val last = lastRefreshMillis
        if (last != null && clock() - last < refreshThrottleMillis) return
        quietRefresh()
    }

    /** One refresh_catalog that never shows as busy and loses to any user action that races it. */
    private suspend fun quietRefresh() {
        lastRefreshMillis = clock()
        val version = operationVersion
        try {
            val reply = client.refreshCatalog(current.snapshot.generation)
            if (version != operationVersion || current.phase != AppPhase.SignedIn) return
            apply(reply.snapshot)
        } catch (error: CoreException) {
            if (version != operationVersion) return
            if (error.code == CoreProtocol.CODE_NOT_STARTED) coreLost()
            // Anything else (stale_generation, a network refusal) is left to the next poll.
        }
    }

    fun select(nodeId: String): Job = scope.launch { selectNow(nodeId) }

    suspend fun selectNow(nodeId: String) {
        val snapshot = current.snapshot
        if (current.phase != AppPhase.SignedIn || current.busy || snapshot.tunnelBusy) return
        if (nodeId == snapshot.selectedNodeId && snapshot.configReady) return
        run { client.selectNode(snapshot.generation, nodeId, snapshot.catalogRevision) }
    }

    fun switchTo(nodeId: String): Job = scope.launch { switchToNow(nodeId) }

    /**
     * The user confirmed switching lines while connected: stop and settle the old segment at
     * its own rate, then select the new line and stay disconnected until Connect (no seamless
     * switch, handoff 7.4).
     */
    suspend fun switchToNow(nodeId: String) {
        if (current.phase != AppPhase.SignedIn || current.busy) return
        if (current.snapshot.state == State.CONNECTED) disconnectNow()
        if (current.snapshot.tunnelBusy) return
        selectNow(nodeId)
    }

    fun connect(): Job = scope.launch { connectNow() }

    /** VPN consent is already granted (the activity ran VpnService.prepare). */
    suspend fun connectNow() {
        if (current.phase != AppPhase.SignedIn || current.busy || !current.connection.canConnect) return
        cancelRequested = false
        operationVersion++
        set { it.copy(busy = true, activity = SessionActivity.Connecting, message = null) }
        try {
            when (val outcome = tunnel.connect(current.snapshot.generation)) {
                is TunnelOutcome.Replied -> {
                    if (cancelRequested) {
                        cancelledByUser()
                        return
                    }
                    apply(outcome.reply.snapshot)
                    set { it.copy(message = outcome.reply.actionError?.let { e -> Notice.core(e.code, e.message) }) }
                }
                is TunnelOutcome.Refused -> when {
                    cancelRequested -> cancelledByUser()
                    outcome.code == "stale_generation" -> staleGeneration(outcome.message)
                    outcome.code == CoreProtocol.CODE_NOT_STARTED -> coreLost()
                    outcome.message.isNotEmpty() && outcome.code !in IPC_CODES -> set { it.copy(message = Notice.core(outcome.code, outcome.message)) }
                    else -> set { it.copy(message = Notice.local(outcome.code)) }
                }
            }
        } finally {
            set { it.copy(busy = false, activity = SessionActivity.None) }
        }
    }

    /** The user declined the system VPN dialog: stay ready and say why nothing happened. */
    fun vpnConsentDenied() {
        set { it.copy(message = Notice.local(LocalCodes.VPN_PERMISSION_DENIED)) }
    }

    fun disconnect(): Job = scope.launch { disconnectNow() }

    suspend fun disconnectNow() {
        if (current.phase != AppPhase.SignedIn || current.busy) return
        operationVersion++
        set { it.copy(busy = true, activity = SessionActivity.Disconnecting) }
        try {
            // Closing the data path also settles the last traffic batch (a few seconds on a slow network).
            val updated = client.disconnect()
            apply(updated)
            set { it.copy(message = updated.lastError?.takeIf { e -> e.message.isNotEmpty() }?.let { e -> Notice.core(e.code, e.message) }) }
        } catch (error: CoreException) {
            if (error.code == CoreProtocol.CODE_NOT_STARTED) coreLost() else set { it.copy(message = describe(error)) }
        } finally {
            set { it.copy(busy = false, activity = SessionActivity.None) }
            if (tunnel.running) tunnel.stop()
        }
    }

    fun cancelConnect(): Job = scope.launch { cancelConnectNow() }

    /**
     * Abandons a connect that takes too long. The command is already inside the Core, so the
     * only reliable way back to a known state is to end the whole session: the Core is stopped
     * (it settles and revokes), the VPN service ends, and the user signs in again.
     */
    suspend fun cancelConnectNow() {
        if (!current.busy || current.phase != AppPhase.SignedIn || current.activity != SessionActivity.Connecting) return
        cancelRequested = true
        stopPolling()
        try {
            core.stop()
        } catch (error: CancellationException) {
            throw error
        } catch (error: Exception) {
            // Already stopped.
        }
        if (tunnel.running) tunnel.stop()
    }

    fun dismissMessage() {
        set { it.copy(message = null, warning = null) }
    }

    private suspend fun run(action: suspend () -> SnapshotReply) {
        cancelRequested = false
        operationVersion++
        set { it.copy(busy = true) }
        try {
            val reply = action()
            apply(reply.snapshot)
            set { it.copy(message = reply.actionError?.let { e -> Notice.core(e.code, e.message) }) }
        } catch (error: CoreException) {
            when {
                error.code == "stale_generation" -> staleGeneration(error.message.orEmpty())
                error.code == CoreProtocol.CODE_NOT_STARTED -> coreLost()
                else -> set { it.copy(message = describe(error)) }
            }
        } finally {
            set { it.copy(busy = false) }
        }
    }

    /** The state changed under the click: show the new state and let the user decide again. */
    private suspend fun staleGeneration(message: String) {
        try {
            apply(client.readSnapshot())
        } catch (error: CoreException) {
            // The next poll tries again.
        }
        set { it.copy(message = if (message.isNotEmpty()) Notice.core("stale_generation", message) else Notice.local(LocalCodes.STATE_CHANGED)) }
    }

    // ---- polling -----------------------------------------------------------------------------

    private fun startPolling() {
        stopPolling()
        polling = scope.launch {
            while (isActive) {
                // Live rates are only worth a faster read while a tunnel carries traffic.
                delay(if (current.snapshot.state == State.CONNECTED) liveIntervalMillis else idleIntervalMillis)
                pollNow()
            }
        }
    }

    private fun stopPolling() {
        polling?.cancel()
        polling = null
    }

    /** One snapshot read. A result that raced a user action is discarded, never applied over newer state. */
    suspend fun pollNow() {
        if (current.phase != AppPhase.SignedIn || current.busy) return
        val version = operationVersion
        try {
            val latest = client.readSnapshot()
            if (version != operationVersion || current.phase != AppPhase.SignedIn) return
            set { it.copy(coreProblem = null) }
            apply(latest)
        } catch (error: CoreException) {
            if (version != operationVersion) return
            if (error.code == CoreProtocol.CODE_NOT_STARTED) coreLost() else set { it.copy(coreProblem = describe(error)) }
        }
    }

    /**
     * The Core stopped (the user cancelled a connect, or it failed). The session, token and
     * tunnel lived only inside it, so nothing is resumed: the user signs in online again.
     */
    private fun coreLost() {
        stopPolling()
        set { it.copy(phase = AppPhase.SignedOut, snapshot = CoreSnapshot.Empty, message = Notice.local(LocalCodes.CORE_LOST), connectedSinceMillis = null) }
        if (tunnel.running) tunnel.stop()
    }

    private fun cancelledByUser() {
        stopPolling()
        set { it.copy(phase = AppPhase.SignedOut, snapshot = CoreSnapshot.Empty, message = Notice.local(LocalCodes.CANCELLED), connectedSinceMillis = null) }
        if (tunnel.running) tunnel.stop()
    }

    private fun apply(latest: CoreSnapshot) {
        val previous = current.snapshot
        // A snapshot from an older generation of the same session is late news, not state.
        if (current.phase == AppPhase.SignedIn && latest.state != State.SIGNED_OUT && latest.generation < previous.generation) return
        val since = when {
            latest.state != State.CONNECTED -> null
            previous.state == State.CONNECTED && current.connectedSinceMillis != null -> current.connectedSinceMillis
            else -> clock()
        }
        set { it.copy(snapshot = latest, connectedSinceMillis = since) }
        // The Core ended the tunnel by itself (quota, plan, lease, a changed line): the VPN
        // interface is already gone with its descriptor; leave the foreground as well.
        if (current.activity == SessionActivity.None && !latest.tunnelBusy && tunnel.running) tunnel.stop()
        if (current.phase != AppPhase.SignedIn || latest.state != State.SIGNED_OUT || previous.state == State.SIGNED_OUT) return

        // The Core ended the session (expired, revoked, plan changed).
        stopPolling()
        val error = latest.lastError
        set {
            it.copy(
                phase = AppPhase.SignedOut,
                message = if (error != null && error.message.isNotEmpty()) Notice.core(error.code, error.message) else Notice.local(LocalCodes.SESSION_ENDED),
            )
        }
        val code = error?.code.orEmpty()
        if (!code.startsWith("native_session_") || autoRelogins >= 1) return
        autoRelogins++
        scope.launch {
            // One silent re-login for an expired session; a refused password is never retried.
            val email = current.email
            val saved = try {
                credentials.read(accountFor(email))
            } catch (error: CredentialStoreException) {
                null
            }
            if (!saved.isNullOrEmpty()) signInNow(email, saved, remember = true, automatic = true)
        }
    }

    // ---- error classification ----------------------------------------------------------------

    private fun describe(error: CoreException): Notice {
        val code = error.code
        val text = error.message.orEmpty()
        return if (code in IPC_CODES || text.isEmpty()) Notice.local(if (code in IPC_CODES) code else LocalCodes.OPERATION_FAILED)
        else Notice.core(code, text)
    }

    private companion object {
        /** Refusals of the command channel itself; their messages are English internals, not user text. */
        val IPC_CODES = setOf(
            CoreProtocol.CODE_INVALID_REQUEST, CoreProtocol.CODE_NOT_STARTED, "payload_too_large", "unknown_command",
            "protocol_unsupported", "not_implemented",
            LocalCodes.VPN_START_FAILED, LocalCodes.VPN_STOPPING, LocalCodes.CONNECT_TIMEOUT,
        )
    }
}
