package com.asterlink.vpn.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.asterlink.vpn.security.ThemeMode

/**
 * Design tokens (handoff_app_android.md 9.2), the same brand as the Windows client. Dynamic
 * (Material You) color is deliberately off so the brand looks the same on every device.
 */
@Immutable
data class StatusColors(
    val connected: Color,
    val pending: Color,
    val error: Color,
    val outline: Color,
    val secondaryText: Color,
    val connectedContainer: Color,
    val pendingContainer: Color,
    val errorContainer: Color,
    val brandContainer: Color,
)

private val LightStatus = StatusColors(
    connected = Color(0xFF16A34A),
    pending = Color(0xFFD97706),
    error = Color(0xFFDC2626),
    outline = Color(0xFFE3E8EF),
    secondaryText = Color(0xFF5B6475),
    connectedContainer = Color(0xFFE8F7EE),
    pendingContainer = Color(0xFFFEF3E2),
    errorContainer = Color(0xFFFDECEC),
    brandContainer = Color(0xFFE8F0FE),
)

private val DarkStatus = StatusColors(
    connected = Color(0xFF22C55E),
    pending = Color(0xFFF59E0B),
    error = Color(0xFFF87171),
    outline = Color(0xFF243049),
    secondaryText = Color(0xFF98A2B3),
    connectedContainer = Color(0xFF0F2A1C),
    pendingContainer = Color(0xFF2E2210),
    errorContainer = Color(0xFF341A1E),
    brandContainer = Color(0xFF16254A),
)

private val LightScheme = lightColorScheme(
    primary = Color(0xFF2F6FEB),
    onPrimary = Color.White,
    primaryContainer = Color(0xFFE8F0FE),
    onPrimaryContainer = Color(0xFF0B3B99),
    secondaryContainer = Color(0xFFE8F0FE),
    onSecondaryContainer = Color(0xFF0B3B99),
    background = Color(0xFFF6F8FB),
    onBackground = Color(0xFF0F172A),
    surface = Color(0xFFFFFFFF),
    onSurface = Color(0xFF0F172A),
    surfaceVariant = Color(0xFFF1F4F9),
    onSurfaceVariant = Color(0xFF5B6475),
    surfaceContainer = Color(0xFFFFFFFF),
    surfaceContainerLow = Color(0xFFFFFFFF),
    surfaceContainerHigh = Color(0xFFF1F4F9),
    surfaceContainerHighest = Color(0xFFE9EEF5),
    outline = Color(0xFFE3E8EF),
    outlineVariant = Color(0xFFE3E8EF),
    error = Color(0xFFDC2626),
    onError = Color.White,
)

private val DarkScheme = darkColorScheme(
    primary = Color(0xFF5B8DEF),
    onPrimary = Color(0xFF0B1220),
    primaryContainer = Color(0xFF16254A),
    onPrimaryContainer = Color(0xFFD6E2FB),
    secondaryContainer = Color(0xFF16254A),
    onSecondaryContainer = Color(0xFFD6E2FB),
    background = Color(0xFF0B1220),
    onBackground = Color(0xFFE6EAF2),
    surface = Color(0xFF121A2B),
    onSurface = Color(0xFFE6EAF2),
    surfaceVariant = Color(0xFF1A2438),
    onSurfaceVariant = Color(0xFF98A2B3),
    surfaceContainer = Color(0xFF121A2B),
    surfaceContainerLow = Color(0xFF121A2B),
    surfaceContainerHigh = Color(0xFF1A2438),
    surfaceContainerHighest = Color(0xFF222D44),
    outline = Color(0xFF243049),
    outlineVariant = Color(0xFF243049),
    error = Color(0xFFF87171),
    onError = Color(0xFF0B1220),
)

/** Type scale: status 32/40, title 20/28, body 15/22, caption 13/18; system font. */
private val AsterTypography = Typography(
    displaySmall = TextStyle(fontSize = 32.sp, lineHeight = 40.sp, fontWeight = FontWeight.SemiBold),
    headlineSmall = TextStyle(fontSize = 24.sp, lineHeight = 32.sp, fontWeight = FontWeight.SemiBold),
    titleLarge = TextStyle(fontSize = 20.sp, lineHeight = 28.sp, fontWeight = FontWeight.SemiBold),
    titleMedium = TextStyle(fontSize = 16.sp, lineHeight = 24.sp, fontWeight = FontWeight.SemiBold),
    titleSmall = TextStyle(fontSize = 15.sp, lineHeight = 22.sp, fontWeight = FontWeight.Medium),
    bodyLarge = TextStyle(fontSize = 15.sp, lineHeight = 22.sp),
    bodyMedium = TextStyle(fontSize = 15.sp, lineHeight = 22.sp),
    bodySmall = TextStyle(fontSize = 13.sp, lineHeight = 18.sp),
    labelLarge = TextStyle(fontSize = 15.sp, lineHeight = 22.sp, fontWeight = FontWeight.SemiBold),
    labelMedium = TextStyle(fontSize = 13.sp, lineHeight = 18.sp, fontWeight = FontWeight.Medium),
    labelSmall = TextStyle(fontSize = 11.sp, lineHeight = 16.sp, fontWeight = FontWeight.Medium),
)

/** Numbers that change in place (rates, counters, versions) use tabular figures. */
val TabularNumbers = TextStyle(fontFeatureSettings = "tnum")

/** 4/8 dp grid, 20 dp page margins, 16 dp card corners, 56 dp pill buttons, 168 dp connect button. */
object Dimens {
    val PageMargin = 20.dp
    val CardRadius = 16.dp
    val ButtonHeight = 56.dp
    val ConnectButton = 168.dp
    val MaxContentWidth = 560.dp
}

val LocalStatusColors = staticCompositionLocalOf { LightStatus }

@Composable
fun ThemeMode.isDark(): Boolean = when (this) {
    ThemeMode.System -> isSystemInDarkTheme()
    ThemeMode.Light -> false
    ThemeMode.Dark -> true
}

@Composable
fun AsterTheme(darkTheme: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
    val status = if (darkTheme) DarkStatus else LightStatus
    CompositionLocalProvider(LocalStatusColors provides status) {
        MaterialTheme(
            colorScheme = if (darkTheme) DarkScheme else LightScheme,
            typography = AsterTypography,
            content = content,
        )
    }
}
