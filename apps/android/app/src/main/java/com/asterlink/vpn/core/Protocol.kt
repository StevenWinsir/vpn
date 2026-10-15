package com.asterlink.vpn.core

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement

/**
 * The Core's command protocol, shared/contracts/ipc-v1.json. The Android app sends the
 * same documents the desktop GUIs put in a pipe frame; native/core/mobile hands them to
 * ipc.HandleOne. Decoding is strict (unknown keys are errors) and every number is a Long,
 * so counters above 2^53 are never rounded through a Double.
 */
object CoreProtocol {
    const val VERSION = 1

    /** Codes the app branches on (never on message text). */
    const val CODE_NOT_STARTED = "core_not_started"
    const val CODE_INVALID_REQUEST = "invalid_request"

    val json = Json {
        ignoreUnknownKeys = false
        explicitNulls = false
        encodeDefaults = true
        isLenient = false
    }
}

@Serializable
data class CoreRequest(
    @SerialName("request_id") val requestId: String,
    @SerialName("protocol_version") val protocolVersion: Int = CoreProtocol.VERSION,
    val generation: Long,
    val command: String,
    val payload: JsonElement? = null,
)

@Serializable
data class CoreError(val code: String, val message: String)

@Serializable
data class CoreResponse(
    @SerialName("request_id") val requestId: String,
    @SerialName("protocol_version") val protocolVersion: Int,
    val generation: Long,
    val ok: Boolean,
    val result: JsonElement? = null,
    val error: CoreError? = null,
)

/** Non-secret build identity (ipc.HelloResult). */
@Serializable
data class HelloResult(
    val component: String,
    @SerialName("core_version") val coreVersion: String,
    @SerialName("protocol_version") val protocolVersion: Int,
    @SerialName("min_protocol_version") val minProtocolVersion: Int,
    @SerialName("max_protocol_version") val maxProtocolVersion: Int,
    @SerialName("mihomo_revision") val mihomoRevision: String,
)

/** A command the Core refused, with its stable code. */
class CoreException(val code: String, message: String) : Exception(message)
