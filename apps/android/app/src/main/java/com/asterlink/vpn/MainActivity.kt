package com.asterlink.vpn

import android.Manifest
import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.os.PowerManager
import android.os.SystemClock
import android.provider.Settings
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.browser.customtabs.CustomTabsIntent
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.core.net.toUri
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.asterlink.vpn.security.ThemeMode
import com.asterlink.vpn.session.AppPhase
import com.asterlink.vpn.session.Diagnostics
import com.asterlink.vpn.ui.AppActions
import com.asterlink.vpn.ui.BuildFacts
import com.asterlink.vpn.ui.DeviceStatus
import com.asterlink.vpn.ui.WebPage
import com.asterlink.vpn.ui.navigation.AsterRoot
import com.asterlink.vpn.ui.navigation.Clock
import com.asterlink.vpn.ui.theme.AsterTheme
import com.asterlink.vpn.ui.theme.isDark
import java.time.Instant
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * The single activity. It renders the SessionController's state and binds the few things only
 * an activity can do: the system VPN consent dialog, the notification permission (Android 13+),
 * Custom Tabs for the website, system settings pages and the clipboard.
 */
class MainActivity : ComponentActivity() {
    private val container: AppContainer get() = (application as AsterApp).container

    /** The system's "allow VPN connection" dialog; denied = stay ready and say why. */
    private val vpnConsent = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == RESULT_OK) container.session.connect() else container.session.vpnConsentDenied()
    }

    /** Asked once before the first connect; a refusal does not stop the VPN (the key icon still shows). */
    private val notificationPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) { _ ->
        connectWithConsent()
    }
    private var askedNotifications = false

    /** Re-read on every resume: the user may have just changed either in system settings. */
    private var device by mutableStateOf(DeviceStatus())

    override fun onCreate(savedInstanceState: Bundle?) {
        val splash = installSplashScreen()
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val session = container.session
        splash.setKeepOnScreenCondition { session.state.value.phase == AppPhase.Starting }
        session.launch()

        val facts = BuildFacts(
            appVersion = BuildConfig.VERSION_NAME,
            channel = BuildConfig.APP_ENV,
            apiHost = BuildConfig.API_BASE_URL.toUri().host.orEmpty(),
            envFromExample = BuildConfig.ENV_FROM_EXAMPLE,
        )
        setContent {
            val state by session.state.collectAsStateWithLifecycle()
            val themeMode by container.preferences.themeMode.collectAsStateWithLifecycle(ThemeMode.System)
            val scope = rememberCoroutineScope()
            var clock by remember { mutableStateOf(Clock(SystemClock.elapsedRealtime(), Instant.now())) }
            LaunchedEffect(Unit) {
                while (true) {
                    clock = Clock(SystemClock.elapsedRealtime(), Instant.now())
                    delay(1_000)
                }
            }
            val actions = remember {
                AppActions(
                    signIn = session::signIn,
                    connect = ::requestConnect,
                    disconnect = { session.disconnect() },
                    cancelConnect = { session.cancelConnect() },
                    select = { session.select(it) },
                    switchTo = { session.switchTo(it) },
                    refresh = { session.refresh() },
                    signOut = { session.signOut() },
                    openWebsite = ::openWebsite,
                    openVpnSettings = { openSettings(Settings.ACTION_VPN_SETTINGS) },
                    openBatterySettings = { openSettings(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS) },
                    openNotificationSettings = ::openNotificationSettings,
                    copyDiagnostics = { copyDiagnostics(facts) },
                    setTheme = { mode -> scope.launch { container.preferences.setThemeMode(mode) } },
                    dismissMessage = session::dismissMessage,
                )
            }
            AsterTheme(darkTheme = themeMode.isDark()) {
                AsterRoot(state, facts, themeMode, actions, clock, device = device)
            }
        }
    }

    override fun onResume() {
        super.onResume()
        device = readDeviceStatus()
    }

    private fun readDeviceStatus(): DeviceStatus {
        val permitted = Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
            ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
        val unrestricted = runCatching { getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(packageName) }.getOrNull()
        return DeviceStatus(
            notificationsAllowed = permitted && NotificationManagerCompat.from(this).areNotificationsEnabled(),
            batteryUnrestricted = unrestricted,
        )
    }

    /**
     * Denied notifications: the VPN works regardless (the system key icon still shows), but
     * the status and the reason a connection ended only reach the user through them, so the
     * account page offers this way back. Android asks only once in-app; afterwards the
     * permission lives in the app's notification settings.
     */
    private fun openNotificationSettings() {
        try {
            startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, packageName))
        } catch (error: ActivityNotFoundException) {
            openSettings(Settings.ACTION_SETTINGS)
        }
    }

    private fun requestConnect() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !askedNotifications &&
            ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            askedNotifications = true
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
            return
        }
        connectWithConsent()
    }

    private fun connectWithConsent() {
        val consent = try {
            VpnService.prepare(this)
        } catch (error: RuntimeException) {
            container.session.vpnConsentDenied()
            return
        }
        if (consent == null) container.session.connect() else vpnConsent.launch(consent)
    }

    private fun openWebsite(page: WebPage) {
        val uri = (BuildConfig.WEBSITE_BASE_URL.trimEnd('/') + page.path).toUri()
        try {
            CustomTabsIntent.Builder().setShowTitle(true).build().launchUrl(this, uri)
        } catch (error: ActivityNotFoundException) {
            try {
                startActivity(Intent(Intent.ACTION_VIEW, uri))
            } catch (again: ActivityNotFoundException) {
                Toast.makeText(this, R.string.notice_browser_missing, Toast.LENGTH_SHORT).show()
            }
        }
    }

    private fun openSettings(action: String) {
        try {
            startActivity(Intent(action))
        } catch (error: ActivityNotFoundException) {
            startActivity(Intent(Settings.ACTION_SETTINGS))
        }
    }

    private fun copyDiagnostics(facts: BuildFacts) {
        val state = container.session.state.value
        val text = Diagnostics.build(
            appVersion = facts.appVersion,
            channel = facts.channel,
            apiHost = facts.apiHost,
            device = "Android ${Build.VERSION.RELEASE} (API ${Build.VERSION.SDK_INT}) ${Build.SUPPORTED_ABIS.firstOrNull().orEmpty()}",
            coreVersion = state.identity?.coreVersion.orEmpty(),
            mihomoRevision = state.identity?.mihomoRevision.orEmpty(),
            snapshot = state.snapshot,
        )
        getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("AsterLink diagnostics", text))
        // Android 13+ shows its own clipboard confirmation.
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) Toast.makeText(this, R.string.diagnostics_copied, Toast.LENGTH_SHORT).show()
    }
}
