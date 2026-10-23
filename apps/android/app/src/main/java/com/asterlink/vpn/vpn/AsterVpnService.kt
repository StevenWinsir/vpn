package com.asterlink.vpn.vpn

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.VpnService
import android.os.Build
import android.util.Log
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.asterlink.vpn.AppContainer
import com.asterlink.vpn.AsterApp
import com.asterlink.vpn.MainActivity
import com.asterlink.vpn.session.LocalCodes
import com.asterlink.vpn.session.TunnelNotice
import com.asterlink.vpn.session.TunnelOutcome
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * The system VPN (handoff_app_android.md 7.2, 7.4). It owns nothing but the two abilities only
 * a consented VpnService has: protecting the Core's sockets and establishing the interface
 * from the Core's [TunSpec]. The descriptor goes to the Core at once (detachFd) and the Core
 * closes it, which is what removes the VPN; this class never keeps one.
 *
 * Lifecycle: ACTION_CONNECT puts the service in the foreground, registers it as the Core's
 * VPN host and runs the [TunnelSession]; a failed connect, ACTION_DISCONNECT (the app or the
 * notification), onRevoke (another VPN took over, or the user turned it off in Settings) and
 * onDestroy all end in the same bounded disconnect. Always-on VPN is not supported in this
 * release (manifest SUPPORTS_ALWAYS_ON=false): a start without our action is refused, so the
 * system cannot bring up a tunnel without a signed-in user asking for it.
 *
 * The ongoing notification is this service's alone: once connected it follows the session
 * state (line name, live rates), and that follower is stopped before the notification is
 * removed, so no late update can bring it back after the VPN is gone.
 */
class AsterVpnService : VpnService(), VpnHost {
    companion object {
        const val ACTION_CONNECT = "com.asterlink.vpn.action.CONNECT"
        const val ACTION_DISCONNECT = "com.asterlink.vpn.action.DISCONNECT"

        /** Upper bound for the system to make the established VPN this app's network. */
        private const val ESTABLISH_ACTIVE_MILLIS = 5_000L

        /** The caller has checked VpnService.prepare(context) == null (consent given). */
        fun connect(context: Context) {
            ContextCompat.startForegroundService(context, Intent(context, AsterVpnService::class.java).setAction(ACTION_CONNECT))
        }

        fun disconnect(context: Context) {
            context.startService(Intent(context, AsterVpnService::class.java).setAction(ACTION_DISCONNECT))
        }
    }

    private lateinit var container: AppContainer
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var session: Job? = null
    private var notifier: Job? = null
    @Volatile private var ending = false
    // Main thread only: a connect that arrived while a disconnect was settling, and whether
    // the system has already destroyed this instance.
    private var connectAfterEnd = false
    private var destroyed = false
    // Main thread only: this instance's teardown is complete, and the newest start it was given.
    private var finished = false
    private var lastStartId = 0

    override fun onCreate() {
        super.onCreate()
        container = (application as AsterApp).container
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        lastStartId = startId
        when (intent?.action) {
            ACTION_CONNECT -> begin()
            ACTION_DISCONNECT -> end()
            else -> {
                // Started by the system (always-on) or restarted without our intent: no
                // user asked for a tunnel, so none is made.
                stopSelf(startId)
            }
        }
        return START_NOT_STICKY
    }

    private fun trace(text: String) {
        Log.i("AsterTrace", "service: $text (ending=$ending finished=$finished connectAfterEnd=$connectAfterEnd session=${session?.isActive})")
    }

    private fun begin() {
        trace("begin")
        // startForegroundService obliges startForeground even when the request is dropped.
        goForeground(TunnelNotice.Connecting)
        // A start that reached this instance after its teardown finished (it was stopping
        // itself): it serves the new connect instead of being destroyed.
        if (finished) {
            finished = false
            ending = false
        }
        // A disconnect of this instance is still settling (a few seconds on a slow network):
        // the connect waits for it in this same foreground service and then runs, instead of
        // being refused or racing the teardown (measured: Disconnect, then Connect at once).
        if (ending) {
            connectAfterEnd = true
            return
        }
        container.vpnHosts.attach(this)
        if (session?.isActive == true) return
        val tunnel = container.tunnelSession
        session = scope.launch {
            val failure = tunnel.connect()
            if (failure == null) {
                goForeground(TunnelNotice(connected = true, lineName = null, uploadBytesPerSecond = 0, downloadBytesPerSecond = 0))
                followSession()
            } else {
                withContext(Dispatchers.Main) { if (!ending) end() }
            }
        }
    }

    /** The one way out: close the data path (bounded), then leave the foreground and stop. Main thread. */
    private fun end() {
        trace("end")
        if (ending) {
            // A second disconnect (the notification's) also cancels a connect queued behind the first.
            if (connectAfterEnd) {
                connectAfterEnd = false
                container.tunnelLauncher.claim()?.complete(TunnelOutcome.Refused(LocalCodes.VPN_STOPPING))
            }
            return
        }
        ending = true
        val tunnel = container.tunnelSession
        val hosts = container.vpnHosts
        // Runs in the application scope, so a disconnect that is still settling finishes
        // after this service object is destroyed. The host stays registered until then:
        // settling needs protected sockets while the interface is going away.
        container.appScope.launch {
            try {
                session?.cancel()
                notifier?.cancelAndJoin()
                tunnel.disconnect()
            } finally {
                hosts.detach(this@AsterVpnService)
                withContext(Dispatchers.Main + NonCancellable) { ended() }
            }
        }
    }

    /** The teardown is complete: run the connect that waited for it, or leave. Main thread. */
    private fun ended() {
        trace("ended")
        if (connectAfterEnd && !destroyed) {
            connectAfterEnd = false
            ending = false
            begin()
            return
        }
        // Stays "ending" for good: this instance is finished, and its onDestroy (or a late
        // disconnect) must not run a second teardown, whose Core disconnect would end a
        // tunnel a new instance of the service is already bringing up.
        if (connectAfterEnd) {
            connectAfterEnd = false
            container.tunnelLauncher.claim()?.complete(TunnelOutcome.Refused(LocalCodes.VPN_STOPPING))
        }
        finished = true
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        // Only if no newer start arrived meanwhile; otherwise that start (a connect) is delivered here.
        stopSelfResult(lastStartId)
    }

    override fun onRevoke() {
        trace("onRevoke")
        // The system has already deactivated the interface (another VPN app took over, or the
        // user turned it off in Settings); the Core must stop forwarding and settle what was
        // carried, and the user is told why (L04).
        if (!ending) container.onVpnRevoked()
        end()
    }

    override fun onDestroy() {
        destroyed = true
        end()
        scope.cancel()
        super.onDestroy()
    }

    /** Line name and live rates while the session says connected (about once a second). */
    private fun followSession() {
        if (notifier?.isActive == true) return
        notifier = scope.launch {
            container.sessionState
                .map(TunnelNotice::from)
                .distinctUntilChanged()
                .collect { notice -> if (notice.connected && !ending) VpnNotifications.update(this@AsterVpnService, notice) }
        }
    }

    private fun goForeground(notice: TunnelNotice) {
        val notification = VpnNotifications.build(this, notice)
        val type = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0
        ServiceCompat.startForeground(this, VpnNotifications.ID, notification, type)
    }

    override val isEnding: Boolean get() = ending

    override fun protectSocket(fd: Int): Boolean = protect(fd)

    override fun establish(spec: TunSpec): Int {
        trace("establish")
        val builder = Builder()
            .setSession("AsterLink")
            .setConfigureIntent(
                PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE),
            )
        spec.applyTo(object : TunSpec.Target {
            override fun addAddress(address: String, prefix: Int) { builder.addAddress(address, prefix) }
            override fun addRoute(address: String, prefix: Int) { builder.addRoute(address, prefix) }
            override fun addDnsServer(address: String) { builder.addDnsServer(address) }
            override fun setMtu(mtu: Int) { builder.setMtu(mtu) }
        })
        // Follow the system default network underneath (null), and let the VPN inherit its
        // meteredness instead of being metered by default (API 29+).
        builder.setUnderlyingNetworks(null)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) builder.setMetered(false)
        // Deliberately absent: allowBypass, setHttpProxy and addDisallowedApplication(self).
        // The Core's own unprotected traffic (Verify, the system resolver) must take the
        // tunnel like any app; its control plane and the node connection are protected.
        // establish() answers null unless this app is the system's current VPN owner, which
        // prepare() makes it, when consent was given. The activity calls prepare() before a
        // connect, but nothing else does (the Quick Settings tile, a connect after the process
        // was restarted); measured on API 36: without this the first establish of a process
        // returned null. Consent withdrawn in Settings: prepare() returns an intent; refuse.
        val consented = try {
            prepare(this) == null
        } catch (error: RuntimeException) {
            false
        }
        if (!consented) return -1
        val descriptor = try {
            builder.establish()
        } catch (error: IllegalArgumentException) {
            null
        } catch (error: IllegalStateException) {
            null
        } catch (error: SecurityException) {
            null
        } ?: return -1
        // establish() returns before the system has made the VPN this app's default network.
        // In that window this process's lookups and sockets still use the physical network
        // (measured on API 36: the first lookup answered with real addresses, not fake-ip),
        // so Verify could pass without crossing the tunnel. The descriptor goes to the Core
        // only once the VPN carries this app; otherwise the VPN is closed again.
        if (!awaitVpnActive(ESTABLISH_ACTIVE_MILLIS)) {
            descriptor.close()
            return -1
        }
        return descriptor.detachFd()
    }

    private fun awaitVpnActive(millis: Long): Boolean {
        val connectivity = getSystemService(android.net.ConnectivityManager::class.java)
        val deadline = android.os.SystemClock.elapsedRealtime() + millis
        while (true) {
            val active = connectivity.activeNetwork?.let(connectivity::getNetworkCapabilities)
            if (active?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN) == true) return true
            if (android.os.SystemClock.elapsedRealtime() >= deadline) return false
            Thread.sleep(25)
        }
    }
}
