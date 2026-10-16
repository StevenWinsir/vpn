package com.asterlink.vpn.ui.components

import androidx.annotation.DrawableRes
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.asterlink.vpn.R
import com.asterlink.vpn.session.Notice
import com.asterlink.vpn.ui.noticeRes
import com.asterlink.vpn.ui.theme.Dimens
import com.asterlink.vpn.ui.theme.LocalStatusColors
import java.util.Locale

/** A Core notice shows the Core's own text; a local one its string resource. */
@Composable
@ReadOnlyComposable
fun noticeText(notice: Notice): String = notice.text ?: stringResource(noticeRes(notice.code))

@Composable
fun AsterIcon(@DrawableRes id: Int, contentDescription: String?, modifier: Modifier = Modifier, tint: Color = MaterialTheme.colorScheme.onSurfaceVariant, size: Dp = 24.dp) {
    Icon(painterResource(id), contentDescription = contentDescription, modifier = modifier.size(size), tint = tint)
}

/** Flat card: 16 dp corners, 1 dp outline, no elevation (handoff 9.2). */
@Composable
fun AsterCard(
    modifier: Modifier = Modifier,
    onClick: (() -> Unit)? = null,
    contentPadding: PaddingValues = PaddingValues(16.dp),
    content: @Composable ColumnScope.() -> Unit,
) {
    val shape = RoundedCornerShape(Dimens.CardRadius)
    val border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline)
    val color = MaterialTheme.colorScheme.surface
    if (onClick != null) {
        Surface(onClick = onClick, modifier = modifier.fillMaxWidth(), shape = shape, color = color, border = border) {
            Column(Modifier.padding(contentPadding), content = content)
        }
    } else {
        Surface(modifier = modifier.fillMaxWidth(), shape = shape, color = color, border = border) {
            Column(Modifier.padding(contentPadding), content = content)
        }
    }
}

/** The region code in a rounded square (no flag images: they are unreliable across fonts). */
@Composable
fun RegionBadge(region: String, modifier: Modifier = Modifier, dedicated: Boolean = false) {
    val label = region.trim().take(3).uppercase(Locale.ROOT).ifEmpty { "··" }
    val colors = LocalStatusColors.current
    Box(
        modifier
            .size(40.dp)
            .background(if (dedicated) colors.brandContainer else MaterialTheme.colorScheme.surfaceVariant, RoundedCornerShape(10.dp)),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            label,
            style = MaterialTheme.typography.labelMedium.copy(fontWeight = FontWeight.Bold),
            color = if (dedicated) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
        )
    }
}

/** "专线" / "普通线路" with its billing rate. Never inferred from the type: the rate is the admin's. */
@Composable
fun LineTypeLabel(dedicated: Boolean, rate: String, modifier: Modifier = Modifier) {
    Row(modifier, verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        Pill(
            text = stringResource(if (dedicated) R.string.line_dedicated else R.string.line_direct),
            container = if (dedicated) LocalStatusColors.current.brandContainer else MaterialTheme.colorScheme.surfaceVariant,
            content = if (dedicated) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
            icon = if (dedicated) R.drawable.ic_bolt else null,
        )
        Text(stringResource(R.string.line_rate, rate), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
fun Pill(text: String, container: Color, content: Color, modifier: Modifier = Modifier, @DrawableRes icon: Int? = null) {
    Row(
        modifier
            .background(container, CircleShape)
            .padding(horizontal = 8.dp, vertical = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (icon != null) {
            AsterIcon(icon, null, tint = content, size = 12.dp)
            Spacer(Modifier.width(2.dp))
        }
        Text(text, style = MaterialTheme.typography.labelSmall, color = content, maxLines = 1)
    }
}

/** Full-width 56 dp pill button with an in-place progress indicator. */
@Composable
fun PrimaryButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true, loading: Boolean = false) {
    Button(
        onClick = onClick,
        enabled = enabled && !loading,
        modifier = modifier.fillMaxWidth().defaultMinSize(minHeight = Dimens.ButtonHeight),
        shape = CircleShape,
    ) {
        if (loading) {
            CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp, color = MaterialTheme.colorScheme.onPrimary)
            Spacer(Modifier.width(12.dp))
        }
        Text(text, style = MaterialTheme.typography.labelLarge)
    }
}

@Composable
fun SecondaryButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier, @DrawableRes icon: Int? = null, danger: Boolean = false, enabled: Boolean = true) {
    val color = if (danger) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary
    OutlinedButton(
        onClick = onClick,
        enabled = enabled,
        modifier = modifier.fillMaxWidth().defaultMinSize(minHeight = 48.dp),
        shape = CircleShape,
        border = BorderStroke(1.dp, if (danger) color.copy(alpha = 0.5f) else MaterialTheme.colorScheme.outline),
        colors = ButtonDefaults.outlinedButtonColors(contentColor = color),
    ) {
        if (icon != null) {
            AsterIcon(icon, null, tint = color, size = 18.dp)
            Spacer(Modifier.width(8.dp))
        }
        Text(text, style = MaterialTheme.typography.labelLarge)
    }
}

/** An inline banner for a problem or a warning; the text is read out when it appears. */
@Composable
fun NoticeBanner(text: String, modifier: Modifier = Modifier, error: Boolean = true) {
    val colors = LocalStatusColors.current
    Row(
        modifier
            .fillMaxWidth()
            .background(if (error) colors.errorContainer else colors.pendingContainer, RoundedCornerShape(12.dp))
            .padding(horizontal = 12.dp, vertical = 10.dp),
        verticalAlignment = Alignment.Top,
    ) {
        AsterIcon(if (error) R.drawable.ic_warning else R.drawable.ic_info, null, tint = if (error) colors.error else colors.pending, size = 18.dp)
        Spacer(Modifier.width(8.dp))
        Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurface)
    }
}

/** Empty or blocked state of a page: icon, one sentence, optional action. */
@Composable
fun EmptyState(@DrawableRes icon: Int, text: String, modifier: Modifier = Modifier, action: String? = null, onAction: () -> Unit = {}) {
    Column(modifier.fillMaxWidth().padding(vertical = 40.dp, horizontal = 24.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Box(
            Modifier.size(64.dp).background(MaterialTheme.colorScheme.surfaceVariant, CircleShape),
            contentAlignment = Alignment.Center,
        ) { AsterIcon(icon, null, size = 28.dp) }
        Spacer(Modifier.height(16.dp))
        Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center)
        if (action != null) {
            Spacer(Modifier.height(16.dp))
            SecondaryButton(action, onAction, Modifier.width(240.dp), icon = R.drawable.ic_open_in_new)
        }
    }
}

/** A labelled value row (about section). */
@Composable
fun FactRow(label: String, value: String, modifier: Modifier = Modifier) {
    Row(modifier.fillMaxWidth().padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(label, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f))
        Spacer(Modifier.width(12.dp))
        Text(value, style = MaterialTheme.typography.bodyMedium.merge(com.asterlink.vpn.ui.theme.TabularNumbers), maxLines = 2, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.End)
    }
}

/** The placeholder brand mark (until the official logo is supplied): a star on the brand blue. */
@Composable
fun BrandMark(modifier: Modifier = Modifier, size: Dp = 64.dp) {
    val description = stringResource(R.string.app_name)
    Box(
        modifier
            .size(size)
            .background(MaterialTheme.colorScheme.primary, RoundedCornerShape(size * 0.28f))
            .clearAndSetSemantics { contentDescription = description },
        contentAlignment = Alignment.Center,
    ) {
        Icon(painterResource(R.drawable.ic_launcher_foreground), null, tint = Color.White, modifier = Modifier.size(size * 1.5f))
    }
}

/** Page title, 20 sp. */
@Composable
fun PageTitle(text: String, modifier: Modifier = Modifier, trailing: @Composable () -> Unit = {}) {
    Row(modifier.fillMaxWidth().padding(top = 8.dp, bottom = 4.dp).defaultMinSize(minHeight = 48.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(text, style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f), fontSize = 22.sp)
        trailing()
    }
}
