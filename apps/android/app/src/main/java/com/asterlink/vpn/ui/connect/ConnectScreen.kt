package com.asterlink.vpn.ui.connect

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.scale
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.asterlink.vpn.R
import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.core.MeteringView
import com.asterlink.vpn.session.ConnectionStatus
import com.asterlink.vpn.session.ConnectionView
import com.asterlink.vpn.session.Formatting
import com.asterlink.vpn.session.SessionState
import com.asterlink.vpn.session.Tone
import com.asterlink.vpn.ui.AppActions
import com.asterlink.vpn.ui.WebPage
import com.asterlink.vpn.ui.components.AsterCard
import com.asterlink.vpn.ui.components.AsterIcon
import com.asterlink.vpn.ui.components.LineTypeLabel
import com.asterlink.vpn.ui.components.NoticeBanner
import com.asterlink.vpn.ui.components.Pill
import com.asterlink.vpn.ui.components.RegionBadge
import com.asterlink.vpn.ui.components.SecondaryButton
import com.asterlink.vpn.ui.components.noticeText
import com.asterlink.vpn.ui.theme.Dimens
import com.asterlink.vpn.ui.theme.LocalStatusColors
import com.asterlink.vpn.ui.theme.TabularNumbers
import java.time.Instant

/**
 * The connect tab (handoff_app_android.md 9.3): the status is the visual centre; one primary
 * action; the selected line; live rates only while connected; the confirmed plan balance with
 * any estimate on its own line. Nothing here invents a latency or a speed test.
 */
@Composable
fun ConnectScreen(
    state: SessionState,
    actions: AppActions,
    nowMillis: Long,
    nowInstant: Instant,
    onOpenLines: () -> Unit,
    onOpenAccount: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val view = state.connection
    val snapshot = state.snapshot
    Column(
        modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = Dimens.PageMargin),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        TopBar(snapshot, onOpenAccount)
        Column(Modifier.widthIn(max = Dimens.MaxContentWidth).fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
            Spacer(Modifier.height(24.dp))
            val statusLabel = statusText(view.status)
            ConnectButton(view, statusLabel) {
                when {
                    view.showDisconnect -> actions.disconnect()
                    view.showConnect && view.canConnect -> actions.connect()
                }
            }
            Spacer(Modifier.height(20.dp))
            val since = state.connectedSinceMillis
            val title = if (view.status == ConnectionStatus.Connected && since != null) {
                stringResource(R.string.status_connected_for, Formatting.elapsed(nowMillis - since))
            } else statusLabel
            Text(
                title,
                style = MaterialTheme.typography.displaySmall.merge(TabularNumbers),
                color = toneColor(view.tone, MaterialTheme.colorScheme.onBackground),
                textAlign = TextAlign.Center,
                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
            )
            Spacer(Modifier.height(6.dp))
            val detail = view.failure?.let { noticeText(it) } ?: view.blocker?.let { noticeText(it) } ?: detailText(view.status)
            if (detail != null) {
                Text(
                    detail,
                    style = MaterialTheme.typography.bodyMedium,
                    color = if (view.failure != null || (view.blocker != null && view.tone == Tone.Bad)) LocalStatusColors.current.error
                    else MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                )
            }
            if (view.cancellable) {
                Spacer(Modifier.height(8.dp))
                TextButton(onClick = actions.cancelConnect) { Text(stringResource(R.string.action_cancel_connect)) }
                Text(
                    stringResource(R.string.cancel_connect_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                )
            }
            if (view.needsPurchase) {
                Spacer(Modifier.height(16.dp))
                SecondaryButton(stringResource(R.string.action_buy_plan), { actions.openWebsite(WebPage.Plans) }, Modifier.widthIn(max = 320.dp), icon = R.drawable.ic_open_in_new)
            }
            state.message?.let {
                Spacer(Modifier.height(16.dp))
                NoticeBanner(noticeText(it))
            }
            Spacer(Modifier.height(28.dp))
            // Without a plan there is no line to pick: the purchase button above is the way on.
            if (snapshot.entitlement?.accountAuthorized == true) {
                LineCard(snapshot, onOpenLines)
                Spacer(Modifier.height(12.dp))
            }
            val metering = snapshot.metering
            if (view.status == ConnectionStatus.Connected && metering != null) {
                TrafficCard(metering)
                Spacer(Modifier.height(12.dp))
            }
            PlanCard(snapshot, nowInstant, onOpenAccount)
            Spacer(Modifier.height(24.dp))
        }
    }
}

@Composable
private fun TopBar(snapshot: CoreSnapshot, onOpenAccount: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(top = 8.dp).height(48.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(stringResource(R.string.app_name), style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f))
        val plan = snapshot.entitlement?.takeIf { it.accountAuthorized }?.planName?.ifEmpty { null }
        val colors = LocalStatusColors.current
        Row(
            Modifier
                .background(if (plan != null) colors.brandContainer else MaterialTheme.colorScheme.surfaceVariant, CircleShape)
                .clickable(role = Role.Button, onClick = onOpenAccount)
                .padding(start = 12.dp, end = 6.dp, top = 6.dp, bottom = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                plan ?: stringResource(R.string.plan_none),
                style = MaterialTheme.typography.labelMedium,
                color = if (plan != null) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.widthIn(max = 160.dp),
            )
            AsterIcon(R.drawable.ic_chevron_right, null, size = 18.dp, tint = if (plan != null) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
fun statusText(status: ConnectionStatus): String = stringResource(
    when (status) {
        ConnectionStatus.Disconnected -> R.string.status_disconnected
        ConnectionStatus.Preparing -> R.string.status_preparing
        ConnectionStatus.Connecting -> R.string.status_connecting
        ConnectionStatus.Connected -> R.string.status_connected
        ConnectionStatus.Stopping -> R.string.status_stopping
        ConnectionStatus.Unavailable -> R.string.status_unavailable
    },
)

@Composable
private fun detailText(status: ConnectionStatus): String? = when (status) {
    ConnectionStatus.Connected -> stringResource(R.string.status_detail_connected)
    ConnectionStatus.Preparing -> stringResource(R.string.status_detail_preparing)
    ConnectionStatus.Connecting -> stringResource(R.string.status_detail_connecting)
    ConnectionStatus.Stopping -> stringResource(R.string.status_detail_stopping)
    else -> null
}

@Composable
private fun toneColor(tone: Tone, idle: Color): Color {
    val colors = LocalStatusColors.current
    return when (tone) {
        Tone.Good -> colors.connected
        Tone.Busy -> idle
        Tone.Bad -> idle
        Tone.Idle -> idle
    }
}

/** The 168 dp button: a ring shows the state (sweeping while busy), the disc is the action. */
@Composable
private fun ConnectButton(view: ConnectionView, statusLabel: String, onClick: () -> Unit) {
    val colors = LocalStatusColors.current
    val scheme = MaterialTheme.colorScheme
    val enabled = view.showDisconnect || (view.showConnect && view.canConnect)
    val accent by animateColorAsState(
        when {
            view.tone == Tone.Good -> colors.connected
            view.tone == Tone.Busy -> colors.pending
            enabled -> scheme.primary
            else -> scheme.outline
        },
        animationSpec = tween(250),
        label = "accent",
    )
    val disc by animateColorAsState(
        when {
            view.tone == Tone.Good -> colors.connected
            enabled && view.showConnect -> scheme.primary
            else -> scheme.surfaceVariant
        },
        animationSpec = tween(250),
        label = "disc",
    )
    val scale by animateFloatAsState(if (view.tone == Tone.Good) 1f else 0.96f, animationSpec = tween(300), label = "scale")
    val sweep = if (view.showProgress) {
        val transition = rememberInfiniteTransition(label = "sweep")
        transition.animateFloat(0f, 360f, infiniteRepeatable(tween(1200, easing = LinearEasing), RepeatMode.Restart), label = "angle").value
    } else 0f
    val description = stringResource(R.string.cd_connect_button, statusLabel)
    val track = scheme.outline
    Box(
        Modifier
            .size(Dimens.ConnectButton)
            .scale(scale)
            // The clickable node (outer) keeps its own semantics, including "disabled"; only the
            // drawing inside is replaced by one description of the state.
            .clickable(enabled = enabled, onClick = onClick, role = Role.Button)
            .clearAndSetSemantics { contentDescription = description },
        contentAlignment = Alignment.Center,
    ) {
        Canvas(Modifier.fillMaxSize()) {
            val stroke = 6.dp.toPx()
            val inset = stroke / 2
            val arcSize = Size(size.width - stroke, size.height - stroke)
            drawArc(track, 0f, 360f, false, Offset(inset, inset), arcSize, style = Stroke(stroke))
            if (view.showProgress) {
                drawArc(accent, sweep - 90f, 90f, false, Offset(inset, inset), arcSize, style = Stroke(stroke, cap = StrokeCap.Round))
            } else if (view.tone == Tone.Good || enabled) {
                drawArc(accent.copy(alpha = if (view.tone == Tone.Good) 1f else 0.35f), 0f, 360f, false, Offset(inset, inset), arcSize, style = Stroke(stroke))
            }
        }
        Box(
            Modifier.size(Dimens.ConnectButton - 32.dp).background(disc, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            AsterIcon(
                R.drawable.ic_power,
                null,
                size = 56.dp,
                tint = if (view.tone == Tone.Good || (enabled && view.showConnect)) Color.White else scheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun LineCard(snapshot: CoreSnapshot, onOpenLines: () -> Unit) {
    val node = snapshot.selectedNode
    AsterCard(onClick = onOpenLines) {
        Text(stringResource(R.string.current_line), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.height(8.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (node != null) {
                RegionBadge(node.region, dedicated = node.lineType == "dedicated")
                Spacer(Modifier.width(12.dp))
                Column(Modifier.weight(1f)) {
                    Text(node.name, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Spacer(Modifier.height(2.dp))
                    LineTypeLabel(node.lineType == "dedicated", Formatting.rate(node.ratePermille))
                }
            } else {
                Text(stringResource(R.string.choose_line), style = MaterialTheme.typography.titleMedium, color = MaterialTheme.colorScheme.primary, modifier = Modifier.weight(1f))
            }
            AsterIcon(R.drawable.ic_chevron_right, null)
        }
    }
}

@Composable
private fun TrafficCard(metering: MeteringView) {
    AsterCard {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            Rate(R.drawable.ic_arrow_up, stringResource(R.string.upload), Formatting.speed(metering.uploadBytesPerSecond), Modifier.weight(1f))
            Rate(R.drawable.ic_arrow_down, stringResource(R.string.download), Formatting.speed(metering.downloadBytesPerSecond), Modifier.weight(1f))
        }
        if (metering.history.size >= 2) {
            Spacer(Modifier.height(12.dp))
            Sparkline(metering, Modifier.fillMaxWidth().height(48.dp))
        }
        Spacer(Modifier.height(12.dp))
        val physical = metering.uploadBytes + metering.downloadBytes
        val billed = Formatting.billedBytes(physical, metering.ratePermille)
        Text(
            stringResource(R.string.traffic_session, Formatting.bytes(physical), Formatting.bytes(billed)),
            style = MaterialTheme.typography.bodySmall.merge(TabularNumbers),
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            stringResource(if (metering.source == "node_reported") R.string.metering_node else R.string.metering_client),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (metering.pending) {
            Text(stringResource(R.string.traffic_pending), style = MaterialTheme.typography.bodySmall, color = LocalStatusColors.current.pending)
        }
    }
}

@Composable
private fun Rate(icon: Int, label: String, value: String, modifier: Modifier) {
    Row(modifier, verticalAlignment = Alignment.CenterVertically) {
        AsterIcon(icon, label, size = 18.dp, tint = MaterialTheme.colorScheme.primary)
        Spacer(Modifier.width(6.dp))
        Text(value, style = MaterialTheme.typography.titleMedium.merge(TabularNumbers), maxLines = 1)
    }
}

/** The recent rate samples (bounded by the Core), drawn without axes: shape, not a measurement claim. */
@Composable
private fun Sparkline(metering: MeteringView, modifier: Modifier) {
    val down = LocalStatusColors.current.connected
    val up = MaterialTheme.colorScheme.primary
    val samples = metering.history.takeLast(60)
    Canvas(modifier.clearAndSetSemantics { }) {
        val peak = samples.maxOf { maxOf(it.upload, it.download) }.coerceAtLeast(1).toFloat()
        fun line(values: List<Long>, color: Color) {
            val path = Path()
            values.forEachIndexed { index, value ->
                val x = size.width * index / (values.size - 1).coerceAtLeast(1)
                val y = size.height - size.height * (value / peak)
                if (index == 0) path.moveTo(x, y) else path.lineTo(x, y)
            }
            drawPath(path, color, style = Stroke(2.dp.toPx(), cap = StrokeCap.Round))
        }
        line(samples.map { it.download }, down)
        line(samples.map { it.upload }, up)
    }
}

@Composable
private fun PlanCard(snapshot: CoreSnapshot, now: Instant, onOpenAccount: () -> Unit) {
    val entitlement = snapshot.entitlement
    AsterCard(onClick = onOpenAccount) {
        if (entitlement == null || !entitlement.accountAuthorized && entitlement.planName.isEmpty()) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(stringResource(R.string.plan_none), style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                AsterIcon(R.drawable.ic_chevron_right, null)
            }
            return@AsterCard
        }
        PlanSummary(snapshot, now)
    }
}

/** Plan name, expiry, the confirmed balance bar and (separately) any estimate. Shared with the account tab. */
@Composable
fun PlanSummary(snapshot: CoreSnapshot, now: Instant) {
    val entitlement = snapshot.entitlement ?: return
    val colors = LocalStatusColors.current
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(entitlement.planName.ifEmpty { stringResource(R.string.plan_none) }, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
        val expires = entitlement.subscriptionExpires
        val days = Formatting.daysLeft(expires, now)
        if (expires != null && days != null) {
            Pill(
                text = if (days > 0) stringResource(R.string.plan_expires, Formatting.date(expires), days.toInt())
                else stringResource(R.string.plan_expired_on, Formatting.date(expires)),
                container = if (days in 1..7 || days == 0L) colors.pendingContainer else MaterialTheme.colorScheme.surfaceVariant,
                content = if (days == 0L) colors.error else MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    if (entitlement.totalBytes > 0) {
        Spacer(Modifier.height(12.dp))
        // The bar is the confirmed balance left, matching the label under it.
        val left = 1f - Formatting.usedFraction(entitlement.totalBytes, entitlement.remainingBytes)
        LinearProgressIndicator(
            progress = { left },
            modifier = Modifier.fillMaxWidth().height(8.dp),
            color = if (entitlement.remainingBytes <= 0) colors.error else MaterialTheme.colorScheme.primary,
            trackColor = MaterialTheme.colorScheme.surfaceVariant,
            strokeCap = StrokeCap.Round,
            gapSize = 0.dp,
            drawStopIndicator = {},
        )
        Spacer(Modifier.height(8.dp))
        Row(verticalAlignment = Alignment.Bottom) {
            Text(
                stringResource(R.string.plan_remaining, Formatting.bytes(entitlement.remainingBytes)),
                style = MaterialTheme.typography.titleSmall.merge(TabularNumbers),
                modifier = Modifier.weight(1f),
            )
            Text(
                stringResource(R.string.plan_total, Formatting.bytes(entitlement.totalBytes)),
                style = MaterialTheme.typography.bodySmall.merge(TabularNumbers),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        val estimate = snapshot.metering?.estimatedRemainingBytes
        if (estimate != null && snapshot.state == CoreSnapshot.State.CONNECTED && estimate != entitlement.remainingBytes) {
            Text(
                stringResource(R.string.plan_estimated, Formatting.bytes(estimate)),
                style = MaterialTheme.typography.bodySmall.merge(TabularNumbers),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Text(stringResource(R.string.plan_units_note), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}
