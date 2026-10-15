package com.asterlink.vpn.core

import java.util.UUID
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

/** One synchronous request/response exchange with the Core (the gomobile binding in the app). */
fun interface CoreTransport {
    fun call(requestJson: String): String
}

/**
 * Typed commands over [CoreTransport]. Every exchange runs on [io]: a Core call blocks for as
 * long as the command runs (connect: up to 90 s). Round 3 adds the session commands and the
 * SessionController on top of this.
 */
class CoreClient(
    private val transport: CoreTransport,
    private val io: CoroutineDispatcher = Dispatchers.IO,
) {
    suspend fun hello(): HelloResult {
        val result = send("hello", generation = 0)
        return decode(HelloResult.serializer(), result)
    }

    /**
     * Connects the managed session at its current generation. Null when the Core reports
     * connected (Verify passed); otherwise its stable code (action_error, then last_error).
     * Round 3 replaces the field reads with the typed snapshot-v1 model.
     */
    suspend fun connect(): String? {
        val generation = snapshotGeneration(send("read_snapshot", generation = 0))
        val snapshot = send("connect", generation = generation, payload = JsonObject(emptyMap()))?.jsonObject
            ?: throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "empty core result")
        if (snapshot["state"]?.jsonPrimitive?.contentOrNull == "connected" && snapshot["action_error"] !is JsonObject) return null
        return errorCode(snapshot["action_error"]) ?: errorCode(snapshot["last_error"]) ?: "tunnel_failed"
    }

    /** Always safe (no generation): closes the data path and settles the tail. */
    suspend fun disconnect() {
        send("disconnect", generation = 0)
    }

    private fun snapshotGeneration(result: JsonElement?): Long =
        (result as? JsonObject)?.get("generation")?.jsonPrimitive?.longOrNull
            ?: throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "snapshot without generation")

    private fun errorCode(element: JsonElement?): String? =
        (element as? JsonObject)?.get("code")?.jsonPrimitive?.contentOrNull

    private suspend fun send(command: String, generation: Long, payload: JsonElement? = null): JsonElement? {
        val request = CoreRequest(requestId = UUID.randomUUID().toString(), generation = generation, command = command, payload = payload)
        val raw = withContext(io) {
            transport.call(CoreProtocol.json.encodeToString(CoreRequest.serializer(), request))
        }
        val response = try {
            CoreProtocol.json.decodeFromString(CoreResponse.serializer(), raw)
        } catch (error: SerializationException) {
            throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "malformed core response")
        } catch (error: IllegalArgumentException) {
            throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "malformed core response")
        }
        // A response to another request (or another session generation) is never applied.
        if (response.requestId != request.requestId || response.generation != generation) {
            throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "core answered a different request")
        }
        response.error?.let { throw CoreException(it.code, it.message) }
        if (!response.ok) throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "core refused without a code")
        return response.result
    }

    private fun <T> decode(serializer: kotlinx.serialization.KSerializer<T>, element: JsonElement?): T {
        if (element == null) throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "empty core result")
        return try {
            CoreProtocol.json.decodeFromJsonElement(serializer, element)
        } catch (error: SerializationException) {
            throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "malformed core result")
        } catch (error: IllegalArgumentException) {
            throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "malformed core result")
        }
    }
}
