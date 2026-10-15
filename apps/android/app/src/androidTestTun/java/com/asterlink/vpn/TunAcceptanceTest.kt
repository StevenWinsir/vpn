package com.asterlink.vpn

import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Bundle
import android.os.ParcelFileDescriptor
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.asterlink.core.mobile.Mobile
import com.asterlink.vpn.vpn.AsterVpnService
import com.asterlink.vpn.vpn.TunnelSession
import java.io.File
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Proxy
import java.net.Socket
import java.security.MessageDigest
import kotlin.random.Random
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import kotlinx.serialization.json.put
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The real Android TUN, end to end on an emulator (handoff_app_android.md 10, round 2):
 * AsterVpnService + VpnService.Builder + the production engine (template, fd hand-off,
 * protect, Verify) against a controlled test node on the host (cmd/asterlink-testnode at
 * 10.0.2.2) and a controlled target (cmd/asterlink-trafficsink at the host's own address,
 * which the node reaches directly; a reply that bypassed the node would also arrive, which is
 * what makes the UDP-reject case meaningful).
 *
 * Driven by scripts/test-native-android.py --suite tun --allow-network-changes, which builds
 * the hooks AAR, pre-grants VPN consent (appops ACTIVATE_VPN) and writes the run's
 * configuration into this app's private files (run-as, never argv); the file is read once and
 * deleted. Results go to files/tuntest/report.json, one entry per case, node hosts only as
 * hash prefixes. Only the emulator's network is changed.
 */
@RunWith(AndroidJUnit4::class)
class TunAcceptanceTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context = instrumentation.targetContext
    private val container = (context.applicationContext as AsterApp).container
    private val connectivity = context.getSystemService(ConnectivityManager::class.java)
    private val dir = File(context.filesDir, "tuntest")
    private val cases = mutableListOf<JsonObject>()
    private val secrets = mutableListOf<String>()
    private lateinit var config: JsonObject

    /** Leaves the service idle as host; the hooks drive the engine. */
    private val hostOnly = object : TunnelSession {
        override suspend fun connect(): String? = null
        override suspend fun disconnect() {
            Mobile.tunTestStop()
        }
    }

    @Before
    fun setUp() {
        val file = File(dir, "config.json")
        assumeTrue("no run configuration (scripts/test-native-android.py --suite tun writes it)", file.isFile)
        config = Json.parseToJsonElement(file.readText()).jsonObject
        file.delete()
        for (node in nodes().values) node["server"]?.jsonPrimitive?.content?.let(secrets::add)
        config["sink"]?.jsonObject?.get("token")?.jsonPrimitive?.content?.let(secrets::add)
        container.tunnelSession = hostOnly
        container.core.start()
    }

    @After
    fun tearDown() {
        if (!::config.isInitialized) return
        Mobile.tunTestStop()
        AsterVpnService.disconnect(context)
        waitFor(10_000) { container.vpnHosts.current == null }
    }

    // ------------------------------------------------------------------ the cases

    @Test
    fun acceptance() {
        try {
            run()
        } finally {
            restorePrivateDns()
            writeReport()
        }
        val failed = cases.filter { it["status"]?.jsonPrimitive?.content == "FAIL" }.map { it["case_id"]!!.jsonPrimitive.content }
        assertEquals("failed cases", emptyList<String>(), failed)
    }

    /** The victim of the driver's T09 force-stop: connect, announce, wait to be killed. */
    @Test
    fun holdForKill() {
        assumeTrue(config["hold"]?.jsonPrimitive?.boolean == true)
        startHost()
        val started = start(nodes().getValue("ss"), controlled = true)
        if (config["trace"]?.jsonPrimitive?.boolean == true) {
            var t = System.nanoTime()
            val a = systemA(sink().str("name"))
            status("trace_lookup", "${(System.nanoTime() - t) / 1_000_000} ms $a")
            t = System.nanoTime()
            val got = httpDown(sink().str("name"), 3)
            status("trace_get", "${(System.nanoTime() - t) / 1_000_000} ms -> $got")
        }
        val verified = if (started.ok) verify() else started
        if (!verified.ok) status("tun_error", verified.error.orEmpty())
        status("tun_ready", if (verified.ok) "1" else "0")
        Thread.sleep(180_000)
    }

    private fun run() {
        val consent = VpnService.prepare(context) == null
        val network = container.networkMonitor.current()
        val others = vpnNetworks()
        record("T-PRE", consent && network.available && others.isEmpty(),
            "consent pre-granted=$consent, physical network ${network.interfaceName ?: "none"}, other VPNs ${others.size}",
            blocked = !consent || !network.available || others.isNotEmpty())
        if (!consent || !network.available || others.isNotEmpty()) return

        // Without any VPN: the sink answers on its direct address (10.0.2.2, the host's
        // loopback), otherwise the UDP reject case proves nothing; and its name leads
        // somewhere else outside the node, so an answer by name can only have come through it.
        val direct = sink().str("direct_host")
        val baselineUdp = udpEcho(direct)
        val baselineHttp = httpDown(direct, 4096)
        val elsewhere = runCatching { InetAddress.getAllByName(sink().str("name")).none { it.isLoopbackAddress || it.hostAddress == direct } }.getOrDefault(true)
        record("T-BASE", baselineUdp && baselineHttp == 4096L && elsewhere,
            "before the tunnel: sink direct HTTP=${baselineHttp == 4096L} UDP=$baselineUdp; the target name does not lead to the sink outside the node=$elsewhere")

        startHost()
        record("T-SVC", container.vpnHosts.current != null, "AsterVpnService in the foreground and registered as the Core's VPN host")

        // ---- one full session over Shadowsocks
        val ss = nodes().getValue("ss")
        val begin = start(ss)
        if (!begin.ok) {
            record("T01a", false, "start failed: ${begin.error}")
            return
        }
        val vpn = waitFor(5_000) { vpnNetworks().firstOrNull() }
        val link = vpn?.let { connectivity.getLinkProperties(it) }
        val addresses = link?.linkAddresses?.map { it.address.hostAddress.orEmpty() }.orEmpty()
        val dns = link?.dnsServers?.map { it.hostAddress.orEmpty() }.orEmpty()
        record("T01a", vpn != null && link?.interfaceName?.startsWith("tun") == true && "198.18.0.1" in addresses && dns == listOf("198.18.0.2"),
            "VPN network up in ${begin.ms} ms on ${link?.interfaceName}, addresses $addresses, DNS $dns")
        val ipv6Mode = begin.ipv6Mode

        val verified = verify()
        record("T02-ss", verified.ok, if (verified.ok) "Verify through the TUN passed in ${verified.ms} ms (Shadowsocks AEAD)" else "Verify: ${verified.error}")

        val proxy = connectivity.defaultProxy
        record("T01-noproxy", proxy == null && System.getProperty("http.proxyHost").isNullOrEmpty(),
            "no system or JVM HTTP proxy is set (${proxy ?: "none"})")

        val size = config["download_bytes"]?.jsonPrimitive?.long ?: (4L shl 20)
        val node0 = nodeStats()
        val core0 = coreStatus()
        val (body, flow) = httpDownCounted(sink().str("name"), size)
        Thread.sleep(1_500) // trackers push their last chunk asynchronously
        val node1 = nodeStats()
        val core1 = coreStatus()
        val nodeDown = if (node0 != null && node1 != null) node1.getValue("download").jsonPrimitive.long - node0.getValue("download").jsonPrimitive.long else -1
        record("T01", body == size && nodeDown >= size,
            "an ordinary app request (no proxy) fetched $body of $size bytes; the node counted $nodeDown bytes down meanwhile (all apps)")
        // Every app shares the tunnel, so the totals include whatever else the device did
        // meanwhile (recorded only). The verdict is this download's own tracked flow: its
        // counter must cover the body plus HTTP headers and nothing more.
        val coreDown = core1.getValue("download").jsonPrimitive.long - core0.getValue("download").jsonPrimitive.long
        val (flowUp, flowDown) = flow ?: (-1L to -1L)
        record("B-COUNT", flowDown >= size && flowDown <= size + 4096 && flowUp > 0,
            "the download's own flow counted down $flowDown (x%.5f of the body), up $flowUp; device-wide proxy total down $coreDown meanwhile".format(flowDown.toDouble() / size))

        dnsCases()
        val node2 = nodeStats()
        val ntp = sntp()
        val node3 = nodeStats()
        val nodeUp = if (node2 != null && node3 != null) node3.getValue("upload").jsonPrimitive.long - node2.getValue("upload").jsonPrimitive.long else -1
        record("T03-udp", ntp, "an SNTP request (UDP/123, ${ntpServer()}) answered through the node (udp:true); node upload counter +$nodeUp bytes meanwhile")
        ipv6Cases(ipv6Mode)

        val api = config["api_host"]?.jsonPrimitive?.content
        if (api == null) {
            record("T05", false, "no api_host given", blocked = true)
        } else {
            val dial = result(Mobile.tunTestDial(api))
            record("T05", dial.ok, if (dial.ok) "the Core's control-plane dial reached ${hash(api)} by name in ${dial.ms} ms (protected, default-network resolvers)" else "dial: ${dial.error}")
        }
        idleLoop()

        stop()
        val gone = waitFor(5_000) { vpnNetworks().isEmpty() }
        record("T07", gone == true && !tunDescriptorOpen(), "after Stop: no VPN network and no /dev/tun descriptor in the process")

        // ---- protocol and policy variants, one session each
        variant("T02-vless", nodes()["vless"], expectVerify = true, what = "plain VLESS")
        variant("T02-reality", nodes()["reality"], expectVerify = true, what = "VLESS REALITY")
        variant("T02-badkey", nodes()["ss_bad"], expectVerify = false, what = "a wrong Shadowsocks password must not forward")
        udpReject()
        realNodes()
        cycles(ss)
    }

    /** [controlled]: Verify probes the sink behind the test node instead of the public probes. */
    private fun variant(id: String, node: JsonObject?, expectVerify: Boolean, what: String, controlled: Boolean = true) {
        if (node == null) {
            record(id, false, "$what: not configured for this run", blocked = true)
            return
        }
        val begin = start(node, controlled)
        val verified = if (begin.ok) verify() else begin
        stop()
        val ok = if (expectVerify) verified.ok else !verified.ok
        record(id, ok, "$what: start=${begin.ok} verify=${verified.ok}${verified.error?.let { " ($it)" } ?: ""}")
    }

    /** A node declared udp:false: UDP must be refused (MATCH,REJECT), never sent DIRECT. */
    private fun udpReject() {
        val node = nodes()["ss_noudp"] ?: return record("T06-udp-reject", false, "not configured", blocked = true)
        val begin = start(node, controlled = true)
        val tcp = begin.ok && verify().ok
        // The direct sink answers anyone who reaches it without the node (T-BASE), and the
        // time server answers anyone on the internet: a reply from either would be UDP sent
        // DIRECT instead of refused.
        val direct = begin.ok && udpEcho(sink().str("direct_host"))
        val ntp = begin.ok && sntp()
        stop()
        record("T06-udp-reject", tcp && !direct && !ntp,
            "udp:false node: TCP verify=$tcp, UDP answered by the direct sink=$direct, by the time server=$ntp (any answer would mean UDP was not refused)")
    }

    /** The user's own nodes, when the driver was given any (SS / VLESS REALITY on the internet). */
    private fun realNodes() {
        val real = config["real_nodes"]?.jsonArray.orEmpty()
        if (real.isEmpty()) {
            record("T02-real", false, "no real node provided (ASTERLINK_TUN_PROXY_JSON / ASTERLINK_TUN_REAL_NODES)", blocked = true)
            return
        }
        for ((i, entry) in real.withIndex()) {
            val node = entry.jsonObject
            node["server"]?.jsonPrimitive?.content?.let(secrets::add)
            val type = node["type"]?.jsonPrimitive?.content
            variant("T02-real-$i", node, expectVerify = true, what = "real $type node ${hash(node["server"]?.jsonPrimitive?.content.orEmpty())}", controlled = false)
        }
    }

    /**
     * Goroutines of the one known per-listener leak: the pinned Mihomo creates the TUN listener
     * with context.TODO(), and sing-tun's TCP NAT timeout loop only ends with that context
     * (stack_system_nat.go), so each start leaves one behind on every platform. It is counted
     * and reported on its own (docs/android-tun-design.md 9); any other growth fails.
     */
    private fun knownLeak(profile: String): Int = profile.split("\n\n").sumOf { block ->
        // A block is "<count> @ <pcs>" plus its frames; the first one follows the profile header.
        val count = Regex("(?m)^(\\d+) @").find(block)?.groupValues?.get(1)?.toIntOrNull() ?: 0
        if ("TCPNat).loopCheckTimeout" in block) count else 0
    }

    private fun cycles(node: JsonObject) {
        val n = config["cycles"]?.jsonPrimitive?.int ?: 20
        var goroutines0 = 0
        var fds0 = 0
        var known0 = 0
        val failures = mutableListOf<String>()
        for (i in 1..n) {
            val begin = start(node, controlled = true)
            if (!begin.ok) {
                failures += "cycle $i start: ${begin.error}"
                break
            }
            if (!verify().ok) failures += "cycle $i verify"
            stop()
            if (waitFor(5_000) { vpnNetworks().isEmpty() } != true) failures += "cycle $i left a VPN network"
            if (i == 1) {
                Thread.sleep(500)
                val s = coreStatus()
                goroutines0 = s.getValue("goroutines").jsonPrimitive.int
                fds0 = s.getValue("fds").jsonPrimitive.int
                val profile = Mobile.tunTestGoroutines()
                known0 = knownLeak(profile)
                File(dir, "goroutines-first.txt").writeText(profile)
            }
        }
        Thread.sleep(1_000)
        val s = coreStatus()
        val profile = Mobile.tunTestGoroutines()
        File(dir, "goroutines-last.txt").writeText(profile)
        val goroutines = s.getValue("goroutines").jsonPrimitive.int
        val fds = s.getValue("fds").jsonPrimitive.int
        val known = knownLeak(profile) - known0
        // Scheduler noise allowance of 3; the known leak is at most one per later cycle.
        if (goroutines - goroutines0 - known > 3 || known > n - 1 || fds - fds0 > 2) {
            failures += "growth: goroutines $goroutines0->$goroutines (known sing-tun NAT loop +$known), fds $fds0->$fds"
        }
        if (tunDescriptorOpen()) failures += "a /dev/tun descriptor is still open"
        record("T-CYCLES", failures.isEmpty(),
            "$n start/verify/stop cycles: goroutines $goroutines0->$goroutines of which +$known are the known sing-tun TCP NAT timeout loop " +
                "(one per listener, upstream), other growth ${goroutines - goroutines0 - known}; fds $fds0->$fds; failures $failures")
    }

    // ---- DNS: system resolver, port-53 hijack, Private DNS modes

    private var privateDnsBefore: Pair<String, String>? = null

    private fun dnsCases() {
        privateDnsBefore = shell("settings get global private_dns_mode").trim() to shell("settings get global private_dns_specifier").trim()
        for ((mode, specifier) in listOf("off" to null, "opportunistic" to null, "hostname" to "dns.google")) {
            shell("settings put global private_dns_mode $mode")
            if (specifier != null) shell("settings put global private_dns_specifier $specifier")
            Thread.sleep(2_000)
            // The system resolver (netd), asked for A records only and past its cache, so the
            // answer is this moment's. Fake-ip answers any name, so off/opportunistic use a
            // random one. Strict mode resolves for real over DoT, so it needs a name that
            // exists, and validating the DoT server can take a while: retry for up to 20 s.
            val name = if (specifier == null) "asterlink-${Random.nextLong().toULong().toString(16)}.example.org" else "www.google.com"
            var answer = systemA(name)
            val until = System.currentTimeMillis() + if (specifier == null) 0 else 20_000
            while (answer.isFailure && System.currentTimeMillis() < until) {
                Thread.sleep(1_000)
                answer = systemA(name)
            }
            val fake = answer.getOrNull()?.any { it.startsWith("198.18.") || it.startsWith("198.19.") } == true
            val observed = answer.fold({ "answer $it" }, { "lookup failed: ${(it.cause ?: it).let { c -> "${c.javaClass.simpleName} ${c.message}" }}" })
            when (mode) {
                // The system resolver asks the VPN's resolver, which the stack answers with fake-ip.
                "off", "opportunistic" -> record("T03-dns-$mode", fake, "Private DNS $mode: system resolver for a random name -> $observed (fake-ip expected)")
                // Strict DoT goes to dns.google:853 inside the tunnel (through the node); a real
                // answer here is that path, not a leak. Recorded, and it must resolve.
                else -> record("T03-dns-strict", answer.isSuccess || fake, "Private DNS strict (dns.google): $observed; ${if (fake) "answered by the tunnel resolver" else "DoT through the tunnel"}")
            }
        }
        restorePrivateDns()
        val hijacked = rawDnsQuery("9.9.9.9")
        record("T03-dns-any53", hijacked != false,
            when (hijacked) {
                true -> "a plain UDP query to 9.9.9.9:53 was answered with a fake-ip address (any:53 hijack)"
                null -> "a plain UDP query to 9.9.9.9:53 got no answer (blocked, not leaked)"
                false -> "9.9.9.9 answered for real: port-53 traffic left the tunnel"
            })
    }

    /**
     * IPv4 answers from the system resolver (getaddrinfo through netd, like any app). The name
     * is asked once: fake-ip answers A itself but forwards AAAA upstream, and for a name that
     * does not exist publicly the cached NXDOMAIN makes Android refuse a repeated lookup.
     */
    private fun systemA(name: String): Result<List<String>> =
        runCatching { InetAddress.getAllByName(name).filterIsInstance<Inet4Address>().map { it.hostAddress.orEmpty() } }

    private fun restorePrivateDns() {
        val (mode, specifier) = privateDnsBefore ?: return
        shell("settings put global private_dns_mode ${if (mode == "null" || mode.isEmpty()) "opportunistic" else mode}")
        if (specifier != "null" && specifier.isNotEmpty()) shell("settings put global private_dns_specifier $specifier")
        privateDnsBefore = null
    }

    /** true: fake-ip answer; false: a real answer (leak); null: no answer. */
    private fun rawDnsQuery(server: String): Boolean? {
        val id = Random.nextInt(0, 65535)
        val name = "asterlink-${Random.nextLong().toULong().toString(16)}.example.org"
        val query = java.io.ByteArrayOutputStream().apply {
            write(byteArrayOf((id shr 8).toByte(), id.toByte(), 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0))
            for (label in name.split('.')) {
                write(label.length)
                write(label.toByteArray())
            }
            write(byteArrayOf(0, 0, 1, 0, 1))
        }.toByteArray()
        return DatagramSocket().use { socket ->
            socket.soTimeout = 3_000
            socket.send(DatagramPacket(query, query.size, InetSocketAddress(server, 53)))
            val buffer = ByteArray(1500)
            val reply = DatagramPacket(buffer, buffer.size)
            try {
                socket.receive(reply)
            } catch (timeout: java.net.SocketTimeoutException) {
                return@use null
            }
            val a = firstA(buffer, reply.length)
            a != null && (a[0].toInt() and 0xff) == 198 && ((a[1].toInt() and 0xff) == 18 || (a[1].toInt() and 0xff) == 19)
        }
    }

    private fun firstA(b: ByteArray, length: Int): ByteArray? {
        if (length < 12) return null
        val answers = ((b[6].toInt() and 0xff) shl 8) or (b[7].toInt() and 0xff)
        var i = 12
        while (i < length && b[i].toInt() != 0) i += (b[i].toInt() and 0xff) + 1
        i += 5
        repeat(answers) {
            if (i + 12 > length) return null
            i += if ((b[i].toInt() and 0xc0) == 0xc0) 2 else { while (i < length && b[i].toInt() != 0) i += (b[i].toInt() and 0xff) + 1; 1 }
            val type = ((b[i].toInt() and 0xff) shl 8) or (b[i + 1].toInt() and 0xff)
            val rdlen = ((b[i + 8].toInt() and 0xff) shl 8) or (b[i + 9].toInt() and 0xff)
            i += 10
            if (type == 1 && rdlen == 4 && i + 4 <= length) return b.copyOfRange(i, i + 4)
            i += rdlen
        }
        return null
    }

    // ---- IPv6: carried by the node, or the family blocked so apps fall back at once

    private fun ipv6Cases(mode: String?) {
        val vpn = vpnNetworks().firstOrNull()
        val v6 = vpn?.let { connectivity.getLinkProperties(it) }?.linkAddresses?.any { it.address is java.net.Inet6Address } == true
        val started = System.nanoTime()
        val connected = runCatching {
            Socket().use { s -> s.connect(InetSocketAddress(InetAddress.getByName("2606:4700:4700::1111"), 80), 5_000) }
        }
        val ms = (System.nanoTime() - started) / 1_000_000
        when (mode) {
            "tunnel" -> {
                record("T04-ipv6", v6 && connected.isSuccess, "the node carries IPv6: the VPN has an IPv6 address ($v6) and an IPv6 connect through it succeeded in $ms ms")
                record("T04-ipv6-fallback", false, "the node carries IPv6, so the address-family block was not exercised", blocked = true)
            }
            else -> {
                record("T04-ipv6", false, "no IPv6 through the node (mode $mode): needs an IPv6 upstream for the emulator host", blocked = true)
                record("T04-ipv6-fallback", !v6 && connected.isFailure && ms < 1_000,
                    "IPv4-only node: the VPN has no IPv6 ($v6 = false expected); an IPv6 connect failed in $ms ms (${connected.exceptionOrNull()?.javaClass?.simpleName}), so apps fall back to IPv4")
            }
        }
    }

    /**
     * A routing loop is the Core's own outer connection (or its bootstrap lookup) re-entering
     * the tunnel it carries; it appears as a TUN flow towards the node or the bootstrap
     * resolver. That is the verdict. Unlike a desktop, every app on the device shares the
     * tunnel (a fresh emulator updates Play services and apps), so the idle byte count is
     * recorded together with the largest flows as evidence, not used as the test.
     */
    private fun idleLoop() {
        val forbidden = nodes().values.map { "${it.str("server")}:${it.getValue("port").jsonPrimitive.content}" }.toSet() +
            setOf("1.1.1.1:53", "8.8.8.8:53")
        Thread.sleep(2_000)
        val s0 = coreStatus()
        val seen = mutableSetOf<String>()
        repeat(6) {
            Thread.sleep(1_000)
            // Only flows that entered through the TUN: the template's own DNS-over-TCP to
            // 1.1.1.1/8.8.8.8 travels through the node as "Inner" connections, which is correct.
            coreStatus().getValue("flows").jsonArray.map { it.jsonObject }
                .filter { it.str("inbound") == "Tun" }
                .forEach { seen += it.str("dst") }
        }
        val s1 = coreStatus()
        val grown = (s1.getValue("upload").jsonPrimitive.long - s0.getValue("upload").jsonPrimitive.long) +
            (s1.getValue("download").jsonPrimitive.long - s0.getValue("download").jsonPrimitive.long)
        val loops = seen.filter { it in forbidden }
        val top = s1.getValue("flows").jsonArray.take(4).joinToString { f ->
            val o = f.jsonObject
            "${o.str("dst")} ${o.str("network")} ${(o.getValue("up").jsonPrimitive.long + o.getValue("down").jsonPrimitive.long) shr 10} KiB"
        }
        record("T05-loop", loops.isEmpty(),
            "no TUN flow towards the node or the bootstrap resolver in 6 s (${loops.ifEmpty { "none" }}); background traffic of other apps $grown bytes, largest flows: ${top.ifEmpty { "none" }}")
    }

    // ------------------------------------------------------------------ plumbing

    private data class Outcome(val ok: Boolean, val error: String?, val ms: Long, val ipv6Mode: String?)

    private fun result(raw: String): Outcome {
        val o = Json.parseToJsonElement(raw).jsonObject
        return Outcome(
            o.getValue("ok").jsonPrimitive.boolean,
            o["error"]?.jsonPrimitive?.content?.let(::redact),
            o["ms"]?.jsonPrimitive?.long ?: 0,
            o["ipv6_mode"]?.jsonPrimitive?.content,
        )
    }

    private fun startHost() {
        if (container.vpnHosts.current != null) return
        AsterVpnService.connect(context)
        waitFor(10_000) { container.vpnHosts.current }
    }

    /**
     * [controlled]: Verify fetches the sink behind the test node (deterministic, no public
     * internet); otherwise the production probes, which prove the node reaches the internet.
     */
    private fun start(node: JsonObject, controlled: Boolean = false): Outcome {
        startHost()
        val options = buildJsonObject {
            put("proxy", node)
            if (controlled) {
                val s = sink()
                put("probes", JsonArray(listOf(JsonPrimitive("http://${s.str("name")}:${s.str("port")}/down?t=${s.str("token")}&bytes=0"))))
            }
        }
        return result(Mobile.tunTestStart(options.toString()))
    }

    private fun verify() = result(Mobile.tunTestVerify())

    private fun stop() = result(Mobile.tunTestStop())

    private fun coreStatus(): JsonObject = Json.parseToJsonElement(Mobile.tunTestStatus()).jsonObject

    private fun nodes(): Map<String, JsonObject> = config.getValue("nodes").jsonObject.mapValues { it.value.jsonObject }

    private fun sink() = config.getValue("sink").jsonObject

    /**
     * Plain HTTP/1.0 over an ordinary socket, like any app's traffic. Not HttpURLConnection: the
     * app's network security config (rightly) refuses cleartext, and this test runs inside the
     * app. Calls [body] with the response body stream after a 200; -1 for anything else.
     */
    private fun <T> rawGet(host: String, port: Int, path: String, body: (java.io.InputStream) -> T): T? = runCatching {
        Socket(Proxy.NO_PROXY).use { socket ->
            socket.connect(InetSocketAddress(ipv4(host), port), 10_000)
            socket.soTimeout = 30_000
            socket.getOutputStream().write("GET $path HTTP/1.0\r\nHost: $host\r\n\r\n".toByteArray())
            val input = java.io.BufferedInputStream(socket.getInputStream())
            val head = StringBuilder()
            while (!head.endsWith("\r\n\r\n")) {
                val b = input.read()
                if (b < 0 || head.length > 8192) return@use null
                head.append(b.toChar())
            }
            if (!head.startsWith("HTTP/1.") || head.substring(9, 12) != "200") return@use null
            body(input)
        }
    }.getOrNull()

    /**
     * The IPv4 answer for [host]. The target name has a real AAAA record, which the system
     * resolver may list first; with an IPv4-only node the VPN blocks IPv6 by design, and the
     * test is about the IPv4 path (T04 covers the family block).
     */
    private fun ipv4(host: String): InetAddress =
        InetAddress.getAllByName(host).firstOrNull { it is Inet4Address } ?: throw java.net.UnknownHostException("no IPv4 address for $host")

    /**
     * Downloads [bytes] over HTTP/1.1 and keeps the connection open after the body (the sink
     * sends Content-Length), so this flow is still tracked when its counters are read: the
     * body length and (upload, download) of the Mihomo flow towards the sink's port.
     */
    private fun httpDownCounted(host: String, bytes: Long): Pair<Long, Pair<Long, Long>?> {
        val s = sink()
        val port = s.str("port").toInt()
        return runCatching {
            Socket(Proxy.NO_PROXY).use { socket ->
                socket.connect(InetSocketAddress(ipv4(host), port), 10_000)
                socket.soTimeout = 30_000
                socket.getOutputStream().write("GET /down?t=${s.str("token")}&bytes=$bytes HTTP/1.1\r\nHost: $host\r\n\r\n".toByteArray())
                val input = java.io.BufferedInputStream(socket.getInputStream())
                val head = StringBuilder()
                while (!head.endsWith("\r\n\r\n")) {
                    val b = input.read()
                    if (b < 0 || head.length > 8192) return@use -1L to null
                    head.append(b.toChar())
                }
                if (head.substring(9, 12) != "200") return@use -1L to null
                val buf = ByteArray(64 shl 10)
                var total = 0L
                while (total < bytes) {
                    val n = input.read(buf, 0, minOf(buf.size.toLong(), bytes - total).toInt())
                    if (n < 0) break
                    total += n
                }
                Thread.sleep(1_000) // trackers push their last chunk asynchronously
                val flow = coreStatus().getValue("flows").jsonArray.map { it.jsonObject }
                    .firstOrNull { it.str("dst") == "$host:$port" }
                    ?.let { it.getValue("up").jsonPrimitive.long to it.getValue("down").jsonPrimitive.long }
                total to flow
            }
        }.getOrDefault(-1L to null)
    }

    private fun httpDown(host: String, bytes: Long): Long {
        val s = sink()
        return rawGet(host, s.str("port").toInt(), "/down?t=${s.str("token")}&bytes=$bytes") { input ->
            val buf = ByteArray(64 shl 10)
            var total = 0L
            while (true) {
                val n = input.read(buf)
                if (n < 0) break
                total += n
            }
            total
        } ?: -1
    }

    /** The node's own counter, fetched by name through the tunnel (the node serves it on its loopback). */
    private fun nodeStats(): JsonObject? {
        val stats = config.getValue("node_stats").jsonObject
        return rawGet(sink().str("name"), stats.str("port").toInt(), "/stats?t=${stats.str("token")}") { input ->
            Json.parseToJsonElement(input.bufferedReader().readText()).jsonObject
        }
    }

    private fun udpEcho(host: String): Boolean {
        val s = sink()
        val payload = "t=${s.str("token")};ping-${Random.nextInt()}".toByteArray()
        return runCatching {
            DatagramSocket().use { socket ->
                socket.soTimeout = 3_000
                val target = InetSocketAddress(ipv4(host), s.str("udp_port").toInt())
                repeat(3) {
                    socket.send(DatagramPacket(payload, payload.size, target))
                    val buffer = ByteArray(2048)
                    val reply = DatagramPacket(buffer, buffer.size)
                    try {
                        socket.receive(reply)
                        if (buffer.copyOf(reply.length).contentEquals(payload)) return@use true
                    } catch (timeout: java.net.SocketTimeoutException) {
                        // retry
                    }
                }
                false
            }
        }.getOrDefault(false)
    }

    /**
     * One SNTP exchange (RFC 4330 client request, 48 bytes) with a public time server by IP.
     * UDP to a name is resolved by Mihomo on the client side, so a name-based target behind the
     * test node cannot be used for UDP; a public server reached through the node can.
     */
    private fun ntpServer() = config["ntp_server"]?.jsonPrimitive?.content ?: "162.159.200.123"

    private fun sntp(): Boolean = runCatching {
        DatagramSocket().use { socket ->
            socket.soTimeout = 3_000
            val request = ByteArray(48).also { it[0] = 0x1b }
            val target = InetSocketAddress(InetAddress.getByName(ntpServer()), 123)
            repeat(3) {
                socket.send(DatagramPacket(request, request.size, target))
                val buffer = ByteArray(128)
                val reply = DatagramPacket(buffer, buffer.size)
                try {
                    socket.receive(reply)
                    if (reply.length >= 48 && (buffer[0].toInt() and 0x07) == 4) return@use true
                } catch (timeout: java.net.SocketTimeoutException) {
                    // retry
                }
            }
            false
        }
    }.getOrDefault(false)

    private fun vpnNetworks() = connectivity.allNetworks.filter {
        connectivity.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
    }

    /** Whether any descriptor of this process still points at the TUN device. */
    private fun tunDescriptorOpen(): Boolean = File("/proc/self/fd").listFiles().orEmpty().any {
        runCatching { android.system.Os.readlink(it.path) }.getOrNull()?.contains("tun") == true
    }

    private fun shell(command: String): String {
        val pfd: ParcelFileDescriptor = instrumentation.uiAutomation.executeShellCommand(command)
        return ParcelFileDescriptor.AutoCloseInputStream(pfd).bufferedReader().use { it.readText() }
    }

    private fun <T> waitFor(millis: Long, probe: () -> T?): T? {
        val deadline = System.currentTimeMillis() + millis
        while (System.currentTimeMillis() < deadline) {
            probe()?.takeIf { it != false }?.let { return it }
            Thread.sleep(200)
        }
        return probe()
    }

    private fun record(id: String, ok: Boolean, observed: String, blocked: Boolean = false) {
        val status = if (blocked) "BLOCKED" else if (ok) "PASS" else "FAIL"
        cases += buildJsonObject {
            put("case_id", id)
            put("status", status)
            put("observed", redact(observed))
        }
        status(id, status)
    }

    private fun status(key: String, value: String) {
        instrumentation.sendStatus(0, Bundle().apply { putString("asterlink", "$key=$value") })
    }

    private fun redact(text: String): String = secrets.filter { it.isNotEmpty() }.fold(text) { acc, s -> acc.replace(s, "<redacted ${hash(s)}>") }

    private fun hash(s: String): String =
        MessageDigest.getInstance("SHA-256").digest(s.toByteArray()).take(4).joinToString("") { "%02x".format(it) }

    private fun writeReport() {
        dir.mkdirs()
        val facts = buildJsonObject {
            put("api", android.os.Build.VERSION.SDK_INT)
            put("abi", android.os.Build.SUPPORTED_ABIS.firstOrNull().orEmpty())
            put("core", JsonPrimitive(BuildConfig.VERSION_NAME))
        }
        File(dir, "report.json").writeText(JsonObject(mapOf("device" to facts, "cases" to JsonArray(cases))).toString())
    }

    private fun JsonObject.str(key: String) = getValue(key).jsonPrimitive.content
}
