package com.follow.clash

import kotlinx.coroutines.CoroutineStart
import com.google.gson.JsonParser
import com.google.gson.Gson
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class ManagedServiceGateTest {
    @Test
    fun `headless preparation forwards only fresh generation revision and port`() = runTest {
        val methods = mutableListOf<String>()
        val gate = ManagedServiceGate { raw, reply ->
            val request = JsonParser.parseString(raw).asJsonObject
            val method = request["method"].asString
            methods += method
            val result = if (method == "managedStatus") {
                mapOf("generation" to 7, "runtime_revision" to 12, "user" to emptyMap<String,String>(), "configuration" to emptyMap<String,String>(), "can_connect" to false)
            } else {
                assertEquals("managedConnect", method)
                val args = request.getAsJsonObject("arguments")
                assertEquals(7, args["generation"].asInt)
                assertEquals(12, args["runtime_revision"].asInt)
                assertEquals(7890, args["mixed_port"].asInt)
                assertEquals(3, args.size())
                mapOf("can_connect" to true)
            }
            reply(Gson().toJson(mapOf("id" to request["id"].asString, "result" to result)))
        }
        assertTrue(gate.prepare(7890) { true })
        assertEquals(listOf("managedStatus", "managedConnect"), methods)
    }

    @Test
    fun `stop overtaking status blocks a delayed native Connect`() = runTest {
        var current = true
        var count = 0
        val gate = ManagedServiceGate { raw, reply ->
            count++
            val request = JsonParser.parseString(raw).asJsonObject
            current = false
            reply("""{"id":${request["id"]},"result":{"generation":7,"runtime_revision":12,"user":{},"configuration":{}}}""")
        }
        assertFalse(gate.prepare(7890) { current })
        assertEquals(1, count)
    }

    @Test
    fun `cold restart cannot recover authorization from stored VPN options`() = runTest {
        var count = 0
        val gate = ManagedServiceGate { raw, reply ->
            count++
            val request = JsonParser.parseString(raw).asJsonObject
            reply("""{"id":${request["id"]},"result":{"generation":0,"runtime_revision":0,"user":null,"can_connect":false}}""")
        }
        assertFalse(gate.prepare(7890) { true })
        assertEquals(1, count)
    }

    private val allowed = """{"id":"managed-service-gate","result":{"can_connect":true}}"""
    private val denied = """{"id":"managed-service-gate","result":{"can_connect":false}}"""

    @Test
    fun `only a correlated top level boolean grants permission`() = runTest {
        assertTrue(ManagedServiceGate { _, reply -> reply(allowed) }.allowed())
        assertFalse(ManagedServiceGate { _, reply -> reply(denied) }.allowed())
    }

    @Test
    fun `status request does not carry credentials or cached authorization`() = runTest {
        var observed: String? = null
        val gate = ManagedServiceGate { request, reply ->
            observed = request
            reply(denied)
        }
        assertFalse(gate.allowed())
        assertEquals(
            """{"id":"managed-service-gate","method":"managedStatus","arguments":null}""",
            observed,
        )
    }

    @Test
    fun `malformed absent oversized and uncorrelated replies fail closed`() = runTest {
        val replies = listOf(
            null,
            "",
            "[]",
            "{",
            "x".repeat(65_537),
            """{"result":{"can_connect":true}}""",
            """{"id":"wrong","result":{"can_connect":true}}""",
            """{"id":"managed-service-gate","error":{},"result":{"can_connect":true}}""",
            """{"id":"managed-service-gate","error":null,"result":{"can_connect":true}}""",
            """{"id":"managed-service-gate","result":{"can_connect":"true"}}""",
            """{"id":"managed-service-gate","result":{"can_connect":1}}""",
            """{"id":"managed-service-gate","result":{"session":{"can_connect":true}}}""",
            """{"id":"managed-service-gate","result":{}}""",
        )
        replies.forEachIndexed { index, response ->
            assertFalse("reply case $index", ManagedServiceGate { _, reply -> reply(response) }.allowed())
        }
    }

    @Test
    fun `a duplicate callback cannot resume twice`() = runTest {
        assertTrue(ManagedServiceGate { _, reply -> reply(allowed); reply(allowed) }.allowed())
    }

    @Test
    fun `a late approval cannot replace an earlier rejection`() = runTest {
        assertFalse(ManagedServiceGate { _, reply -> reply(denied); reply(allowed) }.allowed())
    }

    @Test
    fun `native invocation failure denies permission`() = runTest {
        assertFalse(ManagedServiceGate { _, _ -> error("native failure") }.allowed())
    }

    @Test
    fun `exception after a completed response cannot resume again`() = runTest {
        assertTrue(ManagedServiceGate { _, reply -> reply(allowed); error("late failure") }.allowed())
    }

    @Test
    fun `two second timeout rejects and ignores a later reply`() = runTest {
        lateinit var reply: (String?) -> Unit
        val pending = async(start = CoroutineStart.UNDISPATCHED) {
            ManagedServiceGate { _, callback -> reply = callback }.allowed()
        }
        advanceTimeBy(1_999)
        runCurrent()
        assertFalse(pending.isCompleted)
        advanceTimeBy(1)
        runCurrent()
        assertTrue(pending.isCompleted)
        assertFalse(pending.await())
        reply(allowed)
        assertFalse(pending.await())
    }

    @Test
    fun `caller cancellation cannot be undone by a reply`() = runTest {
        lateinit var reply: (String?) -> Unit
        val pending = async(start = CoroutineStart.UNDISPATCHED) {
            ManagedServiceGate { _, callback -> reply = callback }.allowed()
        }
        pending.cancelAndJoin()
        reply(allowed)
        assertTrue(pending.isCancelled)
    }
}
