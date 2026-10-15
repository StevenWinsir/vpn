package com.asterlink.vpn

import android.content.Context
import android.net.ConnectivityManager
import com.asterlink.core.mobile.Mobile
import com.asterlink.vpn.core.CoreBridge
import com.asterlink.vpn.core.CoreClient
import com.asterlink.vpn.vpn.AndroidPlatform
import com.asterlink.vpn.vpn.CoreTunnelSession
import com.asterlink.vpn.vpn.HostNetwork
import com.asterlink.vpn.vpn.NetworkMonitor
import com.asterlink.vpn.vpn.TunnelSession
import com.asterlink.vpn.vpn.VpnHostRegistry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

/** Hand-written constructor injection (no DI framework; the app is small). */
class AppContainer(context: Context) {
    private val app = context.applicationContext
    val connectivity: ConnectivityManager = app.getSystemService(ConnectivityManager::class.java)

    /** Work that must outlive a screen or the service object (a settling disconnect). */
    val appScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /** The network underneath the VPN; the Core is told when it changes. */
    val networkMonitor: NetworkMonitor = NetworkMonitor(connectivity) { if (core.isStarted) Mobile.networkChanged() }
    val vpnHosts: VpnHostRegistry = VpnHostRegistry()
    val core: CoreBridge = CoreBridge(AndroidPlatform(vpnHosts, networkMonitor), app.cacheDir)
    val coreClient: CoreClient = CoreClient(core)

    /** What AsterVpnService runs. The TUN acceptance test replaces it (debug builds only). */
    @Volatile var tunnelSession: TunnelSession = CoreTunnelSession(core::start, coreClient)

    init {
        networkMonitor.start()
    }

    fun currentNetwork(): HostNetwork = networkMonitor.current()
}
