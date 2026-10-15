package com.asterlink.vpn.vpn

import com.asterlink.core.mobile.Platform

/**
 * Where the Core's platform calls land: the running [AsterVpnService] while there is one.
 * Only a VpnService the user consented to can protect sockets or establish an interface,
 * so without it protect refuses (the Core then fails the dial instead of sending it into the
 * VPN) and openTun returns -1 (the Core reports tun_unavailable).
 */
class VpnHostRegistry {
    @Volatile var current: VpnHost? = null
        private set

    fun attach(host: VpnHost) {
        current = host
    }

    /** Only the host that is registered can remove itself. */
    fun detach(host: VpnHost) {
        if (current === host) current = null
    }
}

/** The two VpnService abilities the Core needs (implemented by [AsterVpnService]). */
interface VpnHost {
    fun protectSocket(fd: Int): Boolean

    /** Establishes the interface for [spec]; the detached descriptor, or -1. */
    fun establish(spec: TunSpec): Int
}

class AndroidPlatform(
    private val hosts: VpnHostRegistry,
    private val network: NetworkMonitor,
) : Platform {
    override fun protect(fd: Int): Boolean = hosts.current?.protectSocket(fd) ?: false

    override fun openTun(specJSON: String): Int {
        val host = hosts.current ?: return -1
        val spec = TunSpec.parse(specJSON) ?: return -1
        return host.establish(spec)
    }

    override fun networkSnapshot(): String = network.snapshotJson()
}
