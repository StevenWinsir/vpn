package com.asterlink.vpn.vpn

import android.net.ConnectivityManager
import com.asterlink.core.mobile.Platform
import com.asterlink.vpn.core.CoreProtocol

/**
 * The callbacks the Go Core makes into the app. Round 1 has no VpnService, so there is no
 * VPN to exclude a socket from and no TUN to open: protect refuses (the Core then fails the
 * dial instead of sending it anywhere) and openTun returns -1. The Core never calls either
 * in this build, because its Android engine reports tun_unavailable before any tunnel step.
 * Round 2 moves both onto AsterVpnService.
 */
class AndroidPlatform(private val connectivity: ConnectivityManager) : Platform {
    override fun protect(fd: Int): Boolean = false

    override fun openTun(specJSON: String): Int = -1

    override fun networkSnapshot(): String =
        CoreProtocol.json.encodeToString(HostNetwork.serializer(), HostNetwork.read(connectivity))
}
