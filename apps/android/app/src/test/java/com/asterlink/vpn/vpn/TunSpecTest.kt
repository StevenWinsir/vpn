package com.asterlink.vpn.vpn

import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test

class TunSpecTest {
    private val contract = Json.parseToJsonElement(
        File(requireNotNull(System.getProperty("asterlink.contracts"))).resolve("tun-spec-v1.json").readText(),
    ).jsonObject

    private fun specs(section: String) =
        contract.getValue(section).jsonArray.map { it.jsonObject.getValue("name").jsonPrimitive.content to it.jsonObject.getValue("spec").jsonPrimitive.content }

    @Test
    fun acceptsWhatTheCoreProducesAndTheContractAccepts() {
        for ((name, raw) in specs("produced") + specs("accept")) assertNotNull(name, TunSpec.parse(raw))
    }

    @Test
    fun refusesEveryContractRejection() {
        for ((name, raw) in specs("reject")) assertNull(name, TunSpec.parse(raw))
    }

    private class Recorder : TunSpec.Target {
        val calls = mutableListOf<String>()
        override fun addAddress(address: String, prefix: Int) { calls += "address $address/$prefix" }
        override fun addRoute(address: String, prefix: Int) { calls += "route $address/$prefix" }
        override fun addDnsServer(address: String) { calls += "dns $address" }
        override fun setMtu(mtu: Int) { calls += "mtu $mtu" }
    }

    /** Everything into the tunnel; IPv6 only with an IPv6 address, otherwise not at all. */
    @Test
    fun theBuilderGetsFullTunnelRoutesAndIpv6OnlyWhenCarried() {
        val (v4, v6) = specs("produced").map { TunSpec.parse(it.second)!! }.partition { !it.ipv6 }
        val ipv4Only = Recorder().also { v4.single().applyTo(it) }.calls
        assertEquals(listOf("address 198.18.0.1/30", "route 0.0.0.0/0", "dns 198.18.0.2", "mtu 9000"), ipv4Only)
        val dual = Recorder().also { v6.single().applyTo(it) }.calls
        assertEquals(ipv4Only + listOf("address fdfe:dcba:9876::1/126", "route ::/0"), dual)
    }

    @Test
    fun literalsAreParsedWithoutResolving() {
        assertArrayEquals(byteArrayOf(198.toByte(), 18, 0, 1), TunSpec.parseIpv4("198.18.0.1"))
        for (bad in listOf("198.18.0", "198.18.0.256", "01.2.3.4", "a.b.c.d", "1.2.3.4.5", "")) assertNull(bad, TunSpec.parseIpv4(bad))
        val v6 = TunSpec.parseIpv6("fdfe:dcba:9876::1")!!
        assertEquals(0xfd.toByte(), v6[0])
        assertEquals(1.toByte(), v6[15])
        assertNotNull(TunSpec.parseIpv6("1:2:3:4:5:6:7:8"))
        assertNotNull(TunSpec.parseIpv6("::1"))
        for (bad in listOf("1::2::3", "1:2:3:4:5:6:7:8:9", "12345::", "fe80::1%wlan0", "::ffff:1.2.3.4", "g::1", "1:2:3:4:5:6:7")) {
            assertNull(bad, TunSpec.parseIpv6(bad))
        }
    }
}
