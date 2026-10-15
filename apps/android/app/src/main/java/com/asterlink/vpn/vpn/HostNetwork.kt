package com.asterlink.vpn.vpn

import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import java.net.InetAddress
import java.net.NetworkInterface
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * The default network as the Core needs it (network.HostSnapshot in native/core): an app
 * cannot read netlink routes on Android 11+, so ConnectivityManager is the source. It
 * carries no account or node data. The Go side decodes it strictly; the field set and the
 * bounds here must stay in step with native/core/internal/network/host.go.
 */
@Serializable
data class HostNetwork(
    val available: Boolean,
    @SerialName("interface") val interfaceName: String? = null,
    val index: Int? = null,
    val addresses: List<String>? = null,
    val dns: List<String>? = null,
    val metered: Boolean? = null,
) {
    companion object {
        val NONE = HostNetwork(available = false)

        fun read(connectivity: ConnectivityManager): HostNetwork {
            val network = connectivity.activeNetwork ?: return NONE
            val link = connectivity.getLinkProperties(network) ?: return NONE
            val capabilities = connectivity.getNetworkCapabilities(network) ?: return NONE
            val name = link.interfaceName ?: return NONE
            val index = runCatching { NetworkInterface.getByName(name)?.index }.getOrNull() ?: return NONE
            return HostNetwork(
                available = true,
                interfaceName = name,
                index = index,
                addresses = link.linkAddresses.map { literal(it.address) }.take(32),
                dns = link.dnsServers.map(::literal).take(16),
                metered = !capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED),
            )
        }

        /** The numeric address without an IPv6 scope suffix. */
        private fun literal(address: InetAddress): String = address.hostAddress.orEmpty().substringBefore('%')
    }
}
