package com.asterlink.vpn.core

import java.util.UUID
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.JsonElement

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
