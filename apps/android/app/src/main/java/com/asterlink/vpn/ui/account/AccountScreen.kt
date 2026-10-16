package com.asterlink.vpn.ui.account

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
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.asterlink.vpn.R
import com.asterlink.vpn.security.ThemeMode
import com.asterlink.vpn.session.Formatting
import com.asterlink.vpn.session.SessionState
import com.asterlink.vpn.ui.AppActions
import com.asterlink.vpn.ui.BuildFacts
import com.asterlink.vpn.ui.DeviceStatus
import com.asterlink.vpn.ui.WebPage
import com.asterlink.vpn.ui.components.AsterCard
import com.asterlink.vpn.ui.components.AsterIcon
import com.asterlink.vpn.ui.components.FactRow
import com.asterlink.vpn.ui.components.PageTitle
import com.asterlink.vpn.ui.components.Pill
import com.asterlink.vpn.ui.components.SecondaryButton
import com.asterlink.vpn.ui.connect.PlanSummary
import com.asterlink.vpn.ui.theme.Dimens
import com.asterlink.vpn.ui.theme.LocalStatusColors
import java.time.Instant

/**
 * The "me" tab: account, plan (renewal on the website), appearance, background/battery and
 * system VPN settings, about, redacted diagnostics, and sign-out (confirmed; it also deletes
 * the password saved on this device).
 */
@Composable
fun AccountScreen(
    state: SessionState,
    facts: BuildFacts,
    themeMode: ThemeMode,
    actions: AppActions,
    nowInstant: Instant,
    modifier: Modifier = Modifier,
    device: DeviceStatus = DeviceStatus(),
) {
    var confirmSignOut by rememberSaveable { mutableStateOf(false) }
    val snapshot = state.snapshot
    val user = snapshot.user
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = Dimens.PageMargin),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(Modifier.widthIn(max = Dimens.MaxContentWidth).fillMaxWidth()) {
            PageTitle(stringResource(R.string.account_title))
            Spacer(Modifier.height(8.dp))
            AsterCard {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.size(48.dp).background(MaterialTheme.colorScheme.primary, CircleShape), contentAlignment = Alignment.Center) {
                        Text(
                            Formatting.initial(user?.name.orEmpty(), user?.email.orEmpty()),
                            style = MaterialTheme.typography.titleMedium,
                            color = MaterialTheme.colorScheme.onPrimary,
                        )
                    }
                    Spacer(Modifier.width(12.dp))
                    Column(Modifier.weight(1f)) {
                        Text(user?.name?.ifEmpty { null } ?: user?.email.orEmpty(), style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(user?.email.orEmpty(), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                    if (user?.role == "admin") {
                        Pill(stringResource(R.string.role_admin), LocalStatusColors.current.brandContainer, MaterialTheme.colorScheme.primary)
                    }
                }
            }

            Spacer(Modifier.height(12.dp))
            AsterCard {
                Text(stringResource(R.string.account_plan), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.height(8.dp))
                val entitlement = snapshot.entitlement
                if (entitlement == null || (!entitlement.accountAuthorized && entitlement.planName.isEmpty())) {
                    Text(stringResource(R.string.plan_none), style = MaterialTheme.typography.titleMedium)
                    if (entitlement?.reasonMessage?.isNotEmpty() == true) {
                        Text(entitlement.reasonMessage, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                } else {
                    PlanSummary(snapshot, nowInstant)
                    if (!entitlement.accountAuthorized && entitlement.reasonMessage.isNotEmpty()) {
                        Text(entitlement.reasonMessage, style = MaterialTheme.typography.bodySmall, color = LocalStatusColors.current.error)
                    }
                }
                Spacer(Modifier.height(12.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    SecondaryButton(stringResource(R.string.account_renew), { actions.openWebsite(WebPage.Plans) }, Modifier.weight(1f), icon = R.drawable.ic_open_in_new)
                    SecondaryButton(stringResource(R.string.account_orders), { actions.openWebsite(WebPage.Orders) }, Modifier.weight(1f), icon = R.drawable.ic_receipt)
                }
            }

            Spacer(Modifier.height(12.dp))
            AsterCard(contentPadding = androidx.compose.foundation.layout.PaddingValues(vertical = 8.dp)) {
                Column(Modifier.padding(horizontal = 16.dp, vertical = 8.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        AsterIcon(R.drawable.ic_contrast, null, size = 20.dp)
                        Spacer(Modifier.width(12.dp))
                        Text(stringResource(R.string.settings_appearance), style = MaterialTheme.typography.bodyLarge)
                    }
                    Spacer(Modifier.height(8.dp))
                    val modes = listOf(ThemeMode.System to R.string.theme_system, ThemeMode.Light to R.string.theme_light, ThemeMode.Dark to R.string.theme_dark)
                    SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                        modes.forEachIndexed { index, (mode, label) ->
                            SegmentedButton(
                                selected = themeMode == mode,
                                onClick = { actions.setTheme(mode) },
                                shape = SegmentedButtonDefaults.itemShape(index, modes.size),
                                icon = {},
                            ) { Text(stringResource(label), maxLines = 1) }
                        }
                    }
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outline)
                val batteryHint = when (device.batteryUnrestricted) {
                    true -> R.string.settings_battery_unrestricted
                    false -> R.string.settings_battery_restricted
                    null -> R.string.settings_battery_hint
                }
                SettingRow(R.drawable.ic_battery, stringResource(R.string.settings_battery), stringResource(batteryHint), actions.openBatterySettings)
                HorizontalDivider(color = MaterialTheme.colorScheme.outline)
                SettingRow(
                    R.drawable.ic_info,
                    stringResource(R.string.settings_notifications),
                    stringResource(if (device.notificationsAllowed) R.string.settings_notifications_on else R.string.settings_notifications_off),
                    actions.openNotificationSettings,
                )
                HorizontalDivider(color = MaterialTheme.colorScheme.outline)
                SettingRow(R.drawable.ic_key, stringResource(R.string.settings_vpn), stringResource(R.string.settings_vpn_hint), actions.openVpnSettings)
                HorizontalDivider(color = MaterialTheme.colorScheme.outline)
                SettingRow(R.drawable.ic_open_in_new, stringResource(R.string.account_dashboard), null) { actions.openWebsite(WebPage.Dashboard) }
            }

            Spacer(Modifier.height(12.dp))
            AsterCard {
                Text(stringResource(R.string.about_title), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.height(4.dp))
                FactRow(stringResource(R.string.about_app_version), facts.appVersion)
                FactRow(stringResource(R.string.about_core_version), state.identity?.coreVersion ?: snapshot.coreVersion.ifEmpty { "—" })
                FactRow(stringResource(R.string.about_mihomo), state.identity?.mihomoRevision?.take(12) ?: "—")
                FactRow(stringResource(R.string.about_server), facts.apiHost)
                FactRow(stringResource(R.string.about_channel), facts.channel)
                Spacer(Modifier.height(8.dp))
                SecondaryButton(stringResource(R.string.copy_diagnostics), actions.copyDiagnostics, icon = R.drawable.ic_copy)
                Text(
                    stringResource(R.string.copy_diagnostics_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 6.dp, start = 4.dp),
                )
            }

            Spacer(Modifier.height(16.dp))
            SecondaryButton(stringResource(R.string.sign_out), { confirmSignOut = true }, icon = R.drawable.ic_logout, danger = true, enabled = !state.busy)
            Spacer(Modifier.height(24.dp))
        }
    }

    if (confirmSignOut) {
        AlertDialog(
            onDismissRequest = { confirmSignOut = false },
            title = { Text(stringResource(R.string.sign_out_title)) },
            text = { Text(stringResource(R.string.sign_out_body)) },
            confirmButton = {
                TextButton(onClick = {
                    confirmSignOut = false
                    actions.signOut()
                }) { Text(stringResource(R.string.sign_out), color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = { TextButton(onClick = { confirmSignOut = false }) { Text(stringResource(R.string.dialog_cancel)) } },
        )
    }
}

@Composable
private fun SettingRow(icon: Int, title: String, hint: String?, onClick: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .heightIn(min = 56.dp)
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        AsterIcon(icon, null, size = 20.dp)
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            if (hint != null) Text(hint, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        AsterIcon(R.drawable.ic_chevron_right, null)
    }
}
