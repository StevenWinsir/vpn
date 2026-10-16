package com.asterlink.vpn.core

import java.time.Instant
import java.time.OffsetDateTime
import java.time.format.DateTimeParseException
import kotlinx.serialization.SerialName
import kotlinx.serialization.SerializationException
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject

// Mirrors shared/contracts/snapshot-v1.json (Go: native/core/internal/managed/snapshot.go;
// Windows: AsterLink.Shared/Snapshot). The snapshot is redacted by construction: no field can
// carry a token, password, YAML, server address, port or node credential. Every integer is a
// Long so counters above 2^53 survive; decoding is strict (CoreProtocol.json).

@Serializable
data class UserView(
    val id: String = "",
    val email: String = "",
    val name: String = "",
    val role: String = "",
)

@Serializable
data class NodeView(
    val id: String = "",
    val name: String = "",
    val region: String = "",
    /** "dedicated" or "direct". "direct" is the ordinary routed line, NOT a Mihomo DIRECT bypass. */
    @SerialName("line_type") val lineType: String = "",
    @SerialName("rate_permille") val ratePermille: Long = 0,
    /** "node_reported" when billed from the node's own counters; empty otherwise. */
    @SerialName("metering_source") val meteringSource: String = "",
)

@Serializable
data class EntitlementView(
    @SerialName("plan_name") val planName: String = "",
    @SerialName("subscription_expires_at") val subscriptionExpiresAt: String? = null,
    @SerialName("authorization_expires_at") val authorizationExpiresAt: String? = null,
    @SerialName("lease_remaining_seconds") val leaseRemainingSeconds: Long = 0,
    /** The account may use the proxy (ignores whether a line is chosen). */
    @SerialName("account_authorized") val accountAuthorized: Boolean = false,
    /** Every gate for pressing Connect right now is satisfied. */
    @SerialName("can_connect") val canConnect: Boolean = false,
    val reason: String = "",
    @SerialName("reason_message") val reasonMessage: String = "",
    @SerialName("total_bytes") val totalBytes: Long = 0,
    @SerialName("remaining_bytes") val remainingBytes: Long = 0,
    /** 1 unit = 1/1000 byte (a 1000 permille line charges 1000 units per byte). */
    @SerialName("used_units") val usedUnits: Long = 0,
) {
    val subscriptionExpires: Instant? get() = parseInstant(subscriptionExpiresAt)
}

@Serializable
data class MeterSample(val upload: Long = 0, val download: Long = 0)

@Serializable
data class MeteringView(
    val source: String = "",
    @SerialName("upload_bytes") val uploadBytes: Long = 0,
    @SerialName("download_bytes") val downloadBytes: Long = 0,
    @SerialName("last_sequence") val lastSequence: Long = 0,
    @SerialName("upload_bytes_per_second") val uploadBytesPerSecond: Long = 0,
    @SerialName("download_bytes_per_second") val downloadBytesPerSecond: Long = 0,
    @SerialName("confirmed_upload_bytes") val confirmedUploadBytes: Long = 0,
    @SerialName("confirmed_download_bytes") val confirmedDownloadBytes: Long = 0,
    @SerialName("confirmed_remaining_bytes") val confirmedRemainingBytes: Long = 0,
    @SerialName("estimated_remaining_bytes") val estimatedRemainingBytes: Long = 0,
    @SerialName("rate_permille") val ratePermille: Long = 0,
    val pending: Boolean = false,
    val history: List<MeterSample> = emptyList(),
)

@Serializable
data class ErrorView(val code: String = "", val message: String = "")

/** The complete UI state returned by the Core. The app renders only this. */
@Serializable
data class CoreSnapshot(
    val generation: Long = 0,
    val state: String = "",
    val user: UserView? = null,
    val entitlement: EntitlementView? = null,
    val nodes: List<NodeView> = emptyList(),
    @SerialName("selected_node_id") val selectedNodeId: String = "",
    @SerialName("catalog_revision") val catalogRevision: String = "",
    @SerialName("profile_version") val profileVersion: String = "",
    @SerialName("config_ready") val configReady: Boolean = false,
    @SerialName("tunnel_health") val tunnelHealth: String = "",
    val metering: MeteringView? = null,
    @SerialName("last_error") val lastError: ErrorView? = null,
    @SerialName("core_version") val coreVersion: String = "",
) {
    val selectedNode: NodeView? get() = nodes.firstOrNull { it.id == selectedNodeId && selectedNodeId.isNotEmpty() }

    /** The data path is up or on its way; selection and sign-in changes wait for it. */
    val tunnelBusy: Boolean get() = state in BUSY_STATES

    companion object {
        val Empty = CoreSnapshot(state = State.SIGNED_OUT)
        private val BUSY_STATES = setOf(State.PREPARING, State.CONNECTING, State.CONNECTED, State.STOPPING)
    }

    /** managed.State values (native/core/internal/managed/manager.go). */
    object State {
        const val SIGNED_OUT = "signed_out"
        const val AUTHENTICATING = "authenticating"
        const val READY = "ready"
        const val PREPARING = "preparing"
        const val CONNECTING = "connecting"
        const val CONNECTED = "connected"
        const val STOPPING = "stopping"
        const val BLOCKED = "blocked"
        const val ERROR = "error"
    }
}

/**
 * What a state-changing command really produced. The Core answers a failed action with
 * ok=true, the new snapshot and an action_error (service.reply), so "ok" alone never means
 * the action succeeded.
 */
data class SnapshotReply(val snapshot: CoreSnapshot, val actionError: ErrorView?) {
    val succeeded: Boolean get() = actionError == null

    companion object {
        /** Splits action_error off the result, then decodes the rest strictly as a snapshot. */
        fun parse(result: JsonElement?): SnapshotReply {
            val body = result as? JsonObject ?: throw SerializationException("snapshot result missing")
            val action = body["action_error"]
            val actionError = if (action == null || action is JsonNull) null
            else CoreProtocol.json.decodeFromJsonElement(ErrorView.serializer(), action)
            val snapshot = CoreProtocol.json.decodeFromJsonElement(CoreSnapshot.serializer(), JsonObject(body - "action_error"))
            return SnapshotReply(snapshot, actionError)
        }
    }
}

/** RFC 3339 as Go writes it (with or without fractional seconds and offset); null when absent or unreadable. */
fun parseInstant(value: String?): Instant? {
    if (value.isNullOrEmpty()) return null
    return try {
        OffsetDateTime.parse(value).toInstant()
    } catch (error: DateTimeParseException) {
        null
    }
}
