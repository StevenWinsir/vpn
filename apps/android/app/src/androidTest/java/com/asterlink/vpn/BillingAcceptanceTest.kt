package com.asterlink.vpn

import android.app.Notification
import android.app.NotificationManager
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.SystemClock
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.core.CoreSnapshot.State
import com.asterlink.vpn.core.LoginTarget
import com.asterlink.vpn.security.FileDeviceIdentity
import com.asterlink.vpn.session.CoreLifecycle
import com.asterlink.vpn.session.CredentialStore
import com.asterlink.vpn.session.LoginPreferences
import com.asterlink.vpn.session.SessionController
import com.asterlink.vpn.session.SessionState
import com.asterlink.vpn.vpn.NotificationAlerts
import com.asterlink.vpn.vpn.VpnNotifications
import java.io.File
import java.io.InputStream
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.net.URI
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicBoolean
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
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Round-4 connection and billing acceptance (handoff_app_android.md B01, B02, A04, B04; and the
 * online presence cases P01-P05 of node groups round 6) on the
 * device, through everything that ships: the app's SessionController, AsterVpnService with
 * VpnService.Builder and protect(), the Go Core's managed session (gates, first metering ACK,
 * TUN, Verify, the meter, the stop paths) and the real Gin/PostgreSQL backend started by
 * scripts/test-native-android.py --suite billing (backend TestAndroidBillingWorld, reached
 * through `adb reverse`). The lines lead to the suite's test node on the host; the bytes go to
 * the traffic sink behind it, whose own counts are the independent truth. The APK is the normal
 * debug build: no acceptance hook is linked into it, and the lost acknowledgement (B02) is
 * injected by the backend world, not by the app.
 *
 * Only runs when the suite has written files/billingtest/world.json; otherwise skipped, so the
 * emulator suite's connectedDebugAndroidTest is unaffected. Results go to
 * files/billingtest/report.json, one entry per case with what was measured.
 */
@SuiteDriver
@RunWith(AndroidJUnit4::class)
class BillingAcceptanceTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val container = (context.applicationContext as AsterApp).container
    private val dir = File(context.filesDir, "billingtest")
    private val cases = mutableListOf<JsonObject>()

    private lateinit var world: JsonObject
    private lateinit var config: JsonObject
    private lateinit var apiBase: String
    private lateinit var password: String
    private lateinit var emails: Map<String, String>
    private lateinit var nodeIds: Map<String, String>
    private lateinit var names: Map<String, String>

    @Test
    fun acceptance() {
        val worldFile = File(dir, "world.json")
        assumeTrue("run through scripts/test-native-android.py --suite billing", worldFile.isFile)
        runBlocking { runAcceptance(worldFile) }
    }

    private suspend fun runAcceptance(worldFile: File) {
        world = Json.parseToJsonElement(worldFile.readText()).jsonObject
        config = Json.parseToJsonElement(File(dir, "config.json").readText()).jsonObject
        apiBase = world.str("api_base")
        password = world.str("password")
        emails = world.getValue("emails").jsonObject.mapValues { it.value.jsonPrimitive.content }
        nodeIds = world.getValue("nodes").jsonObject.mapValues { it.value.jsonPrimitive.content }
        names = world.getValue("names").jsonObject.mapValues { it.value.jsonPrimitive.content }
        // The world's password is test-only, but it still never stays on the device.
        worldFile.delete()
        try {
            runCases()
        } catch (error: Throwable) {
            record("B.unexpected", "FAIL", "${error.javaClass.simpleName}: ${error.message?.take(300)}")
        } finally {
            controller?.let { c -> runCatching { main { c.signOutNow() } } }
            scope?.cancel()
            container.core.stop()
            container.sessionState = container.session.state
            File(dir, "report.json").writeText(JsonObject(mapOf("cases" to JsonArray(cases))).toString())
        }
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

    private var scope: kotlinx.coroutines.CoroutineScope? = null
    private var controller: SessionController? = null

    /**
     * A controller over the app's own Core, VPN launcher and notifications, aimed at the world.
     * The VPN service runs the container's production tunnel session, which claims the
     * request from the same launcher; the notification follows this controller's state.
     */
    private fun newController(): SessionController {
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
            // The instrumented process has no activity on screen: alerts are posted.
            alerts = NotificationAlerts(context) { false },
        )
        container.sessionState = c.state
        controller = c
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

    private suspend fun waitUntil(millis: Long, predicate: () -> Boolean): Long? {
        val start = SystemClock.elapsedRealtime()
        while (SystemClock.elapsedRealtime() - start < millis) {
            if (predicate()) return SystemClock.elapsedRealtime() - start
            delay(100)
        }
        return null
    }

    private fun record(id: String, status: String, observed: String) {
        cases += JsonObject(mapOf("case_id" to JsonPrimitive(id), "status" to JsonPrimitive(status), "observed" to JsonPrimitive(observed)))
    }

    private fun verdict(id: String, problems: List<String>, passText: String) =
        record(id, if (problems.isEmpty()) "PASS" else "FAIL", if (problems.isEmpty()) passText else problems.joinToString("; "))

    /** The world's loopback control calls (a raw socket: the app's network policy forbids cleartext HTTP). */
    private fun world(action: String, body: String = "{}"): String {
        val uri = URI(apiBase)
        Socket().use { socket ->
            socket.connect(InetSocketAddress("127.0.0.1", uri.port), 5_000)
            socket.soTimeout = 20_000
            val bytes = body.toByteArray()
            socket.getOutputStream().write(
                // HTTP/1.0: the reply is never chunked, the body simply ends with the connection.
                ("POST /__world/$action HTTP/1.0\r\nHost: 127.0.0.1\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\n\r\n").toByteArray() + bytes,
            )
            val answer = socket.getInputStream().bufferedReader().readText()
            val status = answer.lineSequence().first()
            check(status.contains(" 200") || status.contains(" 204")) { "world $action: " + answer.substringAfter("\r\n\r\n").take(300) }
            return answer.substringAfter("\r\n\r\n")
        }
    }

    private data class Ledger(val usedUnits: Long, val totalBytes: Long, val remainingBytes: Long, val uploadBytes: Long, val downloadBytes: Long)

    private fun ledger(who: String): Ledger {
        val o = Json.parseToJsonElement(world("ledger", """{"user":"$who"}""")).jsonObject
        return Ledger(o.getValue("used_units").jsonPrimitive.long, o.getValue("total_bytes").jsonPrimitive.long, o.getValue("remaining_bytes").jsonPrimitive.long,
            o.getValue("upload_bytes").jsonPrimitive.long, o.getValue("download_bytes").jsonPrimitive.long)
    }

    private fun sinkStats(): Pair<Long, Long> {
        val o = Json.parseToJsonElement(world("sink")).jsonObject
        return o.getValue("up_received").jsonPrimitive.long to o.getValue("down_sent").jsonPrimitive.long
    }

    private fun trafficReports(): List<JsonObject> = Json.parseToJsonElement(world("traffic")).jsonObject.getValue("reports").let {
        if (it is JsonArray) it.map { r -> r.jsonObject } else emptyList()
    }

    private val connectivity: ConnectivityManager get() = context.getSystemService(ConnectivityManager::class.java)

    @Suppress("DEPRECATION")
    private fun vpnNetworkUp(): Boolean = connectivity.allNetworks.any { connectivity.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true }

    private fun notification(id: Int): Notification? =
        context.getSystemService(NotificationManager::class.java).activeNotifications.firstOrNull { it.id == id }?.notification

    private fun Notification.text(): String = extras.getCharSequence(Notification.EXTRA_TEXT)?.toString().orEmpty()

    // ---------------------------------------------------------------- traffic through the tunnel

    private val sink get() = config.getValue("sink").jsonObject
    private val background get() = config.getValue("background_bytes").jsonPrimitive.long

    /** Downloads exactly [bytes] of body from the sink by [host]; returns the body length read. */
    private fun download(host: String, bytes: Long): Long = withRequest(host, "GET /down?t=${sink.str("token")}&bytes=$bytes HTTP/1.1\r\nHost: $host\r\nConnection: close\r\n\r\n".toByteArray(), 0) { input ->
        var total = 0L
        val buffer = ByteArray(64 shl 10)
        while (true) {
            val n = input.read(buffer)
            if (n < 0) break
            total += n
        }
        total
    }

    /** Uploads exactly [bytes] of body; returns what the sink says it received. */
    private fun upload(host: String, bytes: Long): Long {
        val header = "POST /up?t=${sink.str("token")} HTTP/1.1\r\nHost: $host\r\nContent-Type: application/octet-stream\r\nContent-Length: $bytes\r\nConnection: close\r\n\r\n".toByteArray()
        return withRequest(host, header, bytes) { input ->
            val text = input.readBytes().toString(Charsets.UTF_8)
            Regex("\"received\":(\\d+)").find(text)?.groupValues?.get(1)?.toLong() ?: -1
        }
    }

    private fun <T> withRequest(host: String, header: ByteArray, uploadBytes: Long, read: (InputStream) -> T): T {
        Socket().use { socket ->
            socket.connect(InetSocketAddress(host, sink.str("port").toInt()), 15_000)
            socket.soTimeout = 60_000
            val out = socket.getOutputStream()
            out.write(header)
            val block = ByteArray(64 shl 10) { (it % 251).toByte() }
            var left = uploadBytes
            while (left > 0) {
                val n = minOf(left, block.size.toLong()).toInt()
                out.write(block, 0, n)
                left -= n
            }
            out.flush()
            val input = socket.getInputStream().buffered()
            // Status line and headers end at the first empty line.
            val head = StringBuilder()
            while (!head.endsWith("\r\n\r\n")) {
                val b = input.read()
                check(b >= 0) { "connection closed in the headers: $head" }
                head.append(b.toChar())
            }
            check(head.startsWith("HTTP/1.1 200")) { head.lineSequence().first() }
            return read(input)
        }
    }

    /** The ongoing notification's text sampled while a download runs (live rates are non-zero then). */
    private suspend fun notificationWhileDownloading(): String {
        val worker = Thread { runCatching { download(sink.str("name"), 16L shl 20) } }.apply { start() }
        var text = ""
        waitUntil(8_000) {
            text = notification(VpnNotifications.ID)?.text().orEmpty()
            Regex("↓([1-9][0-9.]*) ").containsMatchIn("$text ")
        }
        worker.join(60_000)
        return text
    }

    // ---------------------------------------------------------------- cases

    private suspend fun signIn(who: String): SessionController {
        controller?.let { old -> main { old.signOutNow() } }
        val c = newController()
        main { c.launchNow(); c.signInNow(emails.getValue(who), password, remember = false, automatic = false) }
        waitFor(c, 10_000) { it.snapshot.nodes.isNotEmpty() }
        return c
    }

    /** The selection really took (a refused select leaves the previous line selected and ready). */
    private fun selected(c: SessionController, node: String): Boolean =
        c.state.value.snapshot.selectedNodeId == nodeIds.getValue(node) && c.state.value.snapshot.configReady

    private data class Measured(
        val ledgerBefore: Ledger, val ledgerAfter: Ledger,
        val clientUp: Long, val clientDown: Long,
        val sinkUp: Long, val sinkDown: Long,
        val liveUsage: com.asterlink.vpn.session.ConnectionUsage?,
        val after: CoreSnapshot,
    ) {
        val serverUp get() = ledgerAfter.uploadBytes - ledgerBefore.uploadBytes
        val serverDown get() = ledgerAfter.downloadBytes - ledgerBefore.downloadBytes
        val units get() = ledgerAfter.usedUnits - ledgerBefore.usedUnits
    }

    /**
     * Select -> Connect -> controlled transfer through the tunnel -> Disconnect (tail settled),
     * with the server's ledger and the sink read before and after. Null when a case failed.
     */
    private suspend fun measure(tag: String, c: SessionController, who: String, node: String, up: Long, down: Long, during: suspend () -> Unit = {}): Measured? {
        main { c.selectNow(nodeIds.getValue(node)) }
        if (!selected(c, node)) {
            record("$tag-select", "FAIL", "line not selected: ${c.state.value.message?.code} ${c.state.value.snapshot.lastError?.code}")
            return null
        }
        val ledgerBefore = ledger(who)
        val (sinkUp0, sinkDown0) = sinkStats()
        val before = c.state.value.snapshot.metering
        val started = SystemClock.elapsedRealtime()
        main { c.connectNow() }
        var s = c.state.value
        if (s.snapshot.state != State.CONNECTED) {
            record("$tag-connect", "FAIL", "state ${s.snapshot.state}, message ${s.message?.code}: ${s.message?.text}, last_error ${s.snapshot.lastError?.code}")
            return null
        }
        val rate = s.snapshot.metering?.ratePermille
        record("$tag-connect", if (vpnNetworkUp() && container.vpnHosts.current != null) "PASS" else "FAIL",
            "connected in ${SystemClock.elapsedRealtime() - started} ms after the Core's gates, first ACK, TUN and Verify; health ${s.snapshot.tunnelHealth}, " +
                "bound rate $rate‰, source ${s.snapshot.metering?.source}, VPN network ${vpnNetworkUp()}, service running ${container.vpnHosts.current != null}")
        val host = sink.str("name")
        val gotUp = runCatching { upload(host, up) }
        val gotDown = runCatching { download(host, down) }
        during()
        delay(2_500) // the meter samples once a second; let the live view include the transfer
        val live = c.state.value.usage
        main { c.disconnectNow() }
        waitFor(c, 15_000) { !it.snapshot.tunnelBusy && it.snapshot.metering?.pending != true }
        s = c.state.value
        if (gotUp.getOrNull() != up || gotDown.getOrNull() != down) {
            record("$tag-transfer", "FAIL", "upload ${gotUp.getOrNull() ?: gotUp.exceptionOrNull()?.message}, download ${gotDown.getOrNull() ?: gotDown.exceptionOrNull()?.message}")
            return null
        }
        val after = s.snapshot.metering
        if (after == null || after.pending || before == null) {
            record("$tag-settle", "FAIL", "after Disconnect: pending ${after?.pending}, last_error ${s.snapshot.lastError?.code}")
            return null
        }
        val (sinkUp1, sinkDown1) = sinkStats()
        return Measured(
            ledgerBefore, ledger(who),
            after.confirmedUploadBytes - before.confirmedUploadBytes, after.confirmedDownloadBytes - before.confirmedDownloadBytes,
            sinkUp1 - sinkUp0, sinkDown1 - sinkDown0, live, s.snapshot,
        )
    }

    /** The ledger rules of handoff 8, from the server's own rows and the client's confirmed counters. */
    private fun ledgerProblems(m: Measured, rate: Long): List<String> {
        val problems = mutableListOf<String>()
        val counted = m.serverUp + m.serverDown
        if (m.units != counted * rate) problems += "charged ${m.units} units, (up ${m.serverUp} + down ${m.serverDown}) x $rate = ${counted * rate}"
        if (m.serverUp != m.clientUp || m.serverDown != m.clientDown) problems += "server counted up ${m.serverUp} down ${m.serverDown}, the Core's confirmed counters moved up ${m.clientUp} down ${m.clientDown}"
        val formula = maxOf(0, (m.ledgerAfter.totalBytes * 1000 - m.ledgerAfter.usedUnits) / 1000)
        if (m.ledgerAfter.remainingBytes != formula) problems += "remaining ${m.ledgerAfter.remainingBytes}, formula $formula"
        val shown = m.after.entitlement
        if (shown?.usedUnits != m.ledgerAfter.usedUnits || shown.remainingBytes != m.ledgerAfter.remainingBytes) {
            problems += "the app shows used ${shown?.usedUnits} / remaining ${shown?.remainingBytes}, the server has ${m.ledgerAfter.usedUnits} / ${m.ledgerAfter.remainingBytes}"
        }
        return problems
    }

    private fun bodyProblems(m: Measured, up: Long, down: Long): List<String> {
        val problems = mutableListOf<String>()
        if (m.sinkUp != up || m.sinkDown != down) problems += "the sink saw up ${m.sinkUp} (sent $up), down ${m.sinkDown} (wanted $down)"
        fun check(name: String, counted: Long, body: Long) {
            val limit = body + body / 10 + background
            if (counted < body) problems += "$name counted $counted is below the $body byte body"
            if (counted > limit) problems += "$name counted $counted exceeds the body $body by more than 10% + $background"
        }
        check("upload", m.serverUp, up)
        check("download", m.serverDown, down)
        return problems
    }

    private fun pct(counted: Long, body: Long) = if (body == 0L) "n/a" else String.format(java.util.Locale.ROOT, "%+.2f%%", 100.0 * (counted - body) / body)

    private suspend fun runCases() {
        val upMiB = config.getValue("upload_mib").jsonPrimitive.long
        val downMiB = config.getValue("download_mib").jsonPrimitive.long
        val up = upMiB shl 20
        val down = downMiB shl 20

        // ---- B01: the same controlled bytes at 1x (dedicated, Shadowsocks) and 0.5x (ordinary, VLESS).
        var c = signIn("meter")
        val perRate = mutableListOf<String>()
        for ((index, node) in listOf("dedicated", "ordinary").withIndex()) {
            val tag = "B01-n${index + 1}"
            val rate = c.state.value.snapshot.nodes.first { it.id == nodeIds.getValue(node) }.ratePermille
            val m = measure(tag, c, "meter", node, up, down) ?: continue
            verdict("$tag-sink-and-body", bodyProblems(m, up, down),
                "the sink saw exactly up $up / down $down; counted up ${m.serverUp} (${pct(m.serverUp, up)}), down ${m.serverDown} (${pct(m.serverDown, down)}) incl. HTTP framing, Verify and emulator background")
            verdict("$tag-ledger", ledgerProblems(m, rate),
                "rate $rate‰: (${m.serverUp} + ${m.serverDown}) bytes -> ${m.units} units (${m.units / 1000} bytes of quota); server, Core and app agree; remaining ${m.ledgerAfter.remainingBytes}")
            val live = m.liveUsage
            verdict("$tag-ui", buildList {
                if (live == null) add("no per-connection usage while connected")
                else {
                    if (live.physicalBytes < up + down) add("the connect page showed ${live.physicalBytes} bytes, below the ${up + down} byte body")
                    if (live.ratePermille != rate) add("shown at ${live.ratePermille}‰, the line is $rate‰")
                    if (live.billedBytes != live.physicalBytes / 1000 * rate + live.physicalBytes % 1000 * rate / 1000) add("billed ${live.billedBytes} is not physical x rate")
                }
            }, "this connection showed ${live?.physicalBytes} bytes, billed ${live?.billedBytes} at ${live?.ratePermille}‰ (before the last report settled)")
            perRate += "$rate‰: ${m.units} units for ${m.serverUp + m.serverDown} bytes"
        }
        if (perRate.size == 2) record("B01-rates", "PASS", perRate.joinToString(" | "))

        // ---- N01: the ongoing notification names the line and shows live rates (its own connection,
        // so the extra bytes stay out of the B01 segments).
        run {
            main { c.selectNow(nodeIds.getValue("dedicated")) }
            // Selected right after B01's disconnect: the catalog revision has just moved with the
            // lease summary (the list is the same), which must not make the selection stale.
            val chosen = selected(c, "dedicated")
            val refusal = c.state.value.message?.code
            main { c.connectNow() }
            val text = if (c.state.value.snapshot.state == State.CONNECTED) notificationWhileDownloading() else ""
            main { c.disconnectNow() }
            val gone = waitUntil(10_000) { notification(VpnNotifications.ID) == null }
            val live = Regex("↓([1-9][0-9.]*) ").containsMatchIn("$text ")
            if (!chosen) record("N01-select", "FAIL", "selecting the line right after a disconnect was refused: $refusal")
            record("N01-notification", if (text.contains(names.getValue("dedicated")) && text.contains("↑") && live && gone != null) "PASS" else "FAIL",
                "during a download the ongoing notification read \"$text\"; removed ${gone} ms after Disconnect")
        }

        // ---- C01: Connect right after Disconnect waits for the old teardown instead of being
        // refused, and the old teardown never ends the new tunnel (five times in a row).
        run {
            val seen = mutableListOf<String>()
            main { c.connectNow() }
            repeat(5) {
                main { c.disconnectNow() }
                val started = SystemClock.elapsedRealtime()
                main { c.connectNow() }
                val connected = c.state.value.snapshot.state == State.CONNECTED
                // Still connected a moment later: no late teardown of the previous service took it down.
                delay(1_500)
                val held = c.state.value.snapshot.state == State.CONNECTED && vpnNetworkUp()
                seen += if (connected && held) "${SystemClock.elapsedRealtime() - started - 1_500} ms" else "${c.state.value.snapshot.state}/${c.state.value.message?.code}"
            }
            main { c.disconnectNow() }
            record("C01-reconnect-at-once", if (seen.all { it.endsWith(" ms") }) "PASS" else "FAIL",
                "Connect issued the moment Disconnect returned, 5 times: ${seen.joinToString()}")
        }

        // ---- B02: one real acknowledgement is lost; the identical batch is resent and charged once.
        run {
            val m = measure("B02", c, "meter", "dedicated", 1L shl 20, 4L shl 20) {
                val seen = trafficReports().size
                world("drop_ack")
                download(sink.str("name"), 2L shl 20)
                // The next report (every ${report interval}) is the one whose reply is lost.
                waitUntil(30_000) {
                    val r = trafficReports().drop(seen)
                    val dropped = r.indexOfFirst { it.getValue("dropped").jsonPrimitive.boolean }
                    dropped >= 0 && r.drop(dropped + 1).any { it.str("token_sha256_prefix") == r[dropped].str("token_sha256_prefix") }
                }
            } ?: return@run
            val reports = trafficReports()
            val dropped = reports.indexOfLast { it.getValue("dropped").jsonPrimitive.boolean }
            val next = reports.drop(dropped + 1).firstOrNull { it.str("token_sha256_prefix") == reports.getOrNull(dropped)?.str("token_sha256_prefix") }
            when {
                dropped < 0 -> record("B02-retry", "FAIL", "no /traffic report was dropped")
                next == null -> record("B02-retry", "FAIL", "the dropped batch was never sent again")
                else -> record("B02-retry", if (next.str("body") == reports[dropped].str("body")) "PASS" else "FAIL",
                    "dropped ${reports[dropped].str("body")} (committed with ${reports[dropped].getValue("status").jsonPrimitive.int}); next ${next.str("body")} -> ${next.getValue("status").jsonPrimitive.int}")
            }
            verdict("B02-once", ledgerProblems(m, 1000), "the batch whose reply was lost is charged once: ${m.units} units for ${m.serverUp + m.serverDown} bytes, server = Core counters")
        }

        // ---- A04: an administrator re-rates the connected line: it stops, the old segment settles at the old rate.
        c = signIn("change")
        stopCase("A04-rate", c, "change", "changing", 1000, setOf("profile_changed")) { world("edit", """{"node":"changing","rate_permille":2000}""") }
        run {
            val rated = waitFor(c, 10_000) { st -> st.snapshot.nodes.any { it.id == nodeIds.getValue("changing") && it.ratePermille == 2000L } }
            main { c.selectNow(nodeIds.getValue("changing")) }
            main { c.connectNow() }
            val s = c.state.value
            val bound = s.snapshot.metering?.ratePermille
            main { c.disconnectNow() }
            record("A04-rebind", if (rated != null && s.snapshot.state == State.CONNECTED && bound == 2000L) "PASS" else "FAIL",
                "new rate listed after $rated ms; the next connection ${s.snapshot.state} bound at $bound‰ (${s.message?.code})")
        }
        // ---- A04: the line is disabled while connected.
        stopCase("A04-disable", c, "change", "changing", 2000, setOf("client_config_unavailable", "node_unavailable")) {
            world("edit", """{"node":"changing","enabled":false}""")
        }
        run {
            val gone = waitFor(c, 10_000) { st -> st.snapshot.nodes.none { it.id == nodeIds.getValue("changing") } }
            val s = c.state.value
            record("A04-disable-gone", if (gone != null && !s.connection.canConnect) "PASS" else "FAIL",
                "the disabled line left the list after $gone ms; connect enabled ${s.connection.canConnect} (${s.connection.blocker?.code})")
        }

        // ---- A04: the plan ends while connected; the stop alert says why.
        c = signIn("expire")
        VpnNotifications.clearStopped(context)
        stopCase("A04-expire", c, "expire", "dedicated", 1000, setOf("subscription_expired")) { world("expire", """{"user":"expire"}""") }
        run {
            val alertAfter = waitUntil(5_000) { notification(VpnNotifications.ALERT_ID) != null }
            val alert = notification(VpnNotifications.ALERT_ID)?.text().orEmpty()
            val reason = c.state.value.snapshot.lastError?.message.orEmpty()
            val s = c.state.value
            record("N02-stop-alert", if (alertAfter != null && reason.isNotEmpty() && alert == reason) "PASS" else "FAIL",
                "alert notification after the Core stopped the tunnel: \"$alert\" (Core reason \"$reason\")")
            record("A04-expire-blocked", if (!s.connection.canConnect && s.snapshot.entitlement?.accountAuthorized == false) "PASS" else "FAIL",
                "after the plan ended: account authorized ${s.snapshot.entitlement?.accountAuthorized}, reason ${s.snapshot.entitlement?.reason}, connect enabled ${s.connection.canConnect}")
        }

        // ---- A04: the data runs out while connected (the app keeps downloading).
        world("quota", """{"user":"quota","remaining_bytes":${12L shl 20}}""")
        c = signIn("quota")
        stopCase("A04-quota", c, "quota", "dedicated", 1000, setOf("quota_exhausted"), drive = true) { }
        run {
            val l = ledger("quota")
            val s = c.state.value
            record("A04-quota-blocked", if (l.remainingBytes == 0L && !s.connection.canConnect) "PASS" else "FAIL",
                "server remaining ${l.remainingBytes} of ${l.totalBytes} bytes (used ${l.usedUnits / 1000} bytes: overshoot ${l.usedUnits / 1000 - l.totalBytes} bytes, " +
                    "the client-reported meter's last interval); the app shows remaining ${s.snapshot.entitlement?.remainingBytes}, connect enabled ${s.connection.canConnect} (${s.snapshot.entitlement?.reason})")
        }

        // ---- B04: a node-reported line, through the real node agent: no client report, the node bills.
        nodeReported()

        // ---- P01-P05: online presence (node groups round 6).
        presenceCases()
    }

    /**
     * Connect on [node], move a little data, then [trigger] a server-side change (or, with
     * [drive], keep downloading until the quota is gone) and require: the tunnel stops by itself
     * with one of [reasons], the VPN network and the service are gone, the tail settles, and the
     * whole connection is billed at [rate] (the rate it was bound to).
     */
    private suspend fun stopCase(id: String, c: SessionController, who: String, node: String, rate: Long, reasons: Set<String>, drive: Boolean = false, trigger: suspend () -> Unit) {
        main { c.selectNow(nodeIds.getValue(node)) }
        if (!selected(c, node)) {
            record(id, "FAIL", "line not selected: ${c.state.value.message?.code} ${c.state.value.snapshot.lastError?.code}")
            return
        }
        val before = ledger(who)
        main { c.connectNow() }
        if (c.state.value.snapshot.state != State.CONNECTED) {
            record(id, "FAIL", "did not connect: ${c.state.value.message?.code} ${c.state.value.snapshot.lastError?.code}")
            return
        }
        runCatching { download(sink.str("name"), 1L shl 20) }
        trigger()
        val began = SystemClock.elapsedRealtime()
        var stoppedAfter: Long? = null
        while (SystemClock.elapsedRealtime() - began < 90_000) {
            if (c.state.value.snapshot.state != State.CONNECTED) {
                stoppedAfter = SystemClock.elapsedRealtime() - began
                break
            }
            if (drive) runCatching { download(sink.str("name"), 2L shl 20) } else delay(100)
        }
        if (stoppedAfter == null) {
            main { c.disconnectNow() }
            record(id, "FAIL", "still connected 90 s after the change")
            return
        }
        val settled = waitFor(c, 20_000) { !it.snapshot.tunnelBusy && it.snapshot.metering?.pending != true }
        val vpnGone = waitUntil(10_000) { !vpnNetworkUp() && container.vpnHosts.current == null }
        val s = c.state.value
        val code = s.snapshot.lastError?.code.orEmpty()
        val after = ledger(who)
        val counted = (after.uploadBytes - before.uploadBytes) + (after.downloadBytes - before.downloadBytes)
        val units = after.usedUnits - before.usedUnits
        verdict(id, buildList {
            if (code !in reasons) add("stopped with \"$code\", expected one of $reasons")
            if (settled == null) add("the tail never settled (pending ${s.snapshot.metering?.pending})")
            if (vpnGone == null) add("VPN network up ${vpnNetworkUp()}, service running ${container.vpnHosts.current != null} 10 s after the stop")
            if (units != counted * rate) add("charged $units units for $counted bytes, expected x $rate")
        }, "stopped by itself ${stoppedAfter} ms after the change with \"$code\" (${s.snapshot.lastError?.message}); tail settled, VPN and service gone after ${vpnGone} ms; " +
            "the segment's $counted bytes charged $units units at its own $rate‰")
    }

    // ---------------------------------------------------------------- online presence (node groups round 6)

    private data class Lease(val lease: String, val holder: Boolean, val server: String, val label: String, val line: String, val reason: String)

    private data class Presence(val live: List<Lease>, val ended: List<Lease>) {
        val own: Lease? get() = live.firstOrNull { !it.holder }
    }

    /** The server's own view of the presence account: live leases and the latest ended ones. */
    private fun presence(): Presence {
        val o = Json.parseToJsonElement(world("presence", """{"user":"presence"}""")).jsonObject
        fun list(key: String) = o.getValue(key).jsonArray.map { e ->
            val l = e.jsonObject
            fun s(k: String) = l[k]?.jsonPrimitive?.content.orEmpty()
            Lease(s("lease"), l["holder"]?.jsonPrimitive?.boolean == true, s("server"), s("label"), s("line"), s("end_reason"))
        }
        return Presence(list("live"), list("ended"))
    }

    private suspend fun waitPresence(millis: Long, predicate: (Presence) -> Boolean): Pair<Long?, Presence> {
        val start = SystemClock.elapsedRealtime()
        var last = presence()
        while (SystemClock.elapsedRealtime() - start < millis) {
            last = presence()
            if (predicate(last)) return (SystemClock.elapsedRealtime() - start) to last
            delay(100)
        }
        return null to last
    }

    /**
     * P01-P05 (handoff_node_use_history_and_load_balancing_c.md round 6) on the device: the
     * device limit when connecting, one automatic reconnect after the administrator withdraws the
     * server this phone is on (in the foreground and with the app in the background, where only
     * the /traffic replies carry the news), and the online slot released by Disconnect and by
     * signing out. The account is "presence"; the line "pair" is one node group of two servers.
     */
    private suspend fun presenceCases() {
        if (!nodeIds.containsKey("pair")) {
            record("P", "BLOCKED", "this billing world has no two-server line (backend older than node groups round 6)")
            return
        }
        try {
            runPresence()
        } finally {
            container.core.setForeground(true)
            runCatching { world("vacate", """{"user":"presence"}""") }
            runCatching { world("restore") }
        }
    }

    private suspend fun runPresence() {
        val lease = world.getValue("lease_seconds").jsonPrimitive.long * 1000
        val c = signIn("presence")
        // ---- P01: two other devices of the account are online: this phone is refused at connect.
        world("occupy", """{"user":"presence","count":2}""")
        main { c.selectNow(nodeIds.getValue("pair")) }
        main { c.connectNow() }
        var s = c.state.value
        var p = presence()
        val want = "已达到同时在线设备上限，请先断开其他设备"
        verdict("P01-device-limit", buildList {
            if (s.snapshot.state == State.CONNECTED) add("connected with two other devices online")
            if (s.message?.code != "device_limit" || s.message?.text != want) add("the app says ${s.message}")
            if (vpnNetworkUp() || container.vpnHosts.current != null) add("VPN network ${vpnNetworkUp()}, service ${container.vpnHosts.current != null} after the refusal")
            if (p.live.size != 2 || p.own != null) add("server: ${p.live.size} live, own ${p.own}")
        }, "two of two slots held by other devices: Connect refused, the app shows \"${s.message?.text}\" (code ${s.message?.code}); no VPN network or service left; the server still counts 2 online, none of them this phone")

        // ---- P01: one of them disconnects; this phone is admitted at once.
        world("vacate", """{"user":"presence","count":1}""")
        val started = SystemClock.elapsedRealtime()
        main { c.connectNow() }
        val took = SystemClock.elapsedRealtime() - started
        s = c.state.value
        p = presence()
        verdict("P01-after-release", buildList {
            if (s.snapshot.state != State.CONNECTED || !vpnNetworkUp()) add("not connected: ${s.snapshot.state} ${s.message?.code} VPN ${vpnNetworkUp()}")
            if (p.live.size != 2 || p.own?.line != "pair") add("server: ${p.live.size} live, own ${p.own}")
        }, "after another device disconnected this phone connected in $took ms; the server counts 2 of 2, this phone on ${p.own?.label}")
        if (s.snapshot.state != State.CONNECTED) return
        world("vacate", """{"user":"presence"}""")

        // ---- P03: the administrator disables this phone's server, in the foreground and then
        // with the app in the background (the catalog long-poll paused).
        if (!reassignCase("P03-reassign", c, background = false)) return
        world("restore")
        reassignCase("P03-reassign-background", c, background = true)
        container.core.setForeground(true)

        // The ledger is continuous across the moves: server = the Core's confirmed counters.
        main { c.disconnectNow() }
        waitFor(c, 15_000) { !it.snapshot.tunnelBusy && it.snapshot.metering?.pending != true }
        val m = c.state.value.snapshot.metering
        val l = ledger("presence")
        verdict("P03-ledger", buildList {
            if (m == null || m.pending || l.uploadBytes != m.confirmedUploadBytes || l.downloadBytes != m.confirmedDownloadBytes) add("server ${l.uploadBytes}/${l.downloadBytes}, Core ${m?.confirmedUploadBytes}/${m?.confirmedDownloadBytes} pending ${m?.pending}")
            if (l.downloadBytes < 4L shl 20) add("the server counted ${l.downloadBytes} download bytes for 4 MiB through three servers")
        }, "after two moves and a disconnect: server up ${l.uploadBytes} / down ${l.downloadBytes} = the Core's confirmed counters, nothing pending")
        world("restore")

        // ---- P04 / P05: Disconnect and signing out release the slot at once (not by expiry).
        releaseCase("P04-disconnect-release", c, lease) { main { c.disconnectNow() } }
        releaseCase("P05-signout-release", c, lease) { main { c.signOutNow() } }
    }

    /**
     * Connect on the pair line, download 1 MiB, withdraw the server this phone is on, and require
     * one automatic reconnect to the other server: the app never shows an idle tunnel meanwhile
     * (it would stop its VPN service), the service stays, and the tunnel forwards again.
     */
    private suspend fun reassignCase(id: String, c: SessionController, background: Boolean): Boolean {
        if (c.state.value.snapshot.state != State.CONNECTED) {
            main { c.selectNow(nodeIds.getValue("pair")) }
            main { c.connectNow() }
        }
        val (_, before) = waitPresence(5_000) { it.own != null }
        val old = before.own
        if (c.state.value.snapshot.state != State.CONNECTED || old == null) {
            record(id, "FAIL", "could not connect first: ${c.state.value.message?.code} ${c.state.value.snapshot.lastError?.code}, own lease $old")
            return false
        }
        val first = runCatching { download(sink.str("name"), 1L shl 20) }.getOrNull()
        if (background) container.core.setForeground(false)
        val idle = CopyOnWriteArrayList<String>()
        val serviceLost = AtomicBoolean(false)
        val sawMessage = AtomicBoolean(false)
        val sampler = CoroutineScope(Dispatchers.Default).launch {
            while (true) {
                val st = c.state.value
                if (!st.snapshot.tunnelBusy) idle += st.snapshot.state
                if (container.vpnHosts.current == null || container.vpnHosts.current?.isEnding == true) serviceLost.set(true)
                if (st.snapshot.lastError?.code == "connection_reassigned") sawMessage.set(true)
                delay(20)
            }
        }
        val withdrawn = SystemClock.elapsedRealtime()
        world("withdraw", """{"user":"presence"}""")
        val (moved, after) = waitPresence(30_000) { it.own != null && it.own?.server != old.server }
        val reconnected = waitFor(c, 20_000) { it.snapshot.state == State.CONNECTED }
        val total = SystemClock.elapsedRealtime() - withdrawn
        delay(300)
        sampler.cancel()
        val second = runCatching { download(sink.str("name"), 1L shl 20) }
        val endedOld = after.ended.firstOrNull { it.lease == old.lease }?.reason
        verdict(id, buildList {
            if (moved == null) add("this phone is not on another server: ${after.own}")
            if (reconnected == null) add("not connected again: ${c.state.value.snapshot.state} ${c.state.value.snapshot.lastError?.code}")
            if (idle.isNotEmpty()) add("the app saw an idle tunnel ${idle.size} times during the reconnect (${idle.first()})")
            if (serviceLost.get()) add("the VPN service stopped during the reconnect")
            if (first != 1L shl 20 || second.getOrNull() != 1L shl 20) add("downloads before/after: $first / ${second.getOrNull() ?: second.exceptionOrNull()?.message}")
            if (!vpnNetworkUp()) add("no VPN network after the reconnect")
            if (endedOld != "node_unavailable") add("the withdrawn lease ended \"$endedOld\"")
        }, "${if (background) "in the background: " else ""}server ${old.label} disabled; this phone was on ${after.own?.label} after $moved ms and connected again after $total ms " +
            "(reassignment message seen: ${sawMessage.get()}); never idle and the VPN service kept (sampled every 20 ms); the old lease ended node_unavailable; 1 MiB downloads before and after")
        return moved != null && reconnected != null
    }

    private suspend fun releaseCase(id: String, c: SessionController, leaseMillis: Long, end: suspend () -> Unit) {
        if (c.state.value.snapshot.state != State.CONNECTED) {
            main { c.selectNow(nodeIds.getValue("pair")) }
            main { c.connectNow() }
        }
        val (_, online) = waitPresence(5_000) { it.own != null }
        if (online.own == null) {
            record(id, "FAIL", "not online first: ${c.state.value.snapshot.state} ${c.state.value.message?.code}")
            return
        }
        val began = SystemClock.elapsedRealtime()
        end()
        val returned = SystemClock.elapsedRealtime() - began
        val (offline, p) = waitPresence(10_000) { it.own == null }
        val reason = p.ended.firstOrNull { it.lease == online.own?.lease }?.reason
        verdict(id, buildList {
            if (offline == null) add("still online 10 s later")
            if (reason != "released") add("ended \"$reason\", expected released")
            if (offline != null && returned + offline >= leaseMillis) add("offline only after ${returned + offline} ms, not before the ${leaseMillis} ms lease ran out")
        }, "the call returned in $returned ms; the server showed this phone offline (released) $offline ms after it, inside one lease ($leaseMillis ms)")
    }

    private suspend fun nodeReported() {
        if (!world.getValue("node_reported").jsonPrimitive.boolean) {
            record("B04", "BLOCKED", "no node agent in this world (the suite builds native/nodeagent; see its output)")
            return
        }
        val target = world.str("agent_target")
        if (!world.getValue("agent_target_resolves").jsonPrimitive.boolean) {
            record("B04", "BLOCKED", "this host's resolver does not map $target to loopback (RFC 6761), so the node cannot reach the sink by name")
            return
        }
        val c = signIn("grant")
        val agent = nodeIds.getValue("agent")
        val listed = waitFor(c, 10_000) { st -> st.snapshot.nodes.any { it.id == agent && it.meteringSource == "node_reported" } }
        if (listed == null) {
            record("B04-connect", "FAIL", "the node-reported line is not listed: ${c.state.value.snapshot.nodes.map { "${it.name}/${it.meteringSource}" }}")
            return
        }
        main { c.selectNow(agent) }
        val before = ledger("grant")
        val (sinkUp0, sinkDown0) = sinkStats()
        val reportsBefore = trafficReports().size
        main { c.connectNow() }
        val s = c.state.value
        if (s.snapshot.state != State.CONNECTED) {
            record("B04-connect", "FAIL", "state ${s.snapshot.state}: ${s.message?.code} ${s.message?.text} ${s.snapshot.lastError?.code}")
            return
        }
        val rate = s.snapshot.metering?.ratePermille ?: 0
        record("B04-connect", if (s.snapshot.metering?.source == "node_reported") "PASS" else "FAIL",
            "connected through a grant on the agent's forwarder; metering source ${s.snapshot.metering?.source}, rate $rate‰")
        val probe = Json.parseToJsonElement(world("reject_probe")).jsonObject
        val moved = probe.getValue("ledger_moved").jsonPrimitive.boolean
        record("B04-reject", if (probe.getValue("status").jsonPrimitive.int == 409 && probe.str("code") == "metering_source_mismatch" && !moved) "PASS" else "FAIL",
            "a client report on this node-reported session -> ${probe.getValue("status").jsonPrimitive.int} ${probe.str("code")}; ledger moved $moved")
        val resolved = runCatching { InetAddress.getAllByName(target).toList() }.getOrDefault(emptyList())
        val fake = resolved.isNotEmpty() && resolved.all { it is Inet4Address && (it.address[0].toInt() and 0xff) == 198 && (it.address[1].toInt() and 0xfe) == 18 }
        if (!fake) {
            main { c.disconnectNow() }
            record("B04-node-bills", "BLOCKED", "Android resolved $target to $resolved instead of the tunnel's fake-ip range, so the bytes would not cross the node")
            return
        }
        val up = 2L shl 20
        val down = 8L shl 20
        val gotUp = runCatching { upload(target, up) }
        val gotDown = runCatching { download(target, down) }
        delay(2_000)
        main { c.disconnectNow() }
        waitFor(c, 15_000) { !it.snapshot.tunnelBusy }
        // The agent reports usage every second; wait for the ledger to stop moving.
        var after = ledger("grant")
        var stable = 0
        val deadline = SystemClock.elapsedRealtime() + 30_000
        while (SystemClock.elapsedRealtime() < deadline && stable < 3) {
            delay(1_000)
            val now = ledger("grant")
            stable = if (now.usedUnits == after.usedUnits && now.usedUnits > before.usedUnits) stable + 1 else 0
            after = now
        }
        val (sinkUp1, sinkDown1) = sinkStats()
        val reports = trafficReports().drop(reportsBefore)
        val units = after.usedUnits - before.usedUnits
        val body = up + down
        val low = body * rate
        val high = (body + body / 50 + background) * rate
        record("B04-no-report", if (reports.isEmpty()) "PASS" else "FAIL", "client /traffic reports during the node-reported connection: ${reports.size}")
        verdict("B04-node-bills", buildList {
            if (gotUp.getOrNull() != up || gotDown.getOrNull() != down) add("transfer: upload ${gotUp.getOrNull() ?: gotUp.exceptionOrNull()?.message}, download ${gotDown.getOrNull() ?: gotDown.exceptionOrNull()?.message}")
            if (sinkUp1 - sinkUp0 != up || sinkDown1 - sinkDown0 != down) add("the sink saw up ${sinkUp1 - sinkUp0} down ${sinkDown1 - sinkDown0}")
            if (units < low || units > high) add("the node billed $units units, expected $low..$high (body x $rate‰, + 2% + ${background} B)")
            if (after.remainingBytes != maxOf(0, (after.totalBytes * 1000 - after.usedUnits) / 1000)) add("remaining does not follow the formula")
        }, "the node alone billed $units units for a $body byte body at $rate‰ (${pct(units / maxOf(rate, 1), body)} framing and background); sink exact")
    }
}
