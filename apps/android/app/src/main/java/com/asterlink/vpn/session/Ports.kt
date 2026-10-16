package com.asterlink.vpn.session

import com.asterlink.vpn.core.SnapshotReply
import java.util.Locale

// What the SessionController needs from the platform, as interfaces so the JVM tests can run
// the real controller against scripted fakes. Android implementations: security/ and vpn/.

/** The in-process Core: [start] is idempotent; [stop] ends the session inside it (bounded settle and revoke). */
interface CoreLifecycle {
    suspend fun start()
    suspend fun stop()
}

class CredentialStoreException(message: String, cause: Throwable? = null) : Exception(message, cause)

/**
 * The remembered password, encrypted with a non-exportable Android Keystore key. There is
 * intentionally no plaintext fallback: a failure surfaces as [CredentialStoreException].
 */
interface CredentialStore {
    suspend fun save(account: String, password: String)
    suspend fun read(account: String): String?
    suspend fun delete(account: String)
}

/** Items are scoped to the exact API base plus the lower-cased email (handoff 5.3). */
object CredentialAccount {
    fun key(apiBase: String, email: String): String = apiBase.trimEnd('/') + "|" + email.trim().lowercase(Locale.ROOT)
}

/** Non-secret remembered choices (never the password). */
interface LoginPreferences {
    suspend fun rememberedEmail(): String?
    suspend fun setRememberedEmail(email: String?)
}

/** A fixed per-installation random UUID (not ANDROID_ID, IMEI or MAC). */
fun interface DeviceIdentity {
    suspend fun deviceId(): String
}

/** How a connect handed to the VPN service ended. */
sealed interface TunnelOutcome {
    /** The Core answered the connect command. */
    data class Replied(val reply: SnapshotReply) : TunnelOutcome

    /** The Core refused it ([code] is a Core code with its [message]) or the service could not run it (a local code). */
    data class Refused(val code: String, val message: String = "") : TunnelOutcome
}

/**
 * The system VPN. A connect must run inside the consented, foreground VpnService (only it can
 * establish the interface and protect sockets), so the controller hands the request over and
 * waits for the Core's reply. The caller has already obtained VPN consent.
 */
interface TunnelControl {
    suspend fun connect(generation: Long): TunnelOutcome

    /** Whether the VPN service is up (in the foreground, registered as the Core's VPN host). */
    val running: Boolean

    /** Leaves the foreground and stops the service; it settles with the Core first. */
    fun stop()
}

/**
 * Tells the user that the VPN ended without them asking (quota used up, plan ended, line
 * disabled or re-rated, lease lost), even when no screen is open: the ongoing notification
 * disappears with the tunnel, so silence would look like a working VPN.
 */
interface SessionAlerts {
    fun tunnelStopped(reason: Notice)

    /** A new connection (or sign-out) makes an earlier alert stale. */
    fun clear()

    object None : SessionAlerts {
        override fun tunnelStopped(reason: Notice) = Unit
        override fun clear() = Unit
    }
}
