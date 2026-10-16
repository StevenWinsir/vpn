package com.asterlink.vpn.core

import java.util.UUID
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** One synchronous request/response exchange with the Core (the gomobile binding in the app). */
fun interface CoreTransport {
    fun call(requestJson: String): String
}

/** The public build parameters every login carries (apps/android/.env through BuildConfig). */
data class LoginTarget(val apiBase: String, val channel: String, val appVersion: String)

/**
 * Typed commands over [CoreTransport] (the Kotlin mirror of the Windows and Swift CoreClient).
 * Every exchange runs on [io]: a Core call blocks for as long as the command runs (connect: up
 * to 90 s), and commands are serialised inside the Core like on the desktop pipe.
 *
 * A refused command (stale generation, malformed payload, Core not started) throws
 * [CoreException]; a business failure comes back inside [SnapshotReply.actionError]. The
 * password appears only in the one-shot login payload built here; nothing keeps it.
 */
class CoreClient(
    private val transport: CoreTransport,
    private val io: CoroutineDispatcher = Dispatchers.IO,
) {
    suspend fun hello(): HelloResult = decode(HelloResult.serializer(), send(Commands.HELLO, generation = 0))

    suspend fun login(email: String, password: String, deviceId: String, target: LoginTarget): SnapshotReply =
        reply(
            send(
                Commands.LOGIN, generation = 0,
                payload = strings(
                    "email" to email,
                    "password" to password,
                    "api_base" to target.apiBase,
                    "channel" to target.channel,
                    "device_id" to deviceId,
                    "app_version" to target.appVersion,
                ),
            ),
        )

    suspend fun readSnapshot(): CoreSnapshot = reply(send(Commands.READ_SNAPSHOT, generation = 0)).snapshot

    suspend fun refreshCatalog(generation: Long): SnapshotReply = reply(send(Commands.REFRESH_CATALOG, generation))

    suspend fun selectNode(generation: Long, nodeId: String, catalogRevision: String): SnapshotReply =
        reply(send(Commands.SELECT_NODE, generation, strings("node_id" to nodeId, "catalog_revision" to catalogRevision)))

    /** Connects the managed session (every gate, the first metering ACK, the TUN, then Verify). */
    suspend fun connect(generation: Long): SnapshotReply = reply(send(Commands.CONNECT, generation, JsonObject(emptyMap())))

    /** Connects at whatever generation the Core is at now (the VPN service's entry point). */
    suspend fun connectCurrent(): SnapshotReply = connect(readSnapshot().generation)

    /** Always safe (no generation): closes the data path and settles the tail. */
    suspend fun disconnect(): CoreSnapshot = reply(send(Commands.DISCONNECT, generation = 0)).snapshot

    /** Always safe: stops, settles, revokes and forgets the session inside the Core. */
    suspend fun logout(): CoreSnapshot = reply(send(Commands.LOGOUT, generation = 0)).snapshot

    private fun strings(vararg pairs: Pair<String, String>): JsonObject =
        JsonObject(pairs.associate { (key, value) -> key to JsonPrimitive(value) })

    private fun reply(result: JsonElement?): SnapshotReply = guarded { SnapshotReply.parse(result) }

    private suspend fun send(command: String, generation: Long, payload: JsonElement? = null): JsonElement? {
        val request = CoreRequest(requestId = UUID.randomUUID().toString(), generation = generation, command = command, payload = payload)
        val raw = withContext(io) {
            transport.call(CoreProtocol.json.encodeToString(CoreRequest.serializer(), request))
        }
        val response = guarded { CoreProtocol.json.decodeFromString(CoreResponse.serializer(), raw) }
        // A response to another request is never applied. Session commands are answered at the
        // generation they quoted, or with the Core's refusal (stale_generation).
        if (response.requestId != request.requestId) {
            throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "core answered a different request")
        }
        response.error?.let { throw CoreException(it.code, it.message) }
        if (!response.ok) throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "core refused without a code")
        if (response.generation != generation) {
            throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "core answered a different request")
        }
        return response.result
    }

    private fun <T> decode(serializer: KSerializer<T>, element: JsonElement?): T {
        if (element == null) throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "empty core result")
        return guarded { CoreProtocol.json.decodeFromJsonElement(serializer, element) }
    }

    private inline fun <T> guarded(block: () -> T): T = try {
        block()
    } catch (error: SerializationException) {
        throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "malformed core result")
    } catch (error: IllegalArgumentException) {
        throw CoreException(CoreProtocol.CODE_INVALID_REQUEST, "malformed core result")
    }
}

/** ipc-v1 command names. */
object Commands {
    const val HELLO = "hello"
    const val LOGIN = "login"
    const val READ_SNAPSHOT = "read_snapshot"
    const val REFRESH_CATALOG = "refresh_catalog"
    const val SELECT_NODE = "select_node"
    const val CONNECT = "connect"
    const val DISCONNECT = "disconnect"
    const val LOGOUT = "logout"
    const val SHUTDOWN = "shutdown"
}
