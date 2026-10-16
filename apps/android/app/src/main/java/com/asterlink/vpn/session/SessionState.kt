package com.asterlink.vpn.session

import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.core.HelloResult
import com.asterlink.vpn.core.MeterSample
import java.nio.charset.StandardCharsets

enum class AppPhase {
    /** Starting the in-process Core. */
    Starting,
    SignedOut,
    SigningIn,
    SignedIn,
}

/** Which long command is in flight; only a connect can be cancelled. */
enum class SessionActivity { None, Connecting, Disconnecting }

/**
 * Something to tell the user. A Core refusal carries the Core's own Chinese text ([text]);
 * a problem found on this side carries only a [code] the UI turns into a string resource
 * (values/strings.xml, values-en/strings.xml). Logic branches on [code], never on text.
 */
data class Notice(val code: String, val text: String? = null) {
    companion object {
        fun core(code: String, message: String): Notice = Notice(code, message.ifEmpty { null })
        fun local(code: String): Notice = Notice(code, null)
    }
}

/** Local notice codes (ErrorText.kt maps them to strings). */
object LocalCodes {
    const val INPUT_MISSING = "input_missing"
    const val EMAIL_TOO_LONG = "email_too_long"
    const val PASSWORD_TOO_LONG = "password_too_long"
    const val CORE_START_FAILED = "core_start_failed"
    const val CORE_LOST = "core_lost"
    const val CANCELLED = "connect_cancelled"
    const val SESSION_ENDED = "session_ended"
    const val STATE_CHANGED = "state_changed"
    const val OPERATION_FAILED = "operation_failed"
    const val VPN_PERMISSION_DENIED = "vpn_permission_denied"
    const val VPN_START_FAILED = "vpn_start_failed"
    const val VPN_STOPPING = "vpn_stopping"
    const val CONNECT_TIMEOUT = "connect_timeout"
    const val CREDENTIAL_READ_FAILED = "credential_read_failed"
    const val CREDENTIAL_SAVE_FAILED = "credential_save_failed"
    const val CREDENTIAL_DELETE_FAILED = "credential_delete_failed"
    const val NO_PLAN = "no_plan"
    const val NO_LINES = "no_lines"
    const val SELECT_LINE = "select_line"
    const val NO_MATCH = "no_match"
    const val SYNCING = "syncing_lines"
    /** The tunnel ended without the user asking and the Core gave no reason. */
    const val TUNNEL_ENDED = "tunnel_ended"
}

/** Everything the screens render. Derived from the Core's snapshot; nothing here decides authorization. */
data class SessionState(
    val phase: AppPhase = AppPhase.Starting,
    val snapshot: CoreSnapshot = CoreSnapshot.Empty,
    val message: Notice? = null,
    val warning: Notice? = null,
    val busy: Boolean = false,
    val activity: SessionActivity = SessionActivity.None,
    /** The Core cannot be used at all (distinct from a business refusal). */
    val coreProblem: Notice? = null,
    /** Prefills the login form: the remembered email, or the one last typed. */
    val email: String = "",
    val rememberPassword: Boolean = false,
    val identity: HelloResult? = null,
    /** elapsedRealtime when the Core first reported this tunnel connected (display only). */
    val connectedSinceMillis: Long? = null,
    /** The Core's metering counters when this tunnel came up: "this connection" starts here. */
    val connectionBaseline: MeterSample? = null,
) {
    val connection: ConnectionView get() = ConnectionView.from(snapshot, busy, activity)

    /** Usage of the current connection only (null unless connected). */
    val usage: ConnectionUsage? get() = ConnectionUsage.of(snapshot, connectionBaseline)
}

/**
 * Bytes carried by the current connection and what they cost. The Core's client-reported
 * counters are cumulative for the whole sign-in (they span connections, possibly at different
 * rates), so the connection's share is the difference to [SessionState.connectionBaseline];
 * a connection binds exactly one rate (a changed rate ends it), so billed = physical x rate.
 * Integers only, like the server's ledger (handoff 8).
 */
data class ConnectionUsage(val uploadBytes: Long, val downloadBytes: Long, val ratePermille: Long) {
    val physicalBytes: Long get() = uploadBytes + downloadBytes
    val billedBytes: Long get() = Formatting.billedBytes(physicalBytes, ratePermille)

    companion object {
        fun of(snapshot: CoreSnapshot, baseline: MeterSample?): ConnectionUsage? {
            val metering = snapshot.metering ?: return null
            if (snapshot.state != CoreSnapshot.State.CONNECTED) return null
            val base = baseline ?: MeterSample()
            return ConnectionUsage(
                uploadBytes = (metering.uploadBytes - base.upload).coerceAtLeast(0),
                downloadBytes = (metering.downloadBytes - base.download).coerceAtLeast(0),
                ratePermille = metering.ratePermille,
            )
        }

        /**
         * Where a new connection's count starts. Client-reported counters continue from the
         * last snapshot before the connect; a node-reported connection has its own local meter
         * that starts at zero.
         */
        fun baseline(before: CoreSnapshot, connected: CoreSnapshot): MeterSample {
            val now = connected.metering
            if (now?.source == "node_reported") return MeterSample()
            val prior = before.metering?.takeIf { it.source == now?.source } ?: now ?: return MeterSample()
            return MeterSample(prior.uploadBytes, prior.downloadBytes)
        }
    }
}

object LoginInput {
    const val MAX_EMAIL_LENGTH = 254
    const val MAX_PASSWORD_BYTES = 72

    /** The Core's limits (native/core/internal/service/service.go); null when acceptable. */
    fun validate(email: String, password: String): String? {
        val trimmed = email.trim()
        return when {
            trimmed.isEmpty() || password.isEmpty() -> LocalCodes.INPUT_MISSING
            trimmed.toByteArray(StandardCharsets.UTF_8).size > MAX_EMAIL_LENGTH -> LocalCodes.EMAIL_TOO_LONG
            password.toByteArray(StandardCharsets.UTF_8).size > MAX_PASSWORD_BYTES -> LocalCodes.PASSWORD_TOO_LONG
            else -> null
        }
    }
}
