package com.asterlink.vpn.ui

import com.asterlink.vpn.security.ThemeMode

/** Public build facts shown in the footer and the about section (never secrets). */
data class BuildFacts(
    val appVersion: String,
    val channel: String,
    val apiHost: String,
    val envFromExample: Boolean,
)

/**
 * System settings that change how the VPN behaves in daily use (handoff_app_android.md 7.2,
 * 7.6), read by the activity whenever it resumes. Both are the user's decision: the app
 * explains and links to the setting, it never asks for an exemption itself.
 */
data class DeviceStatus(
    /** Android 13+ notification permission and the app's notification switch. */
    val notificationsAllowed: Boolean = true,
    /** Exempt from battery optimisation ("unrestricted"); null when it cannot be read. */
    val batteryUnrestricted: Boolean? = null,
)

/** Website pages opened in a Custom Tab (registration, purchase and renewal live on the website). */
enum class WebPage(val path: String) {
    Home("/"),
    Register("/register"),

    /** Password recovery: the website mails a one-time reset link; the app never sees the new password. */
    ForgotPassword("/forgot-password"),
    Plans("/plans"),
    Orders("/orders"),
    Dashboard("/dashboard"),

    /** The website's download page: the current APK, its SHA-256 and the source/licence notices. */
    Download("/download"),
}

/**
 * Everything the screens can ask for. The activity binds these to the SessionController and
 * the system (VPN consent, Custom Tabs, settings, clipboard); previews and screenshot tests
 * use the no-op defaults.
 */
data class AppActions(
    val signIn: (email: String, password: String, remember: Boolean) -> Unit = { _, _, _ -> },
    val connect: () -> Unit = {},
    val disconnect: () -> Unit = {},
    val cancelConnect: () -> Unit = {},
    val select: (nodeId: String) -> Unit = {},
    val switchTo: (nodeId: String) -> Unit = {},
    val refresh: () -> Unit = {},
    val signOut: () -> Unit = {},
    val openWebsite: (WebPage) -> Unit = {},
    val openVpnSettings: () -> Unit = {},
    val openBatterySettings: () -> Unit = {},
    val openNotificationSettings: () -> Unit = {},
    val copyDiagnostics: () -> Unit = {},
    val setTheme: (ThemeMode) -> Unit = {},
    val dismissMessage: () -> Unit = {},
)
