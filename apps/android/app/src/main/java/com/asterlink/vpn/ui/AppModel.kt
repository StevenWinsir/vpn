package com.asterlink.vpn.ui

import com.asterlink.vpn.security.ThemeMode

/** Public build facts shown in the footer and the about section (never secrets). */
data class BuildFacts(
    val appVersion: String,
    val channel: String,
    val apiHost: String,
    val envFromExample: Boolean,
)

/** Website pages opened in a Custom Tab (registration, purchase and renewal live on the website). */
enum class WebPage(val path: String) {
    Home("/"),
    Register("/register"),
    Plans("/plans"),
    Orders("/orders"),
    Dashboard("/dashboard"),
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
    val copyDiagnostics: () -> Unit = {},
    val setTheme: (ThemeMode) -> Unit = {},
    val dismissMessage: () -> Unit = {},
)
