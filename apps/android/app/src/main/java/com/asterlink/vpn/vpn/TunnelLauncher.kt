package com.asterlink.vpn.vpn

import com.asterlink.vpn.session.LocalCodes
import com.asterlink.vpn.session.TunnelControl
import com.asterlink.vpn.session.TunnelOutcome
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.withTimeoutOrNull

/**
 * Hands one connect request from the SessionController to [AsterVpnService] and its outcome
 * back. The connect has to run inside the consented foreground service (only it can
 * establish the interface and protect sockets); the controller only waits for the Core's
 * answer, at the generation it decided on.
 */
class TunnelLauncher(
    private val startService: () -> Unit,
    private val stopService: () -> Unit,
    private val isRunning: () -> Boolean,
    private val timeoutMillis: Long = CONNECT_TIMEOUT_MILLIS,
) : TunnelControl {
    companion object {
        /** The Core's connect budget is 90 s; establish and the hand-off add a few seconds. */
        const val CONNECT_TIMEOUT_MILLIS = 120_000L
    }

    /** A request the service has taken; it reports exactly one outcome. */
    class Claim internal constructor(val generation: Long, private val result: CompletableDeferred<TunnelOutcome>) {
        fun complete(outcome: TunnelOutcome) {
            result.complete(outcome)
        }

        internal suspend fun await(): TunnelOutcome = result.await()
    }

    private val pending = AtomicReference<Claim?>(null)

    override suspend fun connect(generation: Long): TunnelOutcome {
        val claim = Claim(generation, CompletableDeferred())
        pending.getAndSet(claim)?.complete(TunnelOutcome.Refused(LocalCodes.VPN_STOPPING))
        try {
            startService()
        } catch (error: RuntimeException) {
            // IllegalStateException / ForegroundServiceStartNotAllowedException / SecurityException.
            pending.compareAndSet(claim, null)
            return TunnelOutcome.Refused(LocalCodes.VPN_START_FAILED)
        }
        return withTimeoutOrNull(timeoutMillis) { claim.await() } ?: run {
            pending.compareAndSet(claim, null)
            TunnelOutcome.Refused(LocalCodes.CONNECT_TIMEOUT)
        }
    }

    /** The service's side: the waiting request, or null when the controller did not ask. */
    fun claim(): Claim? = pending.getAndSet(null)

    override val running: Boolean get() = isRunning()

    override fun stop() = stopService()
}
