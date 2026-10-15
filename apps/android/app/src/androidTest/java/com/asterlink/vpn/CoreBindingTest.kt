package com.asterlink.vpn

import android.net.ConnectivityManager
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.asterlink.core.mobile.Mobile
import com.asterlink.vpn.core.CoreBridge
import com.asterlink.vpn.core.CoreProtocol
import com.asterlink.vpn.core.CoreResponse
import com.asterlink.vpn.vpn.AndroidPlatform
import com.asterlink.vpn.vpn.NetworkMonitor
import com.asterlink.vpn.vpn.VpnHostRegistry
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The real gomobile AAR (Go Core + pinned Mihomo) inside the app process on an emulator.
 * One test method, because the order matters: the Core is not running at first, then it is
 * started, then stopped. The shared fixtures come from shared/contracts (androidTest assets).
 */
@RunWith(AndroidJUnit4::class)
class CoreBindingTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val testAssets = InstrumentationRegistry.getInstrumentation().context.assets

    private fun call(request: String): CoreResponse =
        CoreProtocol.json.decodeFromString(CoreResponse.serializer(), Mobile.call(request))

    @Test
    fun theBoundCoreAnswersLikeThePipeCoreAndStartsAndStops() {
        Mobile.stop()

        // 1. Every shared ipc-v1 case is decided before any session handler is reached, except
        //    the one that names a session command; that one says the Core is not running yet.
        val fixture = Json.parseToJsonElement(testAssets.open("ipc-v1.json").bufferedReader().readText()).jsonObject
        val cases = fixture.getValue("cases").jsonArray
        assertTrue(cases.size >= 8)
        for (case in cases) {
            val c = case.jsonObject
            val padding = c["request_padding_bytes"]?.jsonPrimitive?.int ?: 0
            val request = c.getValue("request").jsonPrimitive.content.replace("%PAD%", "a".repeat(padding))
            val expect = c.getValue("expect").jsonObject
            val response = call(request)
            val name = c.getValue("name").jsonPrimitive.content
            assertEquals(name, expect.getValue("ok").jsonPrimitive.content.toBoolean(), response.ok)
            assertEquals(name, expect.getValue("request_id").jsonPrimitive.content, response.requestId)
            assertEquals(name, expect["generation"]?.jsonPrimitive?.long ?: 0L, response.generation)
            val code = expect["error_code"]?.jsonPrimitive?.content
            val wanted = if (code == "not_implemented") CoreProtocol.CODE_NOT_STARTED else code
            assertEquals(name, wanted, response.error?.code)
        }

        // 2. Started: the build identity is this APK's, and the managed session is signed out.
        val connectivity = context.getSystemService(ConnectivityManager::class.java)
        val monitor = NetworkMonitor(connectivity) {}.also { it.start() }
        // No VpnService runs here: protect refuses and openTun returns -1.
        val core = CoreBridge(AndroidPlatform(VpnHostRegistry(), monitor), context.cacheDir)
        core.start()
        try {
            val hello = call("""{"request_id":"h","protocol_version":1,"generation":0,"command":"hello"}""")
            assertTrue(hello.ok)
            val identity = hello.result!!.jsonObject
            assertEquals(BuildConfig.VERSION_NAME, identity.getValue("core_version").jsonPrimitive.content)
            assertEquals(40, identity.getValue("mihomo_revision").jsonPrimitive.content.length)

            val snapshot = call("""{"request_id":"s","protocol_version":1,"generation":0,"command":"read_snapshot"}""")
            assertTrue(snapshot.ok)
            assertEquals("signed_out", snapshot.result!!.jsonObject.getValue("state").jsonPrimitive.content)

            // The default network report the Core will rely on decodes to a real link.
            val network = monitor.current()
            assertTrue("emulator has a default network", network.available)
            assertFalse(network.interfaceName.isNullOrEmpty())
            assertTrue("interface index ${network.index}", (network.index ?: 0) > 0)
        } finally {
            core.stop()
        }

        // 3. Stopped: session commands are refused again.
        val after = call("""{"request_id":"a","protocol_version":1,"generation":0,"command":"read_snapshot"}""")
        assertEquals(CoreProtocol.CODE_NOT_STARTED, after.error?.code)
    }
}
