package com.asterlink.vpn.vpn

import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import com.asterlink.vpn.core.CoreProtocol

/**
 * The physical network underneath the VPN (handoff_app_android.md 6: PhysicalDefault and the
 * network signature come from the host). This app is not excluded from its own VPN, so its
 * `activeNetwork` and default-network callback turn into the VPN once the tunnel is up; the
 * monitor instead follows the best network that has INTERNET and is NOT_VPN, which is the
 * one the VPN rides on. The Core reads the cached description through
 * Platform.networkSnapshot (once per second while connected) and is told about changes.
 */
class NetworkMonitor(
    private val connectivity: ConnectivityManager,
    private val onChanged: () -> Unit,
) {
    @Volatile private var underlying: Network? = null
    @Volatile private var cached: String? = null
    private var thread: HandlerThread? = null

    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) = update(network)
        override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) = update(network)
        override fun onLinkPropertiesChanged(network: Network, link: LinkProperties) = update(network)
        override fun onLost(network: Network) {
            if (network == underlying) {
                underlying = null
                refresh()
            }
        }
    }

    private fun update(network: Network) {
        underlying = network
        refresh()
    }

    private fun refresh() {
        val next = encode(HostNetwork.read(connectivity, underlying))
        val changed = next != cached
        cached = next
        if (changed) onChanged()
    }

    /** Idempotent. Callbacks arrive on a private thread, never the main thread. */
    @Synchronized
    fun start() {
        if (thread != null) return
        val handlerThread = HandlerThread("asterlink-network").also { it.start() }
        thread = handlerThread
        val handler = Handler(handlerThread.looper)
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
            .build()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            // Tracks the single best matching network without asking the system to keep one up.
            connectivity.registerBestMatchingNetworkCallback(request, callback, handler)
        } else {
            // Before API 31 the closest equivalent is a request; the default network already
            // satisfies it, so no extra network is brought up.
            connectivity.requestNetwork(request, callback, handler)
        }
    }

    /** The current description, read directly when no callback has arrived yet. */
    fun snapshotJson(): String = cached ?: encode(HostNetwork.read(connectivity, underlying ?: physicalActive()))

    fun current(): HostNetwork = HostNetwork.read(connectivity, underlying ?: physicalActive())

    /** Before the first callback: the active network, if it is not a VPN. */
    private fun physicalActive(): Network? {
        val network = connectivity.activeNetwork ?: return null
        val capabilities = connectivity.getNetworkCapabilities(network) ?: return null
        return network.takeIf { capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) }
    }

    private fun encode(network: HostNetwork): String = CoreProtocol.json.encodeToString(HostNetwork.serializer(), network)
}
