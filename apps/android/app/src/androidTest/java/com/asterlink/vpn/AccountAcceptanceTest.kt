package com.asterlink.vpn

import android.os.SystemClock
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.core.LoginTarget
import com.asterlink.vpn.security.AppPreferences
import com.asterlink.vpn.security.FileDeviceIdentity
import com.asterlink.vpn.security.KeystoreCredentialStore
import com.asterlink.vpn.session.AppPhase
import com.asterlink.vpn.session.CoreLifecycle
import com.asterlink.vpn.session.CredentialAccount
import com.asterlink.vpn.session.Diagnostics
import com.asterlink.vpn.session.SessionController
import com.asterlink.vpn.session.SessionState
import com.asterlink.vpn.session.TunnelControl
import com.asterlink.vpn.session.TunnelOutcome
import java.io.File
import java.net.InetSocketAddress
import java.net.Socket
import java.net.URI
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Round-3 account acceptance (handoff_app_android.md A01-A04) on the device: the app's real
 * SessionController, the real Go Core in this process, the Android Keystore credential store,
 * and the real Gin/PostgreSQL backend that scripts/test-native-android.py --suite account
 * starts on the host (backend TestAndroidAccountWorld, reached through `adb reverse`).
 *
 * Only runs when the suite has written files/accounttest/world.json (a disposable world:
 * synthetic accounts, a private database); otherwise it is skipped, so the emulator suite's
 * connectedDebugAndroidTest is unaffected. Results go to files/accounttest/report.json, one
 * entry per case, with measured times. The VPN is not used (round 4 connects).
 */
@RunWith(AndroidJUnit4::class)
class AccountAcceptanceTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val container = (context.applicationContext as AsterApp).container
    private val dir = File(context.filesDir, "accounttest")
    private val cases = mutableListOf<JsonObject>()

    private lateinit var apiBase: String
    private lateinit var password: String
    private lateinit var emails: Map<String, String>
    private lateinit var nodeIds: Map<String, String>
    private lateinit var stores: CoroutineScope

    @Test
    fun acceptance() {
        // Outside the coroutine: an assumption thrown from inside runBlocking is reported by
        // the connected-test runner as a failure, not a skip.
        val worldFile = File(dir, "world.json")
        assumeTrue("run through scripts/test-native-android.py --suite account", worldFile.isFile)
        runBlocking { runAcceptance(worldFile) }
    }

    private suspend fun runAcceptance(worldFile: File) {
        val world = Json.parseToJsonElement(worldFile.readText()).jsonObject
        apiBase = world.getValue("api_base").jsonPrimitive.content
        password = world.getValue("password").jsonPrimitive.content
        emails = world.getValue("emails").jsonObject.mapValues { it.value.jsonPrimitive.content }
        nodeIds = world.getValue("nodes").jsonObject.mapValues { it.value.jsonPrimitive.content }
        // The world's password is test-only, but it still never stays on the device.
        worldFile.delete()
        stores = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        try {
            runCases()
        } catch (error: Throwable) {
            record("A.unexpected", false, "${error.javaClass.simpleName}: ${error.message?.take(200)}")
        } finally {
            container.core.stop()
            stores.cancel()
            File(dir, "report.json").writeText(JsonObject(mapOf("cases" to JsonArray(cases))).toString())
        }
    }

    // ---------------------------------------------------------------- rig

    private val credentialData by lazy { PreferenceDataStoreFactory.create(scope = stores) { File(dir, "credentials.preferences_pb") } }
    private val settingsData by lazy { PreferenceDataStoreFactory.create(scope = stores) { File(dir, "settings.preferences_pb") } }
    private val credentials by lazy { KeystoreCredentialStore(credentialData) }
    private val preferences by lazy { AppPreferences(settingsData) }
    private val devices by lazy { FileDeviceIdentity(dir) }

    /** No VPN in round 3: a connect is refused before anything starts. */
    private val noTunnel = object : TunnelControl {
        override suspend fun connect(generation: Long) = TunnelOutcome.Refused("vpn_start_failed")
        override val running = false
        override fun stop() = Unit
    }

    private var scope: CoroutineScope? = null

    /** A fresh controller over a fresh Core: what a process restart looks like. */
    private fun newController(): SessionController {
        scope?.cancel()
        container.core.stop()
        val main = MainScope().also { scope = it }
        return SessionController(
            client = container.coreClient,
            core = object : CoreLifecycle {
                override suspend fun start() = withContext(Dispatchers.IO) { container.core.start() }
                override suspend fun stop() = withContext(Dispatchers.IO) { container.core.stop() }
            },
            target = LoginTarget(apiBase, "development", BuildConfig.VERSION_NAME),
            credentials = credentials,
            preferences = preferences,
            devices = devices,
            tunnel = noTunnel,
            scope = main,
            clock = SystemClock::elapsedRealtime,
        )
    }

    private suspend fun main(block: suspend () -> Unit) = withContext(Dispatchers.Main) { block() }

    /** Milliseconds until [predicate] holds for the controller's state, or null on timeout. */
    private suspend fun waitFor(controller: SessionController, millis: Long, predicate: (SessionState) -> Boolean): Long? {
        val start = SystemClock.elapsedRealtime()
        while (SystemClock.elapsedRealtime() - start < millis) {
            if (predicate(controller.state.value)) return SystemClock.elapsedRealtime() - start
            delay(50)
        }
        return null
    }

    private fun record(id: String, ok: Boolean, observed: String) {
        cases += JsonObject(mapOf("case_id" to JsonPrimitive(id), "status" to JsonPrimitive(if (ok) "PASS" else "FAIL"), "observed" to JsonPrimitive(observed)))
    }

    private fun key(who: String) = CredentialAccount.key(apiBase, emails.getValue(who))

    /** The world's loopback control calls. A raw socket: the app's network policy (rightly) forbids cleartext HTTP. */
    private fun world(action: String, body: String) {
        val uri = URI(apiBase)
        Socket().use { socket ->
            socket.connect(InetSocketAddress("127.0.0.1", uri.port), 5_000)
            socket.soTimeout = 15_000
            val bytes = body.toByteArray()
            socket.getOutputStream().write(
                ("POST /__world/$action HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\nConnection: close\r\n\r\n").toByteArray() + bytes,
            )
            val answer = socket.getInputStream().bufferedReader().readText()
            check(answer.lineSequence().first().contains(" 204")) { "world $action: " + answer.substringAfter("\r\n\r\n").take(300) }
        }
    }

    // ---------------------------------------------------------------- cases

    private suspend fun runCases() {
        val vip = emails.getValue("vip")

        // A01: a wrong password is refused online and a stale saved one is forgotten.
        var c = newController()
        credentials.save(key("vip"), "stale-password")
        main { c.launchNow(); c.signInNow(vip, "definitely-wrong", remember = true, automatic = false) }
        record(
            "A01.wrong_password",
            c.state.value.phase == AppPhase.SignedOut && c.state.value.message?.code == "invalid_credentials" && credentials.read(key("vip")) == null,
            "phase ${c.state.value.phase}, message ${c.state.value.message?.code}, stale password deleted ${credentials.read(key("vip")) == null}",
        )

        // A01: sign in and remember: Keystore entry under API base + email, email prefilled.
        main { c.signInNow(vip, password, remember = true, automatic = false) }
        val signedIn = c.state.value
        record(
            "A01.sign_in_remember",
            signedIn.phase == AppPhase.SignedIn && credentials.read(key("vip")) == password && preferences.rememberedEmail() == vip && signedIn.snapshot.user?.email == vip,
            "phase ${signedIn.phase}, state ${signedIn.snapshot.state}, plan ${signedIn.snapshot.entitlement?.planName}, ${signedIn.snapshot.nodes.size} lines",
        )
        val catalog = waitFor(c, 8_000) { it.snapshot.nodes.size == 2 }
        record("A01.lines_after_login", catalog != null, "VIP sees dedicated + ordinary lines after ${catalog} ms")

        // Nothing secret at rest: no password or token in any file the app wrote outside the Keystore.
        val leaks = context.filesDir.parentFile!!.walkTopDown().filter { it.isFile && it.length() < 4_000_000 }.filter { file ->
            val text = file.readBytes().toString(Charsets.ISO_8859_1)
            text.contains(password) || text.contains("Bearer ")
        }.map { it.name }.toList()
        record("A01.nothing_in_clear", leaks.isEmpty(), if (leaks.isEmpty()) "no file under the app's data directory holds the password or a bearer token" else "found in $leaks")

        // A01: a "restart" signs in online with the remembered password (no stored token).
        c = newController()
        main { c.launchNow() }
        val relaunched = c.state.value
        record(
            "A01.relaunch_online",
            relaunched.phase == AppPhase.SignedIn && relaunched.rememberPassword && relaunched.snapshot.user?.email == vip,
            "a fresh Core + controller: phase ${relaunched.phase}, signed in online as the remembered account",
        )

        // A01: explicit sign-out revokes and forgets the password and the email.
        main { c.signOutNow() }
        record(
            "A01.sign_out_forgets",
            c.state.value.phase == AppPhase.SignedOut && credentials.read(key("vip")) == null && preferences.rememberedEmail() == null,
            "after sign-out: saved password ${credentials.read(key("vip")) != null}, remembered email ${preferences.rememberedEmail()}",
        )

        // A01: without "remember" nothing is stored and the next launch asks for the password.
        main { c.signInNow(vip, password, remember = false, automatic = false) }
        val notSaved = credentials.read(key("vip")) == null
        main { c.signOutNow() }
        c = newController()
        main { c.launchNow() }
        record("A01.no_remember", notSaved && c.state.value.phase == AppPhase.SignedOut, "nothing saved; relaunch waits on the login screen (${c.state.value.phase})")

        // A02: an administrator without a plan is not let through because of the role.
        main { c.signInNow(emails.getValue("admin"), password, remember = false, automatic = false) }
        waitFor(c, 8_000) { it.snapshot.state == CoreSnapshot.State.BLOCKED }
        var s = c.state.value
        record(
            "A02.admin_without_plan",
            s.snapshot.user?.role == "admin" && !s.connection.canConnect && s.connection.needsPurchase && s.snapshot.nodes.isEmpty() && s.snapshot.entitlement?.reason == "upgrade_required",
            "role ${s.snapshot.user?.role}, state ${s.snapshot.state}, reason ${s.snapshot.entitlement?.reason}, ${s.snapshot.nodes.size} lines, connect enabled ${s.connection.canConnect}",
        )
        main { c.signOutNow() }

        // A02: a used-up plan.
        main { c.signInNow(emails.getValue("broke"), password, remember = false, automatic = false) }
        waitFor(c, 8_000) { it.snapshot.entitlement?.reason == "quota_exhausted" }
        s = c.state.value
        record(
            "A02.zero_quota",
            s.snapshot.entitlement?.reason == "quota_exhausted" && !s.connection.canConnect && s.connection.needsPurchase && s.snapshot.nodes.isEmpty(),
            "reason ${s.snapshot.entitlement?.reason}, remaining ${s.snapshot.entitlement?.remainingBytes} B, ${s.snapshot.nodes.size} lines",
        )
        main { c.signOutNow() }

        // A02 + A04: a plan that ends while the app is open.
        main { c.signInNow(emails.getValue("expiring"), password, remember = false, automatic = false) }
        waitFor(c, 8_000) { it.snapshot.nodes.isNotEmpty() }
        main { c.selectNow(nodeIds.getValue("dedicated")) }
        val selectedBefore = c.state.value.snapshot.configReady
        world("expire", """{"user":"expiring"}""")
        val expiredAfter = waitFor(c, 15_000) { it.snapshot.entitlement?.accountAuthorized == false && it.snapshot.nodes.isEmpty() }
        s = c.state.value
        record(
            "A02.expiry_while_open",
            selectedBefore && expiredAfter != null && !s.connection.canConnect,
            "line selected first; plan ended on the server, reflected after ${expiredAfter} ms (reason ${s.snapshot.entitlement?.reason}, ${s.snapshot.nodes.size} lines)",
        )
        main { c.signOutNow() }

        // A04: the standard plan sees only the ordinary line (the dedicated one is not in its plan).
        main { c.signInNow(emails.getValue("basic"), password, remember = false, automatic = false) }
        waitFor(c, 8_000) { it.snapshot.nodes.isNotEmpty() }
        s = c.state.value
        record(
            "A04.plan_filter",
            s.snapshot.nodes.map { it.id } == listOf(nodeIds.getValue("ordinary")),
            "basic plan lines: ${s.snapshot.nodes.map { "${it.lineType}/${it.ratePermille}" }}",
        )
        main { c.selectNow(nodeIds.getValue("ordinary")) }

        // A04: rename + new rate made by the administrator.
        world("edit", """{"node":"ordinary","name":"JP-Renamed-By-Admin","rate_permille":1500}""")
        val renamed = waitFor(c, 15_000) { st -> st.snapshot.nodes.any { it.name == "JP-Renamed-By-Admin" && it.ratePermille == 1500L } }
        s = c.state.value
        record(
            "A04.rename_and_rate",
            renamed != null && renamed <= 7_000 && !s.snapshot.configReady,
            "rename + 1.5x seen after ${renamed} ms; the bound config was dropped (config_ready ${s.snapshot.configReady}, last_error ${s.snapshot.lastError?.code})",
        )

        // A04: disabled, then enabled again, then deleted.
        main { c.selectNow(nodeIds.getValue("ordinary")) }
        world("edit", """{"node":"ordinary","enabled":false}""")
        val disabled = waitFor(c, 15_000) { it.snapshot.nodes.isEmpty() }
        s = c.state.value
        record("A04.disable", disabled != null && disabled <= 7_000 && s.snapshot.selectedNodeId.isEmpty(), "disabled line gone after ${disabled} ms, selection cleared ${s.snapshot.selectedNodeId.isEmpty()}")
        world("edit", """{"node":"ordinary","enabled":true}""")
        val enabled = waitFor(c, 15_000) { it.snapshot.nodes.size == 1 }
        record("A04.enable", enabled != null && enabled <= 7_000, "re-enabled line back after ${enabled} ms")
        world("delete", """{"node":"ordinary"}""")
        val deleted = waitFor(c, 15_000) { it.snapshot.nodes.isEmpty() }
        record("A04.delete", deleted != null && deleted <= 7_000, "deleted line gone after ${deleted} ms")

        // Redaction: what the app could show or copy carries no node secret or account detail.
        val diagnostics = Diagnostics.build("0", "development", "127.0.0.1", "test", "0", "0", c.state.value.snapshot)
        val text = c.state.value.toString() + diagnostics
        val secrets = listOf("metadata-private.example.invalid", "never-reaches-the-ui", "aes-256-gcm", password)
        record("A.redaction", secrets.none { text.contains(it) }, "UI state and diagnostics contain no node address, credential, cipher or password")
        main { c.signOutNow() }

        // A02: the plan's only device slot is taken by another device: binding a line says so.
        world("occupy", """{"user":"vip","node":"dedicated"}""")
        main { c.signInNow(vip, password, remember = false, automatic = false) }
        waitFor(c, 8_000) { it.snapshot.nodes.isNotEmpty() }
        main { c.selectNow(nodeIds.getValue("dedicated")) }
        s = c.state.value
        record("A02.device_limit", s.message?.code == "device_limit" && !s.snapshot.configReady, "second device: ${s.message?.code} — ${s.message?.text}")
        main { c.signOutNow() }
        scope?.cancel()
    }
}
