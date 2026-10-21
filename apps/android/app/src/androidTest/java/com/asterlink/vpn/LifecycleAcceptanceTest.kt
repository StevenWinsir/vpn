package com.asterlink.vpn

import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.Bundle
import android.os.ParcelFileDescriptor
import android.os.SystemClock
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.asterlink.vpn.core.CoreSnapshot.State
import com.asterlink.vpn.core.GateRecord
import com.asterlink.vpn.core.LoginTarget
import com.asterlink.vpn.security.FileDeviceIdentity
import com.asterlink.vpn.session.CoreLifecycle
import com.asterlink.vpn.session.CredentialStore
import com.asterlink.vpn.session.LocalCodes
import com.asterlink.vpn.session.LoginPreferences
import com.asterlink.vpn.session.Notice
import com.asterlink.vpn.session.SessionAlerts
import com.asterlink.vpn.session.SessionController
import com.asterlink.vpn.session.SessionState
import com.asterlink.vpn.vpn.VpnNotifications
import java.io.File
import java.io.InputStream
import java.net.InetSocketAddress
import java.net.Socket
import java.net.URI
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Round-5 lifecycle and reliability acceptance (handoff_app_android.md L01-L07) on the device,
 * through everything that ships: the SessionController, AsterVpnService, the Go Core with the
 * Android resume policy (the authorization gate of 7.5) and the real backend of the billing
 * world (TestAndroidBillingWorld through `adb reverse`; scripts/test-native-android.py --suite
 * lifecycle). The device is changed the way a phone changes, with shell commands run through
 * UiAutomation (screen off, Doze, Wi-Fi, airplane mode, a second VPN app); every one is undone
 * in `finally`. A wake-up after the CPU was not scheduled is produced by stopping this very
 * process (SIGSTOP from a child process, SIGCONT after the pause): its clocks then jump, which
 * is what the Core's sleep detector sees on a phone. The host adds what kills this process
 * (force-stop, reboot) after the run.
 *
 * Only runs when the suite has written files/lifecycletest/world.json. Results go to
 * files/lifecycletest/report.json (and soak samples to samples.json).
 */
@SuiteDriver
@RunWith(AndroidJUnit4::class)
class LifecycleAcceptanceTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context = instrumentation.targetContext
    private val container = (context.applicationContext as AsterApp).container
    private val dir = File(context.filesDir, "lifecycletest")
    private val cases = mutableListOf<JsonObject>()
    private val alerts = mutableListOf<Notice>()

    private lateinit var world: JsonObject
    private lateinit var config: JsonObject
    private lateinit var apiBase: String
    private lateinit var password: String
    private lateinit var email: String
    private lateinit var nodeIds: Map<String, String>

    private var scope: CoroutineScope? = null
    private var controller: SessionController? = null
    private val timeline = mutableListOf<JsonObject>()
    private val origin = SystemClock.elapsedRealtime()

    @Test
    fun acceptance() {
        val worldFile = File(dir, "world.json")
        assumeTrue("run through scripts/test-native-android.py --suite lifecycle", worldFile.isFile && !File(dir, "hold").exists())
        runBlocking {
            load(worldFile)
            try {
                runCases()
            } catch (error: Throwable) {
                record("L.unexpected", "FAIL", "${error.javaClass.simpleName}: ${error.message?.take(300)}")
            } finally {
                restoreDevice()
                controller?.let { c -> runCatching { main { c.signOutNow() } } }
                scope?.cancel()
                container.core.stop()
                container.sessionState = container.session.state
                container.onVpnRevoked = { container.session.vpnRevokedNow() }
                File(dir, "report.json").writeText(JsonObject(mapOf("cases" to JsonArray(cases))).toString())
                File(dir, "timeline.json").writeText(synchronized(timeline) { JsonArray(timeline.toList()) }.toString())
            }
        }
    }

    /** L05 on the host: connect, say so, and wait to be killed (force-stop) by the suite. */
    @Test
    fun holdConnected() {
        val worldFile = File(dir, "world.json")
        assumeTrue(worldFile.isFile && File(dir, "hold").exists())
        runBlocking {
            load(worldFile)
            val c = signIn()
            main { c.selectNow(nodeIds.getValue("dedicated")) }
            main { c.connectNow() }
            val up = c.state.value.snapshot.state == State.CONNECTED && vpnNetworkUp()
            status("life_ready", if (up) "1" else "0")
            status("life_pid", android.os.Process.myPid().toString())
            delay(180_000)
        }
    }

    private fun load(worldFile: File) {
        world = Json.parseToJsonElement(worldFile.readText()).jsonObject
        config = Json.parseToJsonElement(File(dir, "config.json").readText()).jsonObject
        apiBase = world.str("api_base")
        password = world.str("password")
        email = world.getValue("emails").jsonObject.getValue("life").jsonPrimitive.content
        nodeIds = world.getValue("nodes").jsonObject.mapValues { it.value.jsonPrimitive.content }
        // The world's password is test-only, but it still never stays on the device.
        worldFile.delete()
    }

    // ---------------------------------------------------------------- rig

    private fun JsonObject.str(key: String) = getValue(key).jsonPrimitive.content

    private val memoryCredentials = object : CredentialStore {
        override suspend fun save(account: String, password: String) = Unit
        override suspend fun read(account: String): String? = null
        override suspend fun delete(account: String) = Unit
    }
    private val memoryPreferences = object : LoginPreferences {
        override suspend fun rememberedEmail(): String? = null
        override suspend fun setRememberedEmail(email: String?) = Unit
    }

    /** Records alerts instead of posting them, so the cases can say exactly what the user would see. */
    private val recordingAlerts = object : SessionAlerts {
        override fun tunnelStopped(reason: Notice) {
            synchronized(alerts) { alerts += reason }
        }

        override fun clear() = Unit
    }

    private suspend fun signIn(): SessionController {
        scope?.cancel()
        val main = MainScope().also { scope = it }
        val c = SessionController(
            client = container.coreClient,
            core = object : CoreLifecycle {
                override suspend fun start() = withContext(Dispatchers.IO) { container.core.start() }
                override suspend fun stop() = withContext(Dispatchers.IO) { container.core.stop() }
            },
            target = LoginTarget(apiBase, "development", BuildConfig.VERSION_NAME),
            credentials = memoryCredentials,
            preferences = memoryPreferences,
            devices = FileDeviceIdentity(dir),
            tunnel = container.tunnelLauncher,
            scope = main,
            clock = SystemClock::elapsedRealtime,
            alerts = recordingAlerts,
        )
        container.sessionState = c.state
        container.onVpnRevoked = { c.vpnRevokedNow() }
        controller = c
        // Evidence: every change of state, gate and stop reason, with its time.
        main.launch {
            var last = ""
            c.state.collect { st ->
                val s = st.snapshot
                val key = "${s.state}|${s.tunnelHealth}|${s.gate?.reason}|${s.gate?.waitingNetwork}|${s.lastError?.code}|${s.lastGate?.totalClosures}|${st.message?.code}"
                if (key != last) {
                    last = key
                    synchronized(timeline) {
                        timeline += JsonObject(mapOf(
                            "t_ms" to JsonPrimitive(SystemClock.elapsedRealtime() - origin), "state" to JsonPrimitive(s.state),
                            "health" to JsonPrimitive(s.tunnelHealth), "gate" to JsonPrimitive(s.gate?.let { "${it.reason}${if (it.waitingNetwork) " (waiting)" else ""}" }),
                            "last_error" to JsonPrimitive(s.lastError?.code), "gates" to JsonPrimitive(s.lastGate?.totalClosures ?: 0),
                            "message" to JsonPrimitive(st.message?.code),
                        ))
                    }
                }
            }
        }
        main { c.launchNow(); c.signInNow(email, password, remember = false, automatic = false) }
        waitFor(c, 10_000) { it.snapshot.nodes.isNotEmpty() }
        return c
    }

    private suspend fun main(block: suspend () -> Unit) = withContext(Dispatchers.Main) { block() }

    private suspend fun waitFor(c: SessionController, millis: Long, predicate: (SessionState) -> Boolean): Long? {
        val start = SystemClock.elapsedRealtime()
        while (SystemClock.elapsedRealtime() - start < millis) {
            if (predicate(c.state.value)) return SystemClock.elapsedRealtime() - start
            delay(50)
        }
        return null
    }

    private fun record(id: String, status: String, observed: String) {
        cases += JsonObject(mapOf("case_id" to JsonPrimitive(id), "status" to JsonPrimitive(status), "observed" to JsonPrimitive(observed)))
    }

    private fun verdict(id: String, problems: List<String>, passText: String) =
        record(id, if (problems.isEmpty()) "PASS" else "FAIL", if (problems.isEmpty()) passText else problems.joinToString("; ") + " | " + passText)

    private fun status(key: String, value: String) {
        instrumentation.sendStatus(0, Bundle().apply { putString("asterlink", "$key=$value") })
    }

    /** A shell command with the shell user's rights (svc, cmd, dumpsys, input, am, appops). */
    private fun shell(command: String): String {
        val pfd = instrumentation.uiAutomation.executeShellCommand(command)
        return ParcelFileDescriptor.AutoCloseInputStream(pfd).use { it.readBytes().toString(Charsets.UTF_8) }.trim()
    }

    /** The world's loopback control calls (a raw socket: the app's network policy forbids cleartext HTTP). */
    private fun world(action: String, body: String = "{}"): String {
        val uri = URI(apiBase)
        Socket().use { socket ->
            socket.connect(InetSocketAddress("127.0.0.1", uri.port), 5_000)
            socket.soTimeout = 20_000
            val bytes = body.toByteArray()
            socket.getOutputStream().write(
                ("POST /__world/$action HTTP/1.0\r\nHost: 127.0.0.1\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\n\r\n").toByteArray() + bytes,
            )
            val answer = socket.getInputStream().bufferedReader().readText()
            val status = answer.lineSequence().first()
            check(status.contains(" 200") || status.contains(" 204")) { "world $action: " + answer.substringAfter("\r\n\r\n").take(300) }
            return answer.substringAfter("\r\n\r\n")
        }
    }

    private fun hits(): Map<String, Int> =
        Json.parseToJsonElement(world("hits")).jsonObject.mapValues { it.value.jsonPrimitive.int }

    private fun sinkDown(): Long = Json.parseToJsonElement(world("sink")).jsonObject.getValue("down_sent").jsonPrimitive.long

    private val connectivity: ConnectivityManager get() = context.getSystemService(ConnectivityManager::class.java)

    @Suppress("DEPRECATION")
    private fun vpnNetworkUp(): Boolean = connectivity.allNetworks.any { connectivity.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true }

    /** Milliseconds until the system has unregistered every VPN network, or null after [millis]. */
    private suspend fun vpnGone(millis: Long = 10_000): Long? {
        val start = SystemClock.elapsedRealtime()
        while (SystemClock.elapsedRealtime() - start < millis) {
            if (!vpnNetworkUp()) return SystemClock.elapsedRealtime() - start
            delay(50)
        }
        return null
    }

    private val sink get() = config.getValue("sink").jsonObject

    /** Downloads [bytes] from the sink by name through the tunnel; the body length read, or the failure. */
    private fun download(bytes: Long, timeoutMillis: Int = 20_000): Result<Long> = runCatching {
        val host = sink.str("name")
        Socket().use { socket ->
            socket.connect(InetSocketAddress(host, sink.str("port").toInt()), timeoutMillis)
            socket.soTimeout = timeoutMillis
            socket.getOutputStream().write("GET /down?t=${sink.str("token")}&bytes=$bytes HTTP/1.1\r\nHost: $host\r\nConnection: close\r\n\r\n".toByteArray())
            val input: InputStream = socket.getInputStream().buffered()
            val head = StringBuilder()
            while (!head.endsWith("\r\n\r\n")) {
                val b = input.read()
                check(b >= 0) { "connection closed before the response" }
                head.append(b.toChar())
            }
            check(head.startsWith("HTTP/1.1 200")) { head.lineSequence().first() }
            var total = 0L
            val buffer = ByteArray(64 shl 10)
            while (true) {
                val n = input.read(buffer)
                if (n < 0) break
                total += n
            }
            total
        }
    }

    private fun forwards(): Boolean = download(256L shl 10).getOrNull() == 256L shl 10

    /**
     * The CPU is not scheduled for [seconds] and then runs again: this process is stopped by a
     * child process (same user) and continued after the pause. Both clocks run on, so the
     * Core's watchdog sees a stall (it is not woken for far longer than its tick), the same
     * signal a phone gives after deep sleep, where the wall clock jumps instead.
     */
    private fun stall(seconds: Int) {
        val pid = android.os.Process.myPid()
        ProcessBuilder("/system/bin/sh", "-c", "sleep 1; kill -STOP $pid; sleep $seconds; kill -CONT $pid").start().waitFor()
    }

    private fun lastGate(c: SessionController): GateRecord? = c.state.value.snapshot.lastGate

    private fun closures(c: SessionController): Long = lastGate(c)?.totalClosures ?: 0

    /** Waits for gate number [closure] to end; null when it did not within [millis]. */
    private suspend fun gateEnded(c: SessionController, closure: Long, millis: Long): GateRecord? {
        waitFor(c, millis) { (it.snapshot.lastGate?.totalClosures ?: 0) >= closure || it.snapshot.state != State.CONNECTED }
        return lastGate(c)?.takeIf { it.totalClosures >= closure }
    }

    private fun connected(c: SessionController) = c.state.value.snapshot.state == State.CONNECTED && c.state.value.snapshot.gate == null

    private suspend fun connect(c: SessionController, node: String = "dedicated"): Boolean {
        if (c.state.value.snapshot.selectedNodeId != nodeIds.getValue(node) || !c.state.value.snapshot.configReady) {
            main { c.selectNow(nodeIds.getValue(node)) }
        }
        main { c.connectNow() }
        return connected(c) && vpnNetworkUp()
    }

    /**
     * Each case starts from a connected tunnel on a working network, so one case's failure is
     * not reported again as the next one's. False (recorded) when that cannot be reached.
     */
    private suspend fun ready(c: SessionController, tag: String): Boolean {
        val deadline = SystemClock.elapsedRealtime() + 90_000
        while (!container.currentNetwork().available && SystemClock.elapsedRealtime() < deadline) delay(500)
        waitFor(c, 30_000) { it.snapshot.gate == null }
        if (connected(c) && vpnNetworkUp()) return true
        if (!c.state.value.snapshot.tunnelBusy && connect(c)) {
            record("$tag-pre", "PASS", "reconnected before the case (the previous case left the tunnel down: ${c.state.value.snapshot.lastError?.code})")
            return true
        }
        record("$tag-pre", "FAIL", "could not start from a connected tunnel: network ${container.currentNetwork().interfaceName}, ${c.state.value.snapshot.state} ${c.state.value.message?.code} ${c.state.value.snapshot.lastError?.code}")
        return false
    }

    private suspend fun disconnect(c: SessionController) {
        main { c.disconnectNow() }
        waitFor(c, 15_000) { !it.snapshot.tunnelBusy && it.snapshot.metering?.pending != true }
    }

    private fun describe(gate: GateRecord?): String =
        if (gate == null) "no gate record" else "gate #${gate.totalClosures}: ${gate.reason} -> ${gate.outcome} after ${gate.closedMillis} ms, ${gate.attempts} attempt(s)"

    // ---------------------------------------------------------------- device state

    private var wifiOff = false
    private var airplane = false
    private var dozing = false
    private var screenOff = false
    private var competitor = false

    private fun screen(on: Boolean) {
        if (on) {
            shell("input keyevent KEYCODE_WAKEUP")
            shell("wm dismiss-keyguard")
        } else {
            shell("input keyevent KEYCODE_SLEEP")
        }
        screenOff = !on
    }

    private fun restoreDevice() {
        if (competitor) runCatching { shell("am start -n com.asterlink.vpn.test/com.asterlink.vpn.CompetitorActivity --ez stop true") }
        runCatching { shell("appops set com.asterlink.vpn.test ACTIVATE_VPN default") }
        if (airplane) runCatching { shell("cmd connectivity airplane-mode disable") }
        if (wifiOff) runCatching { shell("svc wifi enable") }
        if (dozing) runCatching { shell("dumpsys deviceidle unforce"); shell("dumpsys battery reset") }
        if (screenOff || dozing) runCatching { screen(true) }
        container.core.setForeground(true)
        runCatching { world("api", """{"offline":false}""") }
    }

    // ---------------------------------------------------------------- cases

    private suspend fun runCases() {
        val c = signIn()
        if (c.state.value.snapshot.nodes.isEmpty()) {
            record("L-signin", "FAIL", "no lines after sign-in: ${c.state.value.message?.code}")
            return
        }

        notificationsDenied(c)
        if (!connected(c) && !connect(c)) {
            record("L-connect", "FAIL", "could not connect: ${c.state.value.message?.code} ${c.state.value.snapshot.lastError?.code}")
            return
        }

        wakeAfterStall(c)
        screenOffWithoutUser(c)
        gateHoldsWithoutTheServer(c)
        doze(c)
        background(c)
        wifiAndCellular(c)
        airplaneMode(c)
        revokedByAnotherVpn(c)
        taskRemoved(c)
        cycles(c)
        soak(c)
        record("L07-8h", "NOT_RUN", "an 8-hour connected sample needs an unattended run: scripts/android/lifecycle.sh soak --hours 8 (docs/android-lifecycle.md)")
        record("L08-dns-webrtc", "NOT_RUN", "Private DNS off/opportunistic/strict and the any:53 hijack are the tun suite's T03; Chrome DoH and a WebRTC page need Chrome on the image and a person (docs/android-lifecycle.md)")
        disconnect(c)
    }

    /** Notifications denied (Android 13+): the VPN works, nothing is posted; then the permission is given back. */
    private suspend fun notificationsDenied(c: SessionController) {
        val denied = !VpnNotifications.mayPost(context)
        if (!denied) {
            record("N01-denied", "BLOCKED", "the suite could not revoke POST_NOTIFICATIONS before the run")
        } else {
            val up = connect(c)
            val forwarded = up && forwards()
            verdict("N01-denied", buildList {
                if (!up) add("did not connect without the notification permission: ${c.state.value.message?.code}")
                if (up && !forwarded) add("connected but did not forward")
            }, "notifications denied: connected=$up, forwarded 256 KiB=$forwarded, posting skipped (mayPost=false); the system VPN key icon remains the indicator")
            disconnect(c)
        }
        shell("pm grant com.asterlink.vpn android.permission.POST_NOTIFICATIONS")
        record("N01-granted", if (VpnNotifications.mayPost(context)) "PASS" else "FAIL", "permission given back through the system: mayPost=${VpnNotifications.mayPost(context)}")
    }

    /** L01 (stall): the CPU not scheduled for 15 s, then the gate re-confirms and reopens the same tunnel. */
    private suspend fun wakeAfterStall(c: SessionController) {
        if (!ready(c, "L01-stall")) return
        val since = c.state.value.connectedSinceMillis
        val next = closures(c) + 1
        val began = SystemClock.elapsedRealtime()
        stall(15)
        val resumed = SystemClock.elapsedRealtime()
        val gate = gateEnded(c, next, 30_000)
        val reopenedAfter = SystemClock.elapsedRealtime() - resumed
        val forwarded = forwards()
        verdict("L01-stall", buildList {
            if (gate == null) add("no gate closed after a ${(resumed - began) / 1000} s stall")
            else {
                if (gate.reason != "lifecycle_reconfirmation_required") add("gate reason ${gate.reason}")
                if (gate.outcome != "reopened") add("gate ended ${gate.outcome}: ${c.state.value.snapshot.lastError?.code}")
            }
            if (!connected(c)) add("not connected afterwards: ${c.state.value.snapshot.state}")
            if (c.state.value.connectedSinceMillis != since) add("the connection was replaced, not resumed")
            if (!forwarded) add("did not forward after reopening")
            if (!vpnNetworkUp()) add("the VPN network disappeared")
        }, "stopped ${(resumed - began) / 1000} s; ${describe(gate)}; connected again within $reopenedAfter ms of resuming without any user action; same connection, forwards")
    }

    /** L01 (screen): screen off for a while, then on: still the same connection, no user action. */
    private suspend fun screenOffWithoutUser(c: SessionController) {
        if (!ready(c, "L01-screen")) return
        val seconds = config["screen_off_seconds"]?.jsonPrimitive?.int ?: 120
        val since = c.state.value.connectedSinceMillis
        val before = closures(c)
        val wall = System.currentTimeMillis()
        val awake = SystemClock.uptimeMillis()
        val real = SystemClock.elapsedRealtime()
        screen(false)
        delay(seconds * 1000L)
        screen(true)
        val asleepMillis = (SystemClock.elapsedRealtime() - real) - (SystemClock.uptimeMillis() - awake)
        waitFor(c, 30_000) { it.snapshot.gate == null && it.snapshot.state == State.CONNECTED }
        val gates = closures(c) - before
        val forwarded = forwards()
        verdict("L01-screen", buildList {
            if (!connected(c)) add("not connected after the screen came back: ${c.state.value.snapshot.state} ${c.state.value.snapshot.lastError?.code}")
            if (c.state.value.connectedSinceMillis != since) add("the connection was replaced")
            if (!forwarded) add("did not forward afterwards")
        }, "screen off ${seconds} s (wall ${(System.currentTimeMillis() - wall) / 1000} s, device asleep ${asleepMillis / 1000} s by uptime vs elapsed); " +
            "gates closed meanwhile: $gates (${describe(lastGate(c).takeIf { gates > 0 })}); still the same connection, forwards, no user action")
    }

    /** L01 (no leak): while the gate is closed and the server unreachable, nothing is forwarded; it reopens by itself. */
    private suspend fun gateHoldsWithoutTheServer(c: SessionController) {
        if (!ready(c, "L01-gate-holds")) return
        val next = closures(c) + 1
        world("api", """{"offline":true}""")
        stall(12)
        val closedIn = waitFor(c, 20_000) { it.snapshot.gate?.waitingNetwork == true }
        val shown = c.state.value.connection.status
        val sinkBefore = sinkDown()
        val attempt = download(256L shl 10, timeoutMillis = 5_000)
        val sinkAfter = sinkDown()
        val vpnHeld = vpnNetworkUp()
        val notice = com.asterlink.vpn.session.TunnelNotice.from(c.state.value)
        world("api", """{"offline":false}""")
        val gate = gateEnded(c, next, 60_000)
        val forwarded = forwards()
        verdict("L01-gate-holds", buildList {
            if (closedIn == null) add("the gate never reported waiting for the network")
            if (shown != com.asterlink.vpn.session.ConnectionStatus.WaitingNetwork) add("the connect page showed $shown")
            if (notice.gate != com.asterlink.vpn.session.TunnelNotice.GateState.WaitingNetwork) add("the notification showed ${notice.gate}")
            if (attempt.getOrNull() == 256L shl 10) add("a download completed while the gate was closed")
            if (sinkAfter != sinkBefore) add("the sink sent ${sinkAfter - sinkBefore} bytes while the gate was closed")
            if (!vpnHeld) add("the VPN network was dropped (traffic would have left outside it)")
            if (gate?.outcome != "reopened") add("${describe(gate)}; ${c.state.value.snapshot.lastError?.code}")
            if (!connected(c) || !forwarded) add("not forwarding after the server came back")
        }, "server unreachable: gate waiting after ${closedIn} ms, page '$shown', notification '${notice.gate}'; a download through the tunnel failed (${attempt.exceptionOrNull()?.message?.take(80)}), " +
            "sink +${sinkAfter - sinkBefore} bytes, VPN kept=$vpnHeld; server back: ${describe(gate)}, forwards again")
    }

    /** L02: Doze in and out. The foreground service keeps the control plane; nothing stops. */
    private suspend fun doze(c: SessionController) {
        if (!ready(c, "L02-doze")) return
        val since = c.state.value.connectedSinceMillis
        shell("dumpsys deviceidle enable deep")
        shell("dumpsys battery unplug")
        dozing = true
        screen(false)
        val forced = shell("dumpsys deviceidle force-idle deep")
        val stateIn = shell("dumpsys deviceidle get deep")
        hits()
        delay(15_000)
        val during = hits()
        val forwarded = forwards()
        val stillIdle = shell("dumpsys deviceidle get deep")
        shell("dumpsys deviceidle unforce")
        shell("dumpsys battery reset")
        dozing = false
        screen(true)
        waitFor(c, 20_000) { it.snapshot.gate == null }
        val reports = during["traffic"] ?: 0
        verdict("L02-doze", buildList {
            if (stateIn != "IDLE") add("could not enter deep idle: '$forced' / '$stateIn'")
            if (reports < 3) add("only $reports traffic reports in 15 s of Doze (the control plane was cut)")
            if (!forwarded) add("did not forward in Doze")
            if (!connected(c) || c.state.value.connectedSinceMillis != since) add("the connection did not survive Doze: ${c.state.value.snapshot.state} ${c.state.value.snapshot.lastError?.code}")
        }, "deep idle '$stateIn' (still '$stillIdle' after 15 s): $reports traffic reports and ${during["session"] ?: 0} session reads reached the server, a 256 KiB download through the tunnel worked; out of Doze still the same connection")
    }

    /** 7.6: in the background the catalog long-poll pauses, and a server verdict still stops the tunnel promptly. */
    private suspend fun background(c: SessionController) {
        if (!ready(c, "BG")) return
        val interval = world["report_interval_seconds"]?.jsonPrimitive?.int ?: 2
        hits()
        delay(8_000)
        val foreground = hits()
        container.core.setForeground(false)
        delay(3_000) // a long-poll already in flight may still finish
        hits()
        delay(12_000)
        val backgrounded = hits()
        val nodeId = nodeIds.getValue("dedicated")
        val rate = c.state.value.snapshot.nodes.first { it.id == nodeId }.ratePermille
        val revision = c.state.value.snapshot.catalogRevision
        val changed = SystemClock.elapsedRealtime()
        world("edit", """{"node":"dedicated","rate_permille":${rate + 250}}""")
        val stoppedIn = waitFor(c, 30_000) { it.snapshot.state != State.CONNECTED && !it.snapshot.tunnelBusy }
        val reason = c.state.value.snapshot.lastError?.code
        container.core.setForeground(true)
        world("edit", """{"node":"dedicated","rate_permille":$rate}""")
        // Back in the foreground the paused long-poll resumes: the catalog after both edits arrives.
        val caughtUp = waitFor(c, 15_000) { s -> s.snapshot.catalogRevision != revision && s.snapshot.nodes.any { it.id == nodeId && it.ratePermille == rate } }
        verdict("BG-catalog", buildList {
            if ((backgrounded["nodes"] ?: 0) > 1) add("${backgrounded["nodes"]} catalog polls in 12 s of background")
            if ((backgrounded["traffic"] ?: 0) < 12 / interval - 2) add("only ${backgrounded["traffic"]} traffic reports in the background")
        }, "foreground 8 s: ${foreground["nodes"] ?: 0} catalog long-polls, ${foreground["traffic"] ?: 0} traffic reports; background 12 s: ${backgrounded["nodes"] ?: 0} catalog polls, " +
            "${backgrounded["traffic"] ?: 0} traffic reports (report interval $interval s)")
        verdict("BG-verdict", buildList {
            if (stoppedIn == null) add("a re-rated line kept forwarding for 30 s in the background")
            else if (stoppedIn > interval * 1000L + 5_000) add("stopped only after $stoppedIn ms")
            if (caughtUp == null) add("the catalog did not catch up within 15 s of returning to the foreground")
        }, "back in the foreground the catalog caught up in ${caughtUp} ms; line re-rated while in the background: tunnel stopped after ${stoppedIn ?: (SystemClock.elapsedRealtime() - changed)} ms with $reason " +
            "(server verdict on the next traffic acknowledgement; bound = report interval $interval s, production 60 s)")
        if (!connect(c)) record("BG-reconnect", "FAIL", "could not reconnect: ${c.state.value.message?.code} ${c.state.value.snapshot.lastError?.code}")
        else {
            // The binding made from the caught-up catalog must survive the next catalog reads.
            delay(6_000)
            verdict("BG-reconnect", buildList {
                if (!connected(c)) add("the new connection stopped: ${c.state.value.snapshot.lastError?.code}")
            }, "reconnected at the restored rate and still connected 6 s later (no stale-catalog profile_changed)")
        }
    }

    /** L03: Wi-Fi off (cellular takes over) and back: the gate re-confirms on each new network. */
    private suspend fun wifiAndCellular(c: SessionController) {
        if (!ready(c, "L03-wifi-cellular")) return
        val since = c.state.value.connectedSinceMillis
        val before = closures(c)
        shell("svc data enable")
        shell("svc wifi disable")
        wifiOff = true
        val off = gateEnded(c, before + 1, 45_000)
        waitFor(c, 30_000) { it.snapshot.gate == null }
        val onCellular = container.currentNetwork()
        val forwardedCellular = forwards()
        shell("svc wifi enable")
        wifiOff = false
        val wifiBack = waitFor(c, 45_000) { container.currentNetwork().interfaceName != onCellular.interfaceName && (it.snapshot.lastGate?.totalClosures ?: 0) >= before + 2 && it.snapshot.gate == null }
        val back = lastGate(c)
        val forwardedWifi = forwards()
        verdict("L03-wifi-cellular", buildList {
            if (off == null) add("no gate when Wi-Fi went away")
            else if (off.reason != "network_changed" && off.reason != "lifecycle_reconfirmation_required") add("first gate reason ${off.reason}")
            if (!onCellular.available) add("no network after Wi-Fi went off (no cellular in this image?)")
            if (!forwardedCellular) add("did not forward on ${onCellular.interfaceName}")
            if (wifiBack == null) add("no reconfirmed switch back to Wi-Fi: ${describe(back)}")
            if (!forwardedWifi || !connected(c)) add("not forwarding after Wi-Fi returned")
            if (c.state.value.connectedSinceMillis != since) add("the connection was replaced")
        }, "Wi-Fi off: ${describe(off)}, then on ${onCellular.interfaceName} forwards; Wi-Fi on: ${describe(back)}, forwards on ${container.currentNetwork().interfaceName}; same connection throughout")
    }

    /** L03: airplane mode: the gate waits for a network (nothing forwarded) and resumes when it returns. */
    private suspend fun airplaneMode(c: SessionController) {
        if (!ready(c, "L03-airplane")) return
        val before = closures(c)
        shell("cmd connectivity airplane-mode enable")
        airplane = true
        val waiting = waitFor(c, 30_000) { it.snapshot.gate?.waitingNetwork == true || it.snapshot.state != State.CONNECTED }
        val gateNow = c.state.value.snapshot.gate
        val leak = download(64L shl 10, timeoutMillis = 4_000).getOrNull()
        val vpnHeld = vpnNetworkUp()
        delay(5_000)
        shell("cmd connectivity airplane-mode disable")
        airplane = false
        val back = SystemClock.elapsedRealtime()
        val gate = gateEnded(c, before + 1, 120_000)
        val reopened = SystemClock.elapsedRealtime() - back
        waitFor(c, 30_000) { it.snapshot.gate == null }
        val forwarded = forwards()
        verdict("L03-airplane", buildList {
            if (waiting == null || gateNow == null) add("no waiting gate in airplane mode: ${c.state.value.snapshot.state} ${c.state.value.snapshot.lastError?.code}")
            if (leak != null) add("a download completed in airplane mode")
            if (gate?.outcome != "reopened") add("${describe(gate)}; ${c.state.value.snapshot.lastError?.code}")
            if (!connected(c) || !forwarded) add("not forwarding after airplane mode")
        }, "airplane mode: gate ${gateNow?.reason} waiting for the network after $waiting ms (VPN kept=$vpnHeld, nothing forwarded); off again: ${describe(gate)}, " +
            "reopened $reopened ms after airplane mode ended (Wi-Fi reassociation included), forwards")
    }

    /** L04: another VPN app takes over: the tunnel stops, the tail is settled, and the user is told why. */
    private suspend fun revokedByAnotherVpn(c: SessionController) {
        if (!ready(c, "L04-revoke")) return
        synchronized(alerts) { alerts.clear() }
        shell("appops set com.asterlink.vpn.test ACTIVATE_VPN allow")
        competitor = true
        val started = SystemClock.elapsedRealtime()
        shell("am start -W -n com.asterlink.vpn.test/com.asterlink.vpn.CompetitorActivity")
        val stoppedIn = waitFor(c, 30_000) { !it.snapshot.tunnelBusy && it.snapshot.metering?.pending != true }
        val s = c.state.value
        val alerted = synchronized(alerts) { alerts.toList() }
        val serviceGone = container.vpnHosts.current == null || container.vpnHosts.current?.isEnding == true
        val ledger = Json.parseToJsonElement(world("ledger", """{"user":"life"}""")).jsonObject
        val metering = s.snapshot.metering
        val settled = metering != null && !metering.pending &&
            ledger.getValue("upload_bytes").jsonPrimitive.long == metering.confirmedUploadBytes &&
            ledger.getValue("download_bytes").jsonPrimitive.long == metering.confirmedDownloadBytes
        shell("am start -W -n com.asterlink.vpn.test/com.asterlink.vpn.CompetitorActivity --ez stop true")
        competitor = false
        shell("appops set com.asterlink.vpn.test ACTIVATE_VPN default")
        val gone = run {
            val deadline = SystemClock.elapsedRealtime() + 10_000
            while (vpnNetworkUp() && SystemClock.elapsedRealtime() < deadline) delay(200)
            !vpnNetworkUp()
        }
        verdict("L04-revoke", buildList {
            if (stoppedIn == null) add("the tunnel did not end after another VPN took over: ${s.snapshot.state}")
            if (s.message?.code != LocalCodes.VPN_REVOKED) add("the app said ${s.message?.code}")
            if (alerted != listOf(Notice.local(LocalCodes.VPN_REVOKED))) add("alerts $alerted")
            if (!serviceGone) add("AsterVpnService still registered")
            if (!settled) add("tail not settled: pending ${metering?.pending}, server ${ledger["upload_bytes"]}/${ledger["download_bytes"]}, Core ${metering?.confirmedUploadBytes}/${metering?.confirmedDownloadBytes}")
            if (!gone) add("a VPN network remained after the competitor stopped")
        }, "the competitor's VPN revoked ours: stopped and settled in ${stoppedIn} ms (${SystemClock.elapsedRealtime() - started} ms incl. the competitor), " +
            "server ledger = Core's confirmed counters, the user is told '${LocalCodes.VPN_REVOKED}' once; no VPN left after the competitor stopped")
        if (!connect(c)) record("L04-reconnect", "FAIL", "could not reconnect after the competitor: ${c.state.value.message?.code} ${c.state.value.snapshot.lastError?.code}")
        else record("L04-reconnect", "PASS", "connected again (the app prepares itself as the VPN owner again)")
    }

    /** L05: the app's task removed from recents (the activity gone): the VPN keeps running. */
    private suspend fun taskRemoved(c: SessionController) {
        if (!ready(c, "L05-task-removed")) return
        val since = c.state.value.connectedSinceMillis
        val scenario = ActivityScenario.launch(MainActivity::class.java)
        delay(1_500)
        scenario.onActivity { it.finishAndRemoveTask() }
        delay(6_000)
        val forwarded = forwards()
        runCatching { scenario.close() }
        verdict("L05-task-removed", buildList {
            if (!connected(c) || c.state.value.connectedSinceMillis != since) add("the connection did not survive: ${c.state.value.snapshot.state}")
            if (container.vpnHosts.current == null) add("the VPN service stopped")
            if (!forwarded) add("did not forward")
        }, "the activity's task was removed (what swiping it from recents does); the foreground VPN service and the same connection remain and forward")
    }

    private fun procStatus(field: String): Long =
        File("/proc/self/status").readLines().firstOrNull { it.startsWith("$field:") }?.substringAfter(':')?.trim()?.split(' ')?.first()?.toLongOrNull() ?: -1

    private fun fds(): Int = File("/proc/self/fd").list()?.size ?: -1

    private fun cpuTicks(): Long {
        val fields = File("/proc/self/stat").readText().substringAfterLast(')').trim().split(' ')
        return (fields.getOrNull(11)?.toLongOrNull() ?: 0) + (fields.getOrNull(12)?.toLongOrNull() ?: 0)
    }

    /** L07: 20 connect/disconnect cycles leave no descriptors or threads behind. */
    private suspend fun cycles(c: SessionController) {
        val count = config["cycles"]?.jsonPrimitive?.int ?: 20
        disconnect(c)
        if (!connect(c)) return record("L07-cycles", "FAIL", "warm-up connect failed")
        disconnect(c)
        vpnGone()
        delay(1_000)
        val fd0 = fds()
        val th0 = procStatus("Threads")
        var ok = 0
        var retried = 0
        val failures = mutableListOf<String>()
        repeat(count) { i ->
            main { c.connectNow() }
            val core = connected(c)
            val vpn = core && vpnNetworkUp()
            // The Core's Verify already proved forwarding; the app's own download is checked
            // as well, with one retry (counted and reported) for a first flow racing the new network.
            var got = if (vpn) download(256L shl 10) else null
            if (got != null && got.getOrNull() != 256L shl 10) {
                retried++
                delay(500)
                got = download(256L shl 10)
            }
            val problem = when {
                !core -> "#${i + 1}: not connected (${c.state.value.snapshot.state}, ${c.state.value.message?.code ?: c.state.value.snapshot.lastError?.code})"
                !vpn -> "#${i + 1}: connected but no VPN network registered"
                got?.getOrNull() != 256L shl 10 -> "#${i + 1}: download failed twice (${got?.exceptionOrNull()?.message?.take(80)})"
                else -> null
            }
            disconnect(c)
            // The Core reports stopped before Android unregisters the VPN network. Without this
            // wait the next cycle's vpnNetworkUp() can be this dying network, and a socket routed
            // onto it fails at once (seen on a loaded runner: both attempts refused within 500 ms).
            val gone = vpnGone()
            when {
                problem != null -> failures += problem
                gone == null -> failures += "#${i + 1}: VPN network still registered 10 s after disconnect"
                else -> ok++
            }
        }
        delay(2_000)
        val fd1 = fds()
        val th1 = procStatus("Threads")
        verdict("L07-cycles", buildList {
            if (ok != count) add("$ok/$count cycles connected and forwarded (${failures.take(3)})")
            if (fd1 - fd0 > 3) add("descriptors $fd0 -> $fd1")
            if (th1 - th0 > 4) add("threads $th0 -> $th1")
            if (vpnNetworkUp()) add("a VPN network remained")
        }, "$ok/$count connect -> forward -> disconnect cycles ($retried download retried once); descriptors $fd0 -> $fd1, threads $th0 -> $th1 (Go goroutines are proven by the tun suite's T-CYCLES)")
    }

    /** L07 (short soak): connected for a few minutes with periodic traffic, sampled every 15 s. */
    private suspend fun soak(c: SessionController) {
        val seconds = config["soak_seconds"]?.jsonPrimitive?.int ?: 180
        if (!connect(c)) return record("L07-soak", "FAIL", "could not connect: ${c.state.value.message?.code}")
        val samples = mutableListOf<JsonObject>()
        val start = SystemClock.elapsedRealtime()
        var downloads = 0
        var failed = 0
        while (SystemClock.elapsedRealtime() - start < seconds * 1000L) {
            val t = (SystemClock.elapsedRealtime() - start) / 1000
            if (t % 30 < 15) {
                if (download(1L shl 20).getOrNull() == 1L shl 20) downloads++ else failed++
            }
            samples += JsonObject(mapOf(
                "t_s" to JsonPrimitive(t), "rss_kb" to JsonPrimitive(procStatus("VmRSS")), "threads" to JsonPrimitive(procStatus("Threads")),
                "fds" to JsonPrimitive(fds()), "cpu_ticks" to JsonPrimitive(cpuTicks()), "connected" to JsonPrimitive(connected(c)),
            ))
            delay(15_000)
        }
        File(dir, "samples.json").writeText(JsonArray(samples).toString())
        val first = samples.first()
        val last = samples.last()
        fun JsonObject.n(k: String) = getValue(k).jsonPrimitive.long
        val dropped = samples.count { !it.getValue("connected").jsonPrimitive.boolean }
        verdict("L07-soak", buildList {
            if (dropped > 0) add("$dropped samples not connected")
            if (failed > 0) add("$failed of ${downloads + failed} downloads failed")
            if (last.n("fds") - first.n("fds") > 4) add("descriptors ${first.n("fds")} -> ${last.n("fds")}")
        }, "${seconds}s connected, ${samples.size} samples (samples.json): RSS ${first.n("rss_kb")} -> ${last.n("rss_kb")} kB, threads ${first.n("threads")} -> ${last.n("threads")}, " +
            "fds ${first.n("fds")} -> ${last.n("fds")}, CPU ${last.n("cpu_ticks") - first.n("cpu_ticks")} ticks; $downloads x 1 MiB downloads")
    }
}
