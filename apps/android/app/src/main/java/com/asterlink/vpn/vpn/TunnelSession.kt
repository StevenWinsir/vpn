package com.asterlink.vpn.vpn

import com.asterlink.vpn.core.CoreClient
import com.asterlink.vpn.core.CoreException
import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.session.LocalCodes
import com.asterlink.vpn.session.TunnelOutcome
import kotlin.coroutines.cancellation.CancellationException

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
    private val launcher: TunnelLauncher? = null,
) : TunnelSession {
    override suspend fun connect(): String? {
        // The SessionController's request, at the generation it decided on; without one (a
        // start nobody in the app asked for) the Core's current generation is used.
        val claim = launcher?.claim()
        val outcome: TunnelOutcome = try {
            startCore()
            TunnelOutcome.Replied(if (claim != null) client.connect(claim.generation) else client.connectCurrent())
        } catch (error: CoreException) {
            TunnelOutcome.Refused(error.code, error.message.orEmpty())
        } catch (error: CancellationException) {
            claim?.complete(TunnelOutcome.Refused(LocalCodes.VPN_STOPPING))
            throw error
        } catch (error: Exception) {
            TunnelOutcome.Refused(LocalCodes.VPN_START_FAILED)
        }
        claim?.complete(outcome)
        return when (outcome) {
            is TunnelOutcome.Replied -> {
                val reply = outcome.reply
                if (reply.snapshot.state == CoreSnapshot.State.CONNECTED && reply.actionError == null) null
                else reply.actionError?.code ?: reply.snapshot.lastError?.code ?: "tunnel_failed"
            }
            is TunnelOutcome.Refused -> outcome.code
        }
    }

    override suspend fun disconnect() {
        try {
            client.disconnect()
        } catch (error: CoreException) {
            // core_not_started: nothing to close.
        }
    }
}
