package com.asterlink.vpn.vpn

import com.asterlink.vpn.core.CoreProtocol
import kotlinx.serialization.SerializationException
import kotlinx.serialization.Serializable

/**
 * The VPN interface the Core asks for (engine.TunSpec, shared/contracts/tun-spec-v1.json). It
 * is derived from the TUN section Mihomo runs, so applying it as-is keeps the Builder and the
 * stack on one address, MTU and resolver. Decoding is strict and the values are checked
 * without any name lookup: a document that is not exactly a tunnel description is refused
 * and the VPN is not established. It never carries node data.
 */
@Serializable
data class TunSpec(
    val address4: String,
    val prefix4: Int,
    val dns: String,
    val mtu: Int,
    val ipv6: Boolean,
    val address6: String? = null,
    val prefix6: Int? = null,
) {
    /** The calls VpnService.Builder receives; an interface so the mapping is tested on the JVM. */
    interface Target {
        fun addAddress(address: String, prefix: Int)
        fun addRoute(address: String, prefix: Int)
        fun addDnsServer(address: String)
        fun setMtu(mtu: Int)
    }

    /**
     * Everything goes into the tunnel: the IPv4 default route always, the IPv6 one only with
     * an IPv6 address. A VPN without any IPv6 address, route or resolver makes Android block
     * the family for every app, which is the policy for a node without IPv6.
     */
    fun applyTo(target: Target) {
        target.addAddress(address4, prefix4)
        target.addRoute("0.0.0.0", 0)
        target.addDnsServer(dns)
        target.setMtu(mtu)
        if (ipv6) {
            target.addAddress(address6!!, prefix6!!)
            target.addRoute("::", 0)
        }
    }

    companion object {
        /** Strict decode and validation; null for anything that is not a valid spec. */
        fun parse(raw: String): TunSpec? {
            if (raw.isEmpty() || raw.length > 1024) return null
            val spec = try {
                CoreProtocol.json.decodeFromString(serializer(), raw)
            } catch (error: SerializationException) {
                return null
            } catch (error: IllegalArgumentException) {
                return null
            }
            return spec.takeIf { it.valid() }
        }

        /** Dotted-quad IPv4 literal, four bytes; never resolved. */
        fun parseIpv4(text: String): ByteArray? {
            val parts = text.split('.')
            if (parts.size != 4) return null
            val out = ByteArray(4)
            for ((i, part) in parts.withIndex()) {
                if (part.isEmpty() || part.length > 3 || !part.all { it in '0'..'9' }) return null
                if (part.length > 1 && part[0] == '0') return null
                val value = part.toInt()
                if (value > 255) return null
                out[i] = value.toByte()
            }
            return out
        }

        /** IPv6 literal (hex groups, at most one "::", no zone or embedded IPv4); never resolved. */
        fun parseIpv6(text: String): ByteArray? {
            if (text.isEmpty() || text.length > 39 || !text.all { it == ':' || it.isDigit() || it.lowercaseChar() in 'a'..'f' }) return null
            val halves = text.split("::")
            if (halves.size > 2) return null
            fun groups(part: String): List<String>? =
                if (part.isEmpty()) emptyList() else part.split(':').takeIf { list -> list.all { it.length in 1..4 } }
            val head = groups(halves[0]) ?: return null
            val tail = if (halves.size == 2) groups(halves[1]) ?: return null else emptyList()
            val count = head.size + tail.size
            if ((halves.size == 1 && count != 8) || (halves.size == 2 && count > 7)) return null
            val all = head + List(8 - count) { "0" } + tail
            val out = ByteArray(16)
            for ((i, group) in all.withIndex()) {
                val value = group.toInt(16)
                out[2 * i] = (value shr 8).toByte()
                out[2 * i + 1] = value.toByte()
            }
            return out
        }

        private fun inSubnet(address: ByteArray, network: ByteArray, prefix: Int): Boolean {
            for (bit in 0 until prefix) {
                val mask = 0x80 shr (bit % 8)
                if ((address[bit / 8].toInt() and mask) != (network[bit / 8].toInt() and mask)) return false
            }
            return true
        }
    }

    private fun valid(): Boolean {
        val own = parseIpv4(address4) ?: return false
        val resolver = parseIpv4(dns) ?: return false
        if (prefix4 !in 8..30 || mtu !in 1280..65535) return false
        // The resolver must live inside the tunnel's own subnet, so only the stack can answer it.
        if (resolver.contentEquals(own) || !inSubnet(resolver, own, prefix4)) return false
        return if (ipv6) {
            address6 != null && prefix6 != null && prefix6 in 64..126 && parseIpv6(address6) != null
        } else {
            address6 == null && prefix6 == null
        }
    }
}
