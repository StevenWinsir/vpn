package com.asterlink.vpn.session

import com.asterlink.vpn.core.CoreSnapshot

/**
 * What the VPN's ongoing notification says (handoff_app_android.md 9.3): connected or not,
 * the line's display name and the live rates. Only metadata the user already sees in the app;
 * never an address. Equal values mean the notification need not be rebuilt.
 */
data class TunnelNotice(
    val connected: Boolean,
    val lineName: String?,
    val uploadBytesPerSecond: Long,
    val downloadBytesPerSecond: Long,
    /** Connected but the authorization gate is closed: the title says why, without rates. */
    val gate: GateState = GateState.Open,
) {
    enum class GateState { Open, Reconfirming, WaitingNetwork }

    /** "HK 01 · ↑1.20 MiB/s ↓8.40 MiB/s"; null while not connected (the title says it all). */
    val text: String?
        get() {
            if (!connected) return null
            if (gate != GateState.Open) return lineName
            val rates = "↑" + Formatting.speed(uploadBytesPerSecond) + " ↓" + Formatting.speed(downloadBytesPerSecond)
            return if (lineName.isNullOrEmpty()) rates else "$lineName · $rates"
        }

    companion object {
        val Connecting = TunnelNotice(false, null, 0, 0)

        fun from(state: SessionState): TunnelNotice {
            val snapshot = state.snapshot
            if (state.phase != AppPhase.SignedIn || snapshot.state != CoreSnapshot.State.CONNECTED) return Connecting
            val metering = snapshot.metering
            val gate = snapshot.gate
            return TunnelNotice(
                connected = true,
                lineName = snapshot.selectedNode?.name,
                uploadBytesPerSecond = if (gate == null) metering?.uploadBytesPerSecond ?: 0 else 0,
                downloadBytesPerSecond = if (gate == null) metering?.downloadBytesPerSecond ?: 0 else 0,
                gate = when {
                    gate == null -> GateState.Open
                    gate.waitingNetwork -> GateState.WaitingNetwork
                    else -> GateState.Reconfirming
                },
            )
        }
    }
}
