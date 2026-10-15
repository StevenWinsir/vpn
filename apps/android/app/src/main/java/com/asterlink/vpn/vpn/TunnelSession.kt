package com.asterlink.vpn.vpn

import com.asterlink.vpn.core.CoreClient
import com.asterlink.vpn.core.CoreException

/**
 * What [AsterVpnService] runs once it is in the foreground and registered as the VPN host.
 * Production connects the Core's managed session (every gate, the first metering ACK, the
 * TUN, then Verify); the TUN acceptance test substitutes a session that leaves the host idle
 * and drives the engine itself.
 */
interface TunnelSession {
    /** Null when connected; otherwise the stable Core code why not. */
    suspend fun connect(): String?

    /** Bounded: closes the data path and settles the tail. Safe when nothing runs. */
    suspend fun disconnect()
}

class CoreTunnelSession(
    private val startCore: () -> Unit,
    private val client: CoreClient,
) : TunnelSession {
    override suspend fun connect(): String? = try {
        startCore()
        client.connect()
    } catch (error: CoreException) {
        error.code
    }

    override suspend fun disconnect() {
        try {
            client.disconnect()
        } catch (error: CoreException) {
            // core_not_started: nothing to close.
        }
    }
}
