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
 * Design tokens: one role per website token (frontend/src/app/globals.css), once for light and once for
 * dark; scripts/test-brand-assets.py checks every value against the website. Dynamic (Material You)
 * color is deliberately off so the brand looks the same on every device. Screens only use the roles.
 */
@Immutable
data class BrandTokens(
    val brand: Color,
    val brandFill: Color,
    val onBrand: Color,
    val brandSoft: Color,
    val bg: Color,
    val surface: Color,
    val surface2: Color,
    val border: Color,
    val borderStrong: Color,
    val text: Color,
    val text2: Color,
    val text3: Color,
    val warnText: Color,
    val warnBg: Color,
    val warnBorder: Color,
    val dangerText: Color,
    val dangerBg: Color,
    val focus: Color,
)

val LightTokens = BrandTokens(
    brand = Color(0xFF0D7B61),
    brandFill = Color(0xFF0D7B61),
    onBrand = Color(0xFFFFFFFF),
    brandSoft = Color(0xFFE8F5EF),
    bg = Color(0xFFF7F8FA),
    surface = Color(0xFFFFFFFF),
    surface2 = Color(0xFFF3F7F5),
    border = Color(0xFFE9EEED),
    borderStrong = Color(0xFFCDD9D4),
    text = Color(0xFF172D2B),
    text2 = Color(0xFF566A66),
    text3 = Color(0xFF66756F),
    warnText = Color(0xFF7C2D12),
    warnBg = Color(0xFFFFF4EC),
    warnBorder = Color(0xFFF3C7A8),
    dangerText = Color(0xFFB91C1C),
    dangerBg = Color(0xFFFDECEC),
    focus = Color(0xFF3DC89A),
)

val DarkTokens = BrandTokens(
    brand = Color(0xFF3DC89A),
    brandFill = Color(0xFF0D7B61),
    onBrand = Color(0xFFFFFFFF),
    brandSoft = Color(0x213DC89A),
    bg = Color(0xFF121A18),
    surface = Color(0xFF19221F),
    surface2 = Color(0xFF212C29),
    border = Color(0xFF2A3633),
    borderStrong = Color(0xFF3D4D49),
    text = Color(0xFFDBE5E2),
    text2 = Color(0xFFB4C3BF),
    text3 = Color(0xFF93A5A0),
    warnText = Color(0xFFFDBA74),
    warnBg = Color(0x1FEA580C),
    warnBorder = Color(0x66FB923C),
    dangerText = Color(0xFFFF8787),
    dangerBg = Color(0x1FFA5252),
    focus = Color(0xFF3DC89A),
)

/** The state colours the screens use, derived from the tokens. Connected is the brand green. */
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
    val brandFill: Color,
    val onBrand: Color,
)

private fun BrandTokens.status() = StatusColors(
    connected = brand,
    pending = warnText,
    error = dangerText,
    outline = border,
    secondaryText = text2,
    connectedContainer = brandSoft,
    pendingContainer = warnBg,
    errorContainer = dangerBg,
    brandContainer = brandSoft,
    brandFill = brandFill,
    onBrand = onBrand,
)

private val LightStatus = LightTokens.status()
private val DarkStatus = DarkTokens.status()

private val LightScheme = lightColorScheme(
    primary = LightTokens.brand,
    onPrimary = LightTokens.onBrand,
    primaryContainer = LightTokens.brandSoft,
    onPrimaryContainer = LightTokens.brand,
    secondaryContainer = LightTokens.brandSoft,
    onSecondaryContainer = LightTokens.brand,
    background = LightTokens.bg,
    onBackground = LightTokens.text,
    surface = LightTokens.surface,
    onSurface = LightTokens.text,
    surfaceVariant = LightTokens.surface2,
    onSurfaceVariant = LightTokens.text2,
    surfaceContainer = LightTokens.surface,
    surfaceContainerLow = LightTokens.surface,
    surfaceContainerHigh = LightTokens.surface2,
    surfaceContainerHighest = LightTokens.border,
    outline = LightTokens.border,
    outlineVariant = LightTokens.border,
    error = LightTokens.dangerText,
    onError = LightTokens.onBrand,
)

private val DarkScheme = darkColorScheme(
    primary = DarkTokens.brand,
    onPrimary = DarkTokens.bg,
    primaryContainer = DarkTokens.brandSoft,
    onPrimaryContainer = DarkTokens.brand,
    secondaryContainer = DarkTokens.brandSoft,
    onSecondaryContainer = DarkTokens.brand,
    background = DarkTokens.bg,
    onBackground = DarkTokens.text,
    surface = DarkTokens.surface,
    onSurface = DarkTokens.text,
    surfaceVariant = DarkTokens.surface2,
    onSurfaceVariant = DarkTokens.text2,
    surfaceContainer = DarkTokens.surface,
    surfaceContainerLow = DarkTokens.surface,
    surfaceContainerHigh = DarkTokens.surface2,
    surfaceContainerHighest = DarkTokens.border,
    outline = DarkTokens.border,
    outlineVariant = DarkTokens.border,
    error = DarkTokens.dangerText,
    onError = DarkTokens.bg,
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
