package com.asterlink.vpn

import android.content.Context
import android.net.ConnectivityManager
import android.os.SystemClock
import com.asterlink.core.mobile.Mobile
import com.asterlink.vpn.core.CoreBridge
import com.asterlink.vpn.core.CoreClient
import com.asterlink.vpn.core.LoginTarget
import com.asterlink.vpn.security.AppPreferences
import com.asterlink.vpn.security.FileDeviceIdentity
import com.asterlink.vpn.security.KeystoreCredentialStore
import com.asterlink.vpn.security.credentialStore
import com.asterlink.vpn.security.settingsStore
import com.asterlink.vpn.session.CoreLifecycle
import com.asterlink.vpn.session.SessionController
import com.asterlink.vpn.vpn.AndroidPlatform
import com.asterlink.vpn.vpn.AsterVpnService
import com.asterlink.vpn.vpn.CoreTunnelSession
import com.asterlink.vpn.vpn.HostNetwork
import com.asterlink.vpn.vpn.NetworkMonitor
import com.asterlink.vpn.vpn.TunnelLauncher
import com.asterlink.vpn.vpn.TunnelSession
import com.asterlink.vpn.vpn.VpnHostRegistry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.withContext

/** Hand-written constructor injection (no DI framework; the app is small). */
class AppContainer(context: Context) {
    private val app = context.applicationContext
    val connectivity: ConnectivityManager = app.getSystemService(ConnectivityManager::class.java)

    /** Work that must outlive a screen or the service object (a settling disconnect). */
    val appScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /** The network underneath the VPN; the Core is told when it changes. */
    val networkMonitor: NetworkMonitor = NetworkMonitor(connectivity) { if (core.isStarted) Mobile.networkChanged() }
    val vpnHosts: VpnHostRegistry = VpnHostRegistry()
    val core: CoreBridge = CoreBridge(AndroidPlatform(vpnHosts, networkMonitor), app.cacheDir)
    val coreClient: CoreClient = CoreClient(core)

    /** Hands the SessionController's connect to AsterVpnService and the Core's answer back. */
    val tunnelLauncher: TunnelLauncher = TunnelLauncher(
        startService = { AsterVpnService.connect(app) },
        stopService = { runCatching { AsterVpnService.disconnect(app) } },
        isRunning = { vpnHosts.current != null },
    )

    /** What AsterVpnService runs. The TUN acceptance test replaces it (debug builds only). */
    @Volatile var tunnelSession: TunnelSession = CoreTunnelSession(core::start, coreClient, tunnelLauncher)

    val preferences: AppPreferences = AppPreferences(app.settingsStore)

    /**
     * The one session for the process. Application-scoped on the main thread, so it keeps
     * watching the Core while the VPN runs with no activity; started by the first activity.
     */
    val session: SessionController = SessionController(
        client = coreClient,
        core = object : CoreLifecycle {
            override suspend fun start() = withContext(Dispatchers.IO) { core.start() }
            override suspend fun stop() = withContext(Dispatchers.IO) { core.stop() }
        },
        target = LoginTarget(apiBase = BuildConfig.API_BASE_URL, channel = BuildConfig.APP_ENV, appVersion = BuildConfig.VERSION_NAME),
        credentials = KeystoreCredentialStore(app.credentialStore),
        preferences = preferences,
        devices = FileDeviceIdentity(app.filesDir),
        tunnel = tunnelLauncher,
        scope = MainScope(),
        clock = SystemClock::elapsedRealtime,
    )

    init {
        networkMonitor.start()
    }

    fun currentNetwork(): HostNetwork = networkMonitor.current()
}
