package com.asterlink.vpn.session

import com.asterlink.vpn.core.CoreProtocol
import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.core.GateRecord
import com.asterlink.vpn.core.GateView
import com.asterlink.vpn.core.SnapshotReply
import java.io.File
import java.time.Instant
import java.time.ZoneOffset
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/** Contract decoding (snapshot-v1, native-v1/v2 codes) and the pure presentation helpers. */
class PresentationTest {
    private val contracts = File(requireNotNull(System.getProperty("asterlink.contracts")))

    private fun fixtures(): Map<String, CoreSnapshot> {
        val root = Json.parseToJsonElement(contracts.resolve("snapshot-v1.json").readText()).jsonObject
        return root.getValue("snapshots").jsonObject.mapValues { (_, value) ->
            CoreProtocol.json.decodeFromJsonElement(CoreSnapshot.serializer(), value)
        }
    }

    @Test
    fun everySharedSnapshotDecodesStrictly() {
        val all = fixtures()
        assertTrue(all.size >= 5)
        val connected = all.getValue("connected_metering")
        assertEquals(104_857_600_000L, connected.entitlement!!.usedUnits)
        assertEquals(3, connected.metering!!.history.size)
        assertEquals("node_reported", all.getValue("connected_node_reported").nodes.single().meteringSource)
        assertEquals(Instant.parse("2030-01-01T00:10:00Z"), all.getValue("ready_with_selection").entitlement!!.subscriptionExpires)
        assertNull(all.getValue("signed_out_after_expiry").user)
    }

    @Test
    fun aReplySplitsActionErrorOffAndStaysStrict() {
        val body = Json.parseToJsonElement(contracts.resolve("snapshot-v1.json").readText())
            .jsonObject.getValue("snapshots").jsonObject.getValue("blocked_no_plan").jsonObject
        val withError = kotlinx.serialization.json.JsonObject(body + ("action_error" to Json.parseToJsonElement("""{"code":"device_limit","message":"x"}""")))
        val reply = SnapshotReply.parse(withError)
        assertEquals("device_limit", reply.actionError?.code)
        assertFalse(reply.succeeded)
        val unknown = kotlinx.serialization.json.JsonObject(body + ("server" to Json.parseToJsonElement("\"10.0.0.1\"")))
        try {
            SnapshotReply.parse(unknown)
            fail("an unknown snapshot field (a possible leak) was accepted")
        } catch (expected: SerializationException) {
        }
    }

    @Test
    fun countersAbove2pow53StayExact() {
        val big = 9_007_199_254_740_993L
        val snapshot = CoreProtocol.json.decodeFromString(CoreSnapshot.serializer(), """{"generation":$big,"state":"ready","entitlement":{"used_units":$big,"total_bytes":$big,"remaining_bytes":1}}""")
        assertEquals(big, snapshot.generation)
        assertEquals(big, snapshot.entitlement!!.usedUnits)
    }

    @Test
    fun purchaseReasonsAreServerCodes() {
        // The reasons that offer "buy or renew" must be codes the server really sends.
        val v1 = contracts.resolve("native-v1.json").readText()
        val v2 = Json.parseToJsonElement(contracts.resolve("native-v2.json").readText()).jsonObject
        val known = Regex("\"reason\": \"([a-z_]+)\"").findAll(v1).map { it.groupValues[1] }.toSet() +
            v2.getValue("errors").jsonObject.keys
        assertTrue("upgrade_required" in known)
        assertTrue("quota_exhausted" in known)
    }

    @Test
    fun connectionViewFollowsTheCoreVerdict() {
        val all = fixtures()
        val blocked = ConnectionView.from(all.getValue("blocked_no_plan"), busy = false, activity = SessionActivity.None)
        assertFalse(blocked.canConnect)
        assertTrue(blocked.needsPurchase)
        assertEquals(ConnectionStatus.Unavailable, blocked.status)
        assertEquals("upgrade_required", blocked.blocker?.code)

        val ready = ConnectionView.from(all.getValue("ready_with_selection"), busy = false, activity = SessionActivity.None)
        assertTrue(ready.canConnect)
        assertEquals(ConnectionStatus.Disconnected, ready.status)
        assertFalse(ConnectionView.from(all.getValue("ready_with_selection"), busy = true, activity = SessionActivity.None).canConnect)

        val unselected = all.getValue("ready_with_selection").copy(selectedNodeId = "")
        assertEquals(LocalCodes.SELECT_LINE, ConnectionView.from(unselected, false, SessionActivity.None).blocker?.code)
        val noLines = all.getValue("ready_with_selection").copy(nodes = emptyList())
        assertEquals(LocalCodes.NO_LINES, ConnectionView.from(noLines, false, SessionActivity.None).blocker?.code)
        val noCatalogYet = noLines.copy(catalogRevision = "")
        assertEquals(LocalCodes.SYNCING, ConnectionView.from(noCatalogYet, false, SessionActivity.None).blocker?.code)
        assertEquals(LocalCodes.SYNCING, NodeList.emptyReason(noCatalogYet, filtered = false))

        val connecting = ConnectionView.from(all.getValue("ready_with_selection"), busy = true, activity = SessionActivity.Connecting)
        assertEquals(ConnectionStatus.Preparing, connecting.status)
        assertTrue(connecting.cancellable)
        assertTrue(connecting.showProgress)

        val connected = ConnectionView.from(all.getValue("connected_metering"), busy = false, activity = SessionActivity.None)
        assertEquals(ConnectionStatus.Connected, connected.status)
        assertTrue(connected.showDisconnect)
        assertEquals(Tone.Good, connected.tone)

        val stopping = ConnectionView.from(all.getValue("connected_metering"), busy = true, activity = SessionActivity.Disconnecting)
        assertEquals(ConnectionStatus.Stopping, stopping.status)
        assertFalse(stopping.cancellable)
    }

    @Test
    fun nodeListSearchesFiltersAndGroupsInServerOrder() {
        val base = fixtures().getValue("ready_with_selection")
        val snapshot = base.copy(nodes = base.nodes + base.nodes[0].copy(id = "cccccccc-cccc-4ccc-8ccc-cccccccccccc", name = "HK Standard", lineType = "direct", ratePermille = 500))
        val labels: (Boolean) -> String = { if (it) "专线" else "普通线路" }
        val all = NodeList.build(snapshot, "", LineFilter.All, labels)
        assertEquals(listOf("HK", "JP"), all.map { it.region })
        assertEquals(listOf("HK Dedicated", "HK Standard"), all[0].rows.map { it.name })
        assertTrue(all[0].rows[0].selected && all[0].rows[0].configReady)
        assertEquals(listOf("HK Dedicated"), NodeList.build(snapshot, "", LineFilter.Dedicated, labels).flatMap { it.rows }.map { it.name })
        assertEquals(2, NodeList.build(snapshot, "", LineFilter.Direct, labels).flatMap { it.rows }.size)
        assertEquals(listOf("JP Standard"), NodeList.build(snapshot, "jp", LineFilter.All, labels).flatMap { it.rows }.map { it.name })
        assertEquals(1, NodeList.build(snapshot, "专线", LineFilter.All, labels).flatMap { it.rows }.size)
        assertTrue(NodeList.build(snapshot, "nothing", LineFilter.All, labels).isEmpty())
        assertEquals(LocalCodes.NO_MATCH, NodeList.emptyReason(snapshot, filtered = true))
        assertEquals(LocalCodes.NO_PLAN, NodeList.emptyReason(fixtures().getValue("blocked_no_plan"), filtered = false))
    }

    @Test
    fun formattingUsesIntegersAndBinaryUnits() {
        assertEquals("1×", Formatting.rate(1000))
        assertEquals("0.5×", Formatting.rate(500))
        assertEquals("1.25×", Formatting.rate(1250))
        assertEquals("0.001×", Formatting.rate(1))
        assertEquals("512 B", Formatting.bytes(512))
        assertEquals("1.00 GiB", Formatting.bytes(1L shl 30))
        assertEquals("68.20 GiB", Formatting.bytes(73_229_779_763L))
        assertEquals("0 B", Formatting.bytes(-5))
        assertEquals("1.00 MiB/s", Formatting.speed(1_048_576))
        // 20 MiB up + 80 MiB down at 0.5x = 52,428,800 billed bytes (handoff section 8).
        assertEquals(52_428_800L, Formatting.billedBytes(104_857_600L, 500))
        assertEquals(104_857_600L, Formatting.billedBytes(104_857_600L, 1000))
        assertEquals(Long.MAX_VALUE / 1000 * 1000 + 807, Formatting.billedBytes(Long.MAX_VALUE, 1000))
        assertEquals("00:12:31", Formatting.elapsed(751_000))
        assertEquals("1:00:00:05", Formatting.elapsed(86_405_000))
        val now = Instant.parse("2030-01-01T00:00:00Z")
        assertEquals(1L, Formatting.daysLeft(Instant.parse("2030-01-01T00:00:01Z"), now))
        assertEquals(0L, Formatting.daysLeft(Instant.parse("2029-12-31T00:00:00Z"), now))
        assertNull(Formatting.daysLeft(null, now))
        assertEquals("2030-01-01", Formatting.date(now, ZoneOffset.UTC))
        assertEquals(0.5f, Formatting.usedFraction(1000, 500))
        assertEquals("T", Formatting.initial("", "test@test.com"))
    }

    @Test
    fun diagnosticsNeverCarryAccountOrLineDetails() {
        val snapshot = fixtures().getValue("connected_metering")
        val text = Diagnostics.build("0.1.0", "development", "demo.example.invalid", "Android 16 (API 36) arm64-v8a", "0.1.0", "70f0570405c3c2c47bb113b88db95006d239b346", snapshot)
        for (secret in listOf("fixture@example.invalid", "HK Dedicated", "Fixture", "22222222-2222-4222-8222-222222222222", snapshot.selectedNodeId, snapshot.catalogRevision)) {
            assertFalse("diagnostics leaked $secret", text.contains(secret))
        }
        assertTrue(text.contains("state connected"))
    }

    @Test
    fun credentialAccountsAreScopedToApiBaseAndEmail() {
        assertEquals("https://a.example/api/v1/client|user@example.com", CredentialAccount.key("https://a.example/api/v1/client/", " User@Example.com "))
        assertFalse(CredentialAccount.key("https://a.example/api/v1/client", "u@x.io") == CredentialAccount.key("https://b.example/api/v1/client", "u@x.io"))
    }

    // ---- round 4 -------------------------------------------------------------------------------

    @Test
    fun theOngoingNotificationNamesTheLineAndTheRatesOnly() {
        val connected = fixtures().getValue("connected_metering")
        val state = SessionState(phase = AppPhase.SignedIn, snapshot = connected)
        val notice = TunnelNotice.from(state)
        assertTrue(notice.connected)
        val text = notice.text!!
        assertTrue(text, text.startsWith((connected.selectedNode?.name ?: "") ))
        assertTrue(text, text.contains("↑" + Formatting.speed(connected.metering!!.uploadBytesPerSecond)))
        assertTrue(text, text.contains("↓" + Formatting.speed(connected.metering!!.downloadBytesPerSecond)))
        // Not connected (or signed out): the title alone, no text.
        assertNull(TunnelNotice.from(state.copy(snapshot = connected.copy(state = CoreSnapshot.State.READY))).text)
        assertNull(TunnelNotice.from(state.copy(phase = AppPhase.SignedOut)).text)
        assertEquals("↑0 B/s ↓1.00 KiB/s", TunnelNotice(true, null, 0, 1024).text)
    }

    @Test
    fun aNodeReportedConnectionCountsFromZeroAClientReportedOneFromBeforeTheConnect() {
        val all = fixtures()
        val client = all.getValue("connected_metering")
        val before = client.copy(state = CoreSnapshot.State.READY, metering = client.metering!!.copy(uploadBytes = 10, downloadBytes = 20))
        assertEquals(com.asterlink.vpn.core.MeterSample(10, 20), ConnectionUsage.baseline(before, client))
        val node = all.getValue("connected_node_reported")
        assertEquals(com.asterlink.vpn.core.MeterSample(0, 0), ConnectionUsage.baseline(before, node))
        // A baseline above the counters (the Core restarted its meter) never shows negative usage.
        val usage = ConnectionUsage.of(client, com.asterlink.vpn.core.MeterSample(Long.MAX_VALUE, Long.MAX_VALUE))!!
        assertEquals(0L, usage.physicalBytes)
        assertNull(ConnectionUsage.of(before, null))
    }

    @Test
    fun theTileOnlyConnectsWhenTheConnectPageWould() {
        val all = fixtures()
        assertEquals(TileView(false, TileView.Action.OpenApp), TileView.from(SessionState(phase = AppPhase.SignedOut)))
        val connected = SessionState(phase = AppPhase.SignedIn, snapshot = all.getValue("connected_metering"))
        assertEquals(TileView(true, TileView.Action.Disconnect), TileView.from(connected))
        val ready = SessionState(phase = AppPhase.SignedIn, snapshot = all.getValue("ready_with_selection"))
        assertEquals(TileView.Action.Connect, TileView.from(ready).action)
        assertEquals(TileView.Action.Busy, TileView.from(ready.copy(busy = true, activity = SessionActivity.Connecting)).action)
        val noLine = ready.copy(snapshot = ready.snapshot.copy(selectedNodeId = ""))
        assertEquals(TileView.Action.OpenApp, TileView.from(noLine).action)
    }

    @Test
    fun aGatedTunnelIsStillConnectedButSaysItIsWaiting() {
        val gated = fixtures().getValue("connected_gated")
        assertEquals("gated", gated.tunnelHealth)
        assertEquals(GateView("lifecycle_reconfirmation_required", waitingNetwork = true, closedSeconds = 42, attempts = 3), gated.gate)
        assertEquals(GateRecord("network_changed", "reopened", 1_830, 1, 2), gated.lastGate)
        assertTrue(gated.tunnelBusy)
        val view = ConnectionView.from(gated, busy = false, activity = SessionActivity.None)
        assertEquals(ConnectionStatus.WaitingNetwork, view.status)
        assertEquals(Tone.Busy, view.tone)
        assertTrue(view.showDisconnect)
        assertFalse(view.showConnect)
        val reconfirming = ConnectionView.from(gated.copy(gate = gated.gate!!.copy(waitingNetwork = false)), busy = false, activity = SessionActivity.None)
        assertEquals(ConnectionStatus.Reconfirming, reconfirming.status)
        // Every other fixture has no gate: the field is Android-only and optional.
        fixtures().filterKeys { it != "connected_gated" }.values.forEach { assertNull(it.gate); assertNull(it.lastGate) }
    }

    @Test
    fun theNotificationOfAGatedTunnelShowsNoRates() {
        val gated = fixtures().getValue("connected_gated")
        val notice = TunnelNotice.from(SessionState(phase = AppPhase.SignedIn, snapshot = gated))
        assertEquals(TunnelNotice.GateState.WaitingNetwork, notice.gate)
        assertEquals("HK Dedicated", notice.text)
        val open = TunnelNotice.from(SessionState(phase = AppPhase.SignedIn, snapshot = fixtures().getValue("connected_metering")))
        assertEquals(TunnelNotice.GateState.Open, open.gate)
        assertTrue(open.text!!.contains("↑"))
    }
}
