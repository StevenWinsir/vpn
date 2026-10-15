package com.asterlink.vpn.core

import com.asterlink.vpn.vpn.HostNetwork
import java.io.File
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class CoreProtocolTest {
    private val contracts = File(requireNotNull(System.getProperty("asterlink.contracts")) { "asterlink.contracts is set by the Gradle test task" })

    @Test
    fun requestsHaveExactlyTheContractFields() {
        val encoded = CoreProtocol.json.encodeToString(CoreRequest.serializer(), CoreRequest("r1", generation = 7, command = "hello"))
        assertEquals("""{"request_id":"r1","protocol_version":1,"generation":7,"command":"hello"}""", encoded)
        // The first shared fixture case is this exact request.
        val fixture = Json.parseToJsonElement(contracts.resolve("ipc-v1.json").readText()).jsonObject
        assertEquals(CoreProtocol.VERSION, fixture.getValue("protocol_version").jsonPrimitive.int)
        val first = fixture.getValue("cases").jsonArray[0].jsonObject.getValue("request").jsonPrimitive.content
        assertEquals(Json.parseToJsonElement(first), Json.parseToJsonElement(encoded))
    }

    @Test
    fun everyFixtureRequestIsAValidRequestDocumentOrAKnownRefusal() {
        val cases = Json.parseToJsonElement(contracts.resolve("ipc-v1.json").readText()).jsonObject.getValue("cases").jsonArray
        assertTrue(cases.size >= 8)
        for (case in cases) {
            val request = case.jsonObject.getValue("request").jsonPrimitive.content.replace("%PAD%", "a")
            val expect = case.jsonObject.getValue("expect").jsonObject
            val decoded = runCatching { CoreProtocol.json.decodeFromString(CoreRequest.serializer(), request) }
            // Kotlin is as strict as the Go decoder about unknown fields and trailing data.
            if (expect["error_code"]?.jsonPrimitive?.content == CoreProtocol.CODE_INVALID_REQUEST) {
                assertTrue("Kotlin accepted what the Core refuses: $request", decoded.isFailure)
            }
        }
    }

    @Test
    fun countersAbove2pow53StayExact() {
        val big = 9_007_199_254_740_993L // 2^53 + 1: a Double would round it
        val response = CoreProtocol.json.decodeFromString(
            CoreResponse.serializer(),
            """{"request_id":"a","protocol_version":1,"generation":$big,"ok":true,"result":{"used_units":$big}}""",
        )
        assertEquals(big, response.generation)
        assertEquals(big, response.result!!.jsonObject.getValue("used_units").jsonPrimitive.long)
    }

    @Test(expected = SerializationException::class)
    fun unknownResponseFieldsAreRejected() {
        CoreProtocol.json.decodeFromString(CoreResponse.serializer(), """{"request_id":"a","protocol_version":1,"generation":0,"ok":true,"debug":"x"}""")
    }

    private class Scripted(val answer: (CoreRequest) -> String) : CoreTransport {
        var calls = 0
        override fun call(requestJson: String): String {
            calls++
            return answer(CoreProtocol.json.decodeFromString(CoreRequest.serializer(), requestJson))
        }
    }

    @Test
    fun helloDecodesTheCoreIdentity() = runTest {
        val transport = Scripted { r ->
            """{"request_id":"${r.requestId}","protocol_version":1,"generation":0,"ok":true,"result":{"component":"asterlink-core","core_version":"0.1.0","protocol_version":1,"min_protocol_version":1,"max_protocol_version":1,"mihomo_revision":"70f0570405c3c2c47bb113b88db95006d239b346"}}"""
        }
        val hello = CoreClient(transport, StandardTestDispatcher(testScheduler)).hello()
        assertEquals("0.1.0", hello.coreVersion)
        assertEquals("70f0570405c3c2c47bb113b88db95006d239b346", hello.mihomoRevision)
        assertEquals(1, transport.calls)
    }

    @Test
    fun refusalsSurfaceTheStableCode() = runTest {
        val transport = Scripted { r ->
            """{"request_id":"${r.requestId}","protocol_version":1,"generation":0,"ok":false,"error":{"code":"core_not_started","message":"core is not running"}}"""
        }
        try {
            CoreClient(transport, StandardTestDispatcher(testScheduler)).hello()
            fail("expected a refusal")
        } catch (error: CoreException) {
            assertEquals(CoreProtocol.CODE_NOT_STARTED, error.code)
        }
    }

    @Test
    fun aResponseToAnotherRequestIsNeverApplied() = runTest {
        val transport = Scripted { _ ->
            """{"request_id":"someone-else","protocol_version":1,"generation":0,"ok":true,"result":{"component":"x","core_version":"x","protocol_version":1,"min_protocol_version":1,"max_protocol_version":1,"mihomo_revision":"x"}}"""
        }
        try {
            CoreClient(transport, StandardTestDispatcher(testScheduler)).hello()
            fail("a mismatched request_id was accepted")
        } catch (error: CoreException) {
            assertEquals(CoreProtocol.CODE_INVALID_REQUEST, error.code)
        }
    }

    @Test
    fun hostNetworkUsesOnlyTheFieldsTheCoreAccepts() {
        assertEquals("""{"available":false}""", CoreProtocol.json.encodeToString(HostNetwork.serializer(), HostNetwork.NONE))
        val wifi = HostNetwork(true, "wlan0", 30, listOf("10.0.2.16"), listOf("10.0.2.3"), false)
        val keys = Json.parseToJsonElement(CoreProtocol.json.encodeToString(HostNetwork.serializer(), wifi)).jsonObject.keys
        // native/core/internal/network/host.go HostSnapshot
        assertEquals(setOf("available", "interface", "index", "addresses", "dns", "metered"), keys)
        assertNull(HostNetwork.NONE.interfaceName)
    }
}
