package com.asterlink.vpn.ui.lines

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.asterlink.vpn.R
import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.session.Formatting
import com.asterlink.vpn.session.LineFilter
import com.asterlink.vpn.session.LocalCodes
import com.asterlink.vpn.session.NodeList
import com.asterlink.vpn.session.NodeRow
import com.asterlink.vpn.session.SessionState
import com.asterlink.vpn.ui.AppActions
import com.asterlink.vpn.ui.WebPage
import com.asterlink.vpn.ui.components.AsterIcon
import com.asterlink.vpn.ui.components.EmptyState
import com.asterlink.vpn.ui.components.LineTypeLabel
import com.asterlink.vpn.ui.components.NoticeBanner
import com.asterlink.vpn.ui.components.PageTitle
import com.asterlink.vpn.ui.components.RegionBadge
import com.asterlink.vpn.ui.components.noticeText
import com.asterlink.vpn.ui.theme.Dimens
import com.asterlink.vpn.ui.theme.LocalStatusColors

/**
 * The lines tab: search, a dedicated/standard filter and the lines grouped by region in the
 * server's order. Only metadata the Core exposes is shown (never an address, port or YAML).
 * Switching while connected asks first: the old segment is stopped and settled, and the app
 * stays disconnected until the user connects again.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LinesScreen(state: SessionState, actions: AppActions, modifier: Modifier = Modifier, initialQuery: String = "", initialFilter: LineFilter = LineFilter.All) {
    var query by rememberSaveable { mutableStateOf(initialQuery) }
    var filter by rememberSaveable { mutableStateOf(initialFilter) }
    var confirm by rememberSaveable { mutableStateOf<String?>(null) }
    val snapshot = state.snapshot
    val dedicatedLabel = stringResource(R.string.line_dedicated)
    val directLabel = stringResource(R.string.line_direct)
    val groups = NodeList.build(snapshot, query, filter) { dedicated -> if (dedicated) dedicatedLabel else directLabel }
    val locked = state.busy || snapshot.state in setOf(CoreSnapshot.State.PREPARING, CoreSnapshot.State.CONNECTING, CoreSnapshot.State.STOPPING)

    Column(modifier.fillMaxSize().padding(horizontal = Dimens.PageMargin)) {
        PageTitle(stringResource(R.string.lines_title)) {
            IconButton(onClick = actions.refresh, enabled = !state.busy) {
                AsterIcon(R.drawable.ic_refresh, stringResource(R.string.lines_refresh))
            }
        }
        if (state.busy) LinearProgressIndicator(Modifier.fillMaxWidth().height(2.dp)) else Spacer(Modifier.height(2.dp))
        Spacer(Modifier.height(8.dp))
        OutlinedTextField(
            value = query,
            onValueChange = { query = it },
            placeholder = { Text(stringResource(R.string.lines_search)) },
            leadingIcon = { AsterIcon(R.drawable.ic_search, null) },
            singleLine = true,
            shape = RoundedCornerShape(Dimens.CardRadius),
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(12.dp))
        val options = listOf(LineFilter.All to R.string.filter_all, LineFilter.Dedicated to R.string.filter_dedicated, LineFilter.Direct to R.string.filter_direct)
        SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
            options.forEachIndexed { index, (value, label) ->
                SegmentedButton(
                    selected = filter == value,
                    onClick = { filter = value },
                    shape = SegmentedButtonDefaults.itemShape(index, options.size),
                    icon = {},
                ) { Text(stringResource(label), maxLines = 1) }
            }
        }
        state.message?.let {
            Spacer(Modifier.height(12.dp))
            NoticeBanner(noticeText(it))
        }
        Spacer(Modifier.height(8.dp))
        PullToRefreshBox(isRefreshing = false, onRefresh = actions.refresh, modifier = Modifier.fillMaxSize()) {
            if (groups.isEmpty()) {
                val reason = NodeList.emptyReason(snapshot, filtered = query.isNotBlank() || filter != LineFilter.All) ?: LocalCodes.NO_MATCH
                LazyColumn(Modifier.fillMaxSize()) {
                    item {
                        when (reason) {
                            LocalCodes.NO_PLAN -> EmptyState(R.drawable.ic_lines, stringResource(R.string.notice_no_plan_lines), action = stringResource(R.string.action_buy_plan)) {
                                actions.openWebsite(WebPage.Plans)
                            }
                            LocalCodes.NO_LINES -> EmptyState(R.drawable.ic_cloud_off, stringResource(R.string.notice_no_lines))
                            LocalCodes.SYNCING -> EmptyState(R.drawable.ic_refresh, stringResource(R.string.notice_syncing))
                            else -> EmptyState(R.drawable.ic_search, stringResource(R.string.notice_no_match))
                        }
                    }
                }
            } else {
                LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    groups.forEach { group ->
                        item(key = "region:" + group.region) {
                            Row(Modifier.fillMaxWidth().padding(top = 12.dp, bottom = 4.dp, start = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                                Text(
                                    group.region.ifEmpty { stringResource(R.string.lines_region_other) },
                                    style = MaterialTheme.typography.labelMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    modifier = Modifier.weight(1f),
                                )
                                Text(stringResource(R.string.lines_count, group.rows.size), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                        items(group.rows, key = { it.id }) { row ->
                            LineRow(row, enabled = !locked) {
                                when {
                                    row.selected && row.configReady -> Unit
                                    snapshot.state == CoreSnapshot.State.CONNECTED -> confirm = row.id
                                    else -> actions.select(row.id)
                                }
                            }
                        }
                    }
                    item { Spacer(Modifier.height(24.dp)) }
                }
            }
        }
    }

    confirm?.let { target ->
        AlertDialog(
            onDismissRequest = { confirm = null },
            title = { Text(stringResource(R.string.switch_line_title)) },
            text = { Text(stringResource(R.string.switch_line_body)) },
            confirmButton = {
                TextButton(onClick = {
                    confirm = null
                    actions.switchTo(target)
                }) { Text(stringResource(R.string.switch_line_confirm)) }
            },
            dismissButton = { TextButton(onClick = { confirm = null }) { Text(stringResource(R.string.dialog_cancel)) } },
        )
    }
}

@Composable
private fun LineRow(row: NodeRow, enabled: Boolean, onClick: () -> Unit) {
    val colors = LocalStatusColors.current
    val selectedBorder = MaterialTheme.colorScheme.primary
    Row(
        Modifier
            .fillMaxWidth()
            .heightIn(min = 64.dp)
            .background(
                if (row.selected) colors.brandContainer else MaterialTheme.colorScheme.surface,
                RoundedCornerShape(Dimens.CardRadius),
            )
            .border(1.dp, if (row.selected) selectedBorder else MaterialTheme.colorScheme.outline, RoundedCornerShape(Dimens.CardRadius))
            .semantics { selected = row.selected }
            .clickable(enabled = enabled, role = Role.RadioButton, onClick = onClick)
            .padding(horizontal = 12.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RegionBadge(row.region, dedicated = row.dedicated)
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(row.name, style = MaterialTheme.typography.titleSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            Spacer(Modifier.height(2.dp))
            LineTypeLabel(row.dedicated, Formatting.rate(row.ratePermille))
        }
        if (row.selected) {
            Box(Modifier.size(28.dp).background(selectedBorder, CircleShape), contentAlignment = Alignment.Center) {
                AsterIcon(R.drawable.ic_check, stringResource(R.string.lines_selected), tint = MaterialTheme.colorScheme.onPrimary, size = 18.dp)
            }
        }
    }
}
