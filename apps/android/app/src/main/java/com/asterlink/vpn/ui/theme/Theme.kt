package com.asterlink.vpn.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

/**
 * Design tokens (handoff_app_android.md 9.2), the same brand as the Windows client. Dynamic
 * (Material You) color is deliberately off so the brand looks the same on every device.
 */
@Immutable
data class StatusColors(val connected: Color, val pending: Color, val error: Color, val outline: Color, val secondaryText: Color)

private val LightStatus = StatusColors(
    connected = Color(0xFF16A34A),
    pending = Color(0xFFD97706),
    error = Color(0xFFDC2626),
    outline = Color(0xFFE3E8EF),
    secondaryText = Color(0xFF5B6475),
)

private val DarkStatus = StatusColors(
    connected = Color(0xFF22C55E),
    pending = Color(0xFFF59E0B),
    error = Color(0xFFF87171),
    outline = Color(0xFF243049),
    secondaryText = Color(0xFF98A2B3),
)

private val LightScheme = lightColorScheme(
    primary = Color(0xFF2F6FEB),
    onPrimary = Color.White,
    background = Color(0xFFF6F8FB),
    onBackground = Color(0xFF0F172A),
    surface = Color(0xFFFFFFFF),
    onSurface = Color(0xFF0F172A),
    onSurfaceVariant = Color(0xFF5B6475),
    outline = Color(0xFFE3E8EF),
    error = Color(0xFFDC2626),
)

private val DarkScheme = darkColorScheme(
    primary = Color(0xFF5B8DEF),
    onPrimary = Color(0xFF0B1220),
    background = Color(0xFF0B1220),
    onBackground = Color(0xFFE6EAF2),
    surface = Color(0xFF121A2B),
    onSurface = Color(0xFFE6EAF2),
    onSurfaceVariant = Color(0xFF98A2B3),
    outline = Color(0xFF243049),
    error = Color(0xFFF87171),
)

/** Type scale: status 32/40, title 20/28, body 15/22, caption 13/18; system font. */
private val AsterTypography = Typography(
    displaySmall = TextStyle(fontSize = 32.sp, lineHeight = 40.sp, fontWeight = FontWeight.SemiBold),
    titleLarge = TextStyle(fontSize = 20.sp, lineHeight = 28.sp, fontWeight = FontWeight.SemiBold),
    bodyLarge = TextStyle(fontSize = 15.sp, lineHeight = 22.sp),
    bodyMedium = TextStyle(fontSize = 15.sp, lineHeight = 22.sp),
    bodySmall = TextStyle(fontSize = 13.sp, lineHeight = 18.sp),
    labelMedium = TextStyle(fontSize = 13.sp, lineHeight = 18.sp, fontWeight = FontWeight.Medium),
)

/** Numbers that change in place (rates, counters, versions) use tabular figures. */
val TabularNumbers = TextStyle(fontFeatureSettings = "tnum")

val LocalStatusColors = staticCompositionLocalOf { LightStatus }

@Composable
fun AsterTheme(darkTheme: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
    val status = if (darkTheme) DarkStatus else LightStatus
    androidx.compose.runtime.CompositionLocalProvider(LocalStatusColors provides status) {
        MaterialTheme(
            colorScheme = if (darkTheme) DarkScheme else LightScheme,
            typography = AsterTypography,
            content = content,
        )
    }
}
