package com.asterlink.vpn

import android.app.Activity
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.os.ParcelFileDescriptor

/**
 * "Another VPN app" for the lifecycle suite's L04 (test APK only). The shell starts the
 * activity with `am start -n com.asterlink.vpn.test/com.asterlink.vpn.CompetitorActivity`
 * (`--ez stop true` to end it); its consent is pre-granted with appops, like the app's own.
 * Preparing and establishing a VPN here makes the system revoke AsterLink's VPN, which is
 * exactly what installing and starting a second VPN app does on a phone.
 */
class CompetitorActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val service = Intent(this, CompetitorVpnService::class.java)
        if (intent.getBooleanExtra("stop", false)) {
            stopService(service)
        } else if (VpnService.prepare(this) == null) {
            startService(service)
        }
        finish()
    }
}

/** Holds one minimal VPN interface (a private /32, no default route) until stopped. */
class CompetitorVpnService : VpnService() {
    @Volatile private var tun: ParcelFileDescriptor? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (tun == null) {
            tun = runCatching {
                Builder().setSession("lifecycle-competitor").addAddress("10.234.0.1", 32).addRoute("10.234.0.0", 16).establish()
            }.getOrNull()
        }
        return START_NOT_STICKY
    }

    override fun onRevoke() = stopSelf()

    override fun onDestroy() {
        runCatching { tun?.close() }
        tun = null
        super.onDestroy()
    }
}
