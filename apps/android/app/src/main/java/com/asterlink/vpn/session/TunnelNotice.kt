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
) {
    /** "HK 01 · ↑1.20 MiB/s ↓8.40 MiB/s"; null while not connected (the title says it all). */
    val text: String?
        get() {
            if (!connected) return null
            val rates = "↑" + Formatting.speed(uploadBytesPerSecond) + " ↓" + Formatting.speed(downloadBytesPerSecond)
            return if (lineName.isNullOrEmpty()) rates else "$lineName · $rates"
        }

    companion object {
        val Connecting = TunnelNotice(false, null, 0, 0)

        fun from(state: SessionState): TunnelNotice {
            val snapshot = state.snapshot
            if (state.phase != AppPhase.SignedIn || snapshot.state != CoreSnapshot.State.CONNECTED) return Connecting
            val metering = snapshot.metering
            return TunnelNotice(
                connected = true,
                lineName = snapshot.selectedNode?.name,
                uploadBytesPerSecond = metering?.uploadBytesPerSecond ?: 0,
                downloadBytesPerSecond = metering?.downloadBytesPerSecond ?: 0,
            )
        }
    }
}
