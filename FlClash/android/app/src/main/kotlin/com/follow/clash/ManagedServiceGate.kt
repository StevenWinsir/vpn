package com.follow.clash

import com.google.gson.JsonParser
import com.google.gson.Gson
import com.google.gson.JsonObject
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong
import kotlin.coroutines.resume
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull

internal class ManagedServiceGate(
    private val invoke: (String, (String?) -> Unit) -> Unit,
) {
    private val ids = AtomicLong()

    fun stopNow() {
        val request = Gson().toJson(mapOf("id" to "managed-stop-${ids.incrementAndGet()}", "method" to "stopListener", "arguments" to null))
        runCatching { invoke(request) {} }
    }

    suspend fun prepare(port: Int, isCurrent: () -> Boolean): Boolean {
        if (port !in 1024..65535 || !isCurrent()) return false
        val state = rpc("managedStatus") ?: return false
        val generation = unsigned(state, "generation") ?: return false
        val revision = unsigned(state, "runtime_revision") ?: return false
        if (!state.get("configuration").let { it != null && it.isJsonObject } ||
            !state.get("user").let { it != null && it.isJsonObject } || !isCurrent()) return false
        val result = rpc("managedConnect", mapOf(
            "generation" to generation, "runtime_revision" to revision, "mixed_port" to port,
        ), 23_000) ?: return false
        return isCurrent() && permitted(result)
    }

    suspend fun disconnect(): Boolean {
        val state = rpc("managedStatus") ?: return false
        if (state.get("user").let { it == null || it.isJsonNull }) return true
        val generation = unsigned(state, "generation") ?: return false
        val result = rpc("managedDisconnect", mapOf("generation" to generation), 2_500) ?: return false
        return !permitted(result) && result.get("error_code")?.asString.orEmpty().isEmpty()
    }

    private fun unsigned(value: JsonObject, key: String): Long? = runCatching {
        value[key]?.takeIf { it.isJsonPrimitive && it.asJsonPrimitive.isNumber }
            ?.asString?.takeIf { it.matches(Regex("[0-9]{1,15}")) }?.toLong()
    }.getOrNull()

    private fun permitted(value: JsonObject): Boolean = value.get("can_connect")?.let {
        it.isJsonPrimitive && it.asJsonPrimitive.isBoolean && it.asBoolean
    } == true

    private suspend fun rpc(method: String, arguments: Any? = null, timeout: Long = 2_000): JsonObject? = withTimeoutOrNull(timeout) {
        val id = "managed-runtime-${ids.incrementAndGet()}"
        val request = Gson().toJson(mapOf("id" to id, "method" to method, "arguments" to arguments))
        suspendCancellableCoroutine { continuation ->
            val delivered = AtomicBoolean(false)
            continuation.invokeOnCancellation { delivered.set(true) }
            val complete: (String?) -> Unit = { raw ->
                val result = runCatching {
                    if (raw == null || raw.length > 65_536) null else {
                        val response = JsonParser.parseString(raw).asJsonObject
                        if (response["id"]?.asString != id || response.has("error")) null
                        else response.getAsJsonObject("result")
                    }
                }.getOrNull()
                if (delivered.compareAndSet(false, true) && continuation.isActive) continuation.resume(result)
            }
            runCatching { invoke(request, complete) }.onFailure { complete(null) }
        }
    }

    suspend fun allowed(): Boolean = withTimeoutOrNull(2_000) {
        suspendCancellableCoroutine { continuation ->
            val delivered = AtomicBoolean(false)
            continuation.invokeOnCancellation { delivered.set(true) }
            val complete: (Boolean) -> Unit = { allowed ->
                if (delivered.compareAndSet(false, true) && continuation.isActive) {
                    continuation.resume(allowed)
                }
            }
            runCatching {
                invoke(REQUEST) { response -> complete(parse(response)) }
            }.onFailure { complete(false) }
        }
    } ?: false

    private fun parse(value: String?): Boolean {
        if (value == null || value.length > 65_536) return false
        return runCatching {
            val response = JsonParser.parseString(value).asJsonObject
            response.get("id")?.asString == "managed-service-gate" &&
                !response.has("error") &&
                response.getAsJsonObject("result")?.get("can_connect")?.let {
                    it.isJsonPrimitive && it.asJsonPrimitive.isBoolean && it.asBoolean
                } == true
        }.getOrDefault(false)
    }

    private companion object {
        const val REQUEST = """{"id":"managed-service-gate","method":"managedStatus","arguments":null}"""
    }
}
