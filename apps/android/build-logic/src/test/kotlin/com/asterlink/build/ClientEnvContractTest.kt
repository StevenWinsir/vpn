package com.asterlink.build

import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/** The Gradle parser accepts and rejects exactly what scripts/native_env.py does. */
class ClientEnvContractTest {
    private val contracts = File(requireNotNull(System.getProperty("asterlink.contracts")) { "asterlink.contracts is set by the Gradle test task" })

    @Test
    fun everySharedCaseAgrees() {
        val root = Json.parseToJsonElement(contracts.resolve("env-v1.json").readText()).jsonObject
        val cases = root.getValue("cases").jsonArray
        assertTrue("fixture has ${cases.size} cases", cases.size >= 16)
        for (case in cases) {
            val name = case.jsonObject.getValue("name").jsonPrimitive.content
            val env = case.jsonObject.getValue("env").jsonPrimitive.content
            val ok = case.jsonObject.getValue("ok").jsonPrimitive.boolean
            val accepted = try {
                ClientEnv.parse(env)
                true
            } catch (error: ClientEnv.Invalid) {
                false
            }
            assertEquals("case '$name'", ok, accepted)
        }
    }

    @Test
    fun theCommittedExampleIsValidAndPointsAtTheDemoServers() {
        val example = ClientEnv.load(contracts.resolve("../../apps/android/.env.example").canonicalFile)
        assertEquals("development", example.channel)
        assertEquals("https://demo.hyshentou.cn/api/v1/client", example.apiBaseUrl)
        assertEquals("https://test.hyshentou.cn", example.websiteBaseUrl)
    }

    @Test
    fun edgeCasesTheFixtureDoesNotSpellOut() {
        for (bad in listOf(
            "APP_ENV=development\nAPI_BASE_URL=https://demo.hyshentou.cn:99999/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn\n",
            "APP_ENV=development\nAPI_BASE_URL=https://demo.hyshentou.cn/api/v1/client#x\nWEBSITE_BASE_URL=https://test.hyshentou.cn\n",
            "APP_ENV=development\nAPI_BASE_URL=https://demo.hyshentou.cn/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn/buy\n",
            "APP_ENV=development\nAPI_BASE_URL=ftp://demo.hyshentou.cn/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn\n",
            "APP_ENV=development\nAPI_BASE_URL= https://demo.hyshentou.cn/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn\n",
        )) {
            try {
                ClientEnv.parse(bad)
                fail("accepted: $bad")
            } catch (expected: ClientEnv.Invalid) {
            }
        }
        // IPv6 loopback over http is a development convenience, like 127.0.0.1.
        ClientEnv.parse("APP_ENV=development\nAPI_BASE_URL=http://[::1]:8080/api/v1/client\nWEBSITE_BASE_URL=http://localhost:3000\n")
    }
}
