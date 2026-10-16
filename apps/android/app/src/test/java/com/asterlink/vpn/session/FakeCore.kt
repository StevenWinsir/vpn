package com.asterlink.vpn.session

import com.asterlink.vpn.core.CoreClient
import com.asterlink.vpn.core.CoreProtocol
import com.asterlink.vpn.core.CoreRequest
import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.core.CoreSnapshot.State
import com.asterlink.vpn.core.CoreTransport
import com.asterlink.vpn.core.EntitlementView
import com.asterlink.vpn.core.ErrorView
import com.asterlink.vpn.core.MeteringView
import com.asterlink.vpn.core.NodeView
import com.asterlink.vpn.core.UserView
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

/**
 * A scripted stand-in for the Go Core behind the binding: the same ipc-v1 documents, the same
 * generation check for refresh/select/connect, failures as ok=true + action_error. Only the
 * behaviour the SessionController relies on is modelled.
 */
class FakeCore : CoreTransport {
    data class Account(val password: String, val entitlement: EntitlementView, val role: String = "user")

    val accounts = mutableMapOf<String, Account>()
    var nodes = listOf(
        NodeView("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "HK Dedicated", "HK", "dedicated", 1000),
        NodeView("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "JP Standard", "JP", "direct", 500),
    )
    var snapshot = CoreSnapshot.Empty
    var started = true
    var deviceLimit = false
    /** Business failure the next connect reports (null = connects). */
    var connectFailure: ErrorView? = null
    val calls = mutableListOf<String>()
    val loginPasswords = mutableListOf<String>()
    val deviceIds = mutableListOf<String>()

    fun count(command: String) = calls.count { it == command }

    /** The Core changed its own state (watcher, lease, expiry): a new generation. */
    fun mutate(change: (CoreSnapshot) -> CoreSnapshot) {
        snapshot = change(snapshot).copy(generation = snapshot.generation + 1)
    }

    override fun call(requestJson: String): String {
        val request = CoreProtocol.json.decodeFromString(CoreRequest.serializer(), requestJson)
        calls += request.command
        if (!started && request.command != "hello") return refuse(request, CoreProtocol.CODE_NOT_STARTED, "core is not running")
        val needsGeneration = request.command in setOf("refresh_catalog", "select_node", "connect")
        if (needsGeneration && request.generation != snapshot.generation) return refuse(request, "stale_generation", "状态已变化，请刷新后重试")
        return when (request.command) {
            "hello" -> respond(request, buildJsonObject {
                put("component", "asterlink-core"); put("core_version", "0.1.0"); put("protocol_version", 1)
                put("min_protocol_version", 1); put("max_protocol_version", 1); put("mihomo_revision", "70f0570405c3c2c47bb113b88db95006d239b346")
            })
            "login" -> login(request)
            "read_snapshot" -> respond(request, encode(snapshot))
            "refresh_catalog" -> {
                snapshot = snapshot.copy(nodes = if (snapshot.entitlement?.accountAuthorized == true) nodes else emptyList())
                respond(request, encode(snapshot, null))
            }
            "select_node" -> {
                val id = request.payload!!.jsonObject.getValue("node_id").jsonPrimitive.content
                snapshot = snapshot.copy(selectedNodeId = id, configReady = true)
                respond(request, encode(snapshot, null))
            }
            "connect" -> {
                val failure = connectFailure
                if (failure != null) {
                    snapshot = snapshot.copy(state = State.READY, lastError = failure)
                    respond(request, encode(snapshot, failure))
                } else {
                    snapshot = snapshot.copy(state = State.CONNECTED, tunnelHealth = "up", lastError = null,
                        metering = MeteringView(source = "client_reported", uploadBytesPerSecond = 2048, downloadBytesPerSecond = 1048576))
                    respond(request, encode(snapshot, null))
                }
            }
            "disconnect" -> {
                if (snapshot.state == State.CONNECTED) snapshot = snapshot.copy(state = State.READY, tunnelHealth = "idle")
                respond(request, encode(snapshot))
            }
            "logout" -> {
                snapshot = CoreSnapshot.Empty.copy(generation = snapshot.generation + 1)
                respond(request, encode(snapshot))
            }
            else -> refuse(request, "unknown_command", "unknown command")
        }
    }

    private fun login(request: CoreRequest): String {
        val payload = request.payload!!.jsonObject
        val email = payload.getValue("email").jsonPrimitive.content
        val password = payload.getValue("password").jsonPrimitive.content
        loginPasswords += password
        deviceIds += payload.getValue("device_id").jsonPrimitive.content
        val account = accounts[email.lowercase()]
        if (account == null || account.password != password) {
            snapshot = CoreSnapshot.Empty.copy(generation = snapshot.generation + 1)
            return respond(request, encode(snapshot, ErrorView("invalid_credentials", "邮箱或密码不正确")))
        }
        if (deviceLimit) {
            snapshot = CoreSnapshot.Empty.copy(generation = snapshot.generation + 1)
            return respond(request, encode(snapshot, ErrorView("device_limit", "已达到套餐同时在线设备上限，请先在其他设备退出")))
        }
        val authorized = account.entitlement.accountAuthorized
        snapshot = CoreSnapshot(
            generation = snapshot.generation + 1,
            state = if (authorized) State.READY else State.BLOCKED,
            user = UserView("22222222-2222-4222-8222-222222222222", email, "Fixture", account.role),
            entitlement = account.entitlement,
            nodes = if (authorized) nodes else emptyList(),
            catalogRevision = "191ed7ece6ec8b4ae68e897af41900f7de83ae3cabf80592a1406e97a405547a",
            tunnelHealth = "idle",
            metering = MeteringView(source = "client_reported"),
            coreVersion = "0.1.0",
        )
        return respond(request, encode(snapshot, null))
    }

    private fun encode(value: CoreSnapshot, actionError: ErrorView? = null, withAction: Boolean = true): JsonElement {
        val body = CoreProtocol.json.encodeToJsonElement(CoreSnapshot.serializer(), value).jsonObject
        if (!withAction) return body
        return JsonObject(body + ("action_error" to (actionError?.let { CoreProtocol.json.encodeToJsonElement(ErrorView.serializer(), it) } ?: JsonNull)))
    }

    private fun encode(value: CoreSnapshot): JsonElement = encode(value, null, withAction = false)

    private fun respond(request: CoreRequest, result: JsonElement): String = CoreProtocol.json.encodeToString(
        JsonObject.serializer(),
        buildJsonObject {
            put("request_id", request.requestId); put("protocol_version", 1); put("generation", request.generation); put("ok", true)
            put("result", result)
        },
    )

    private fun refuse(request: CoreRequest, code: String, message: String): String = CoreProtocol.json.encodeToString(
        JsonObject.serializer(),
        buildJsonObject {
            put("request_id", request.requestId); put("protocol_version", 1); put("generation", request.generation); put("ok", false)
            put("error", buildJsonObject { put("code", code); put("message", message) })
        },
    )

    companion object {
        val VIP = EntitlementView(
            planName = "VIP", subscriptionExpiresAt = "2030-01-01T00:10:00Z", accountAuthorized = true, canConnect = false,
            reason = "", totalBytes = 1L shl 30, remainingBytes = (1L shl 30) - 1000,
        )
        val NO_PLAN = EntitlementView(
            accountAuthorized = false, reason = "upgrade_required", reasonMessage = "当前账号没有有效套餐，请前往网站购买",
        )
        val EXPIRED = EntitlementView(
            planName = "VIP", subscriptionExpiresAt = "2020-01-01T00:00:00Z", accountAuthorized = false,
            reason = "subscription_expired", reasonMessage = "套餐已到期，请前往网站续费", totalBytes = 1L shl 30, remainingBytes = 1L shl 29,
        )
        val NO_QUOTA = EntitlementView(
            planName = "VIP", subscriptionExpiresAt = "2030-01-01T00:10:00Z", accountAuthorized = false,
            reason = "quota_exhausted", reasonMessage = "套餐流量已用完，请前往网站续费或升级", totalBytes = 1L shl 30, remainingBytes = 0,
        )
    }
}

class FakeCredentials : CredentialStore {
    val items = mutableMapOf<String, String>()
    var failSave = false
    var failRead = false

    override suspend fun save(account: String, password: String) {
        if (failSave) throw CredentialStoreException("keystore unavailable")
        items[account] = password
    }

    override suspend fun read(account: String): String? {
        if (failRead) throw CredentialStoreException("keystore unavailable")
        return items[account]
    }

    override suspend fun delete(account: String) {
        items.remove(account)
    }
}

class FakePreferences(var email: String? = null) : LoginPreferences {
    override suspend fun rememberedEmail(): String? = email
    override suspend fun setRememberedEmail(email: String?) {
        this.email = email
    }
}

class FakeLifecycle(private val core: FakeCore) : CoreLifecycle {
    var starts = 0
    var stops = 0
    var failStart = false

    override suspend fun start() {
        if (failStart) throw IllegalStateException("libgojni.so missing")
        starts++
        core.started = true
    }

    override suspend fun stop() {
        stops++
        core.started = false
    }
}

/** The VPN service, collapsed: it runs the Core's connect at the requested generation. */
class FakeTunnel(private val client: () -> CoreClient) : TunnelControl {
    override var running = false
    var stops = 0
    var refuseWith: String? = null
    var beforeReply: () -> Unit = {}

    override suspend fun connect(generation: Long): TunnelOutcome {
        refuseWith?.let { return TunnelOutcome.Refused(it) }
        running = true
        beforeReply()
        return try {
            val reply = client().connect(generation)
            if (!reply.succeeded) running = false
            TunnelOutcome.Replied(reply)
        } catch (error: com.asterlink.vpn.core.CoreException) {
            running = false
            TunnelOutcome.Refused(error.code, error.message.orEmpty())
        }
    }

    override fun stop() {
        stops++
        running = false
    }
}
