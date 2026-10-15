package com.asterlink.vpn

import android.content.Context
import android.net.ConnectivityManager
import com.asterlink.vpn.core.CoreBridge
import com.asterlink.vpn.core.CoreClient
import com.asterlink.vpn.vpn.AndroidPlatform
import com.asterlink.vpn.vpn.HostNetwork

/** Hand-written constructor injection (no DI framework; the app is small). */
class AppContainer(context: Context) {
    private val app = context.applicationContext
    val connectivity: ConnectivityManager = app.getSystemService(ConnectivityManager::class.java)
    val core = CoreBridge(AndroidPlatform(connectivity), app.cacheDir)
    val coreClient = CoreClient(core)

    fun currentNetwork(): HostNetwork = HostNetwork.read(connectivity)
}
