package com.asterlink.vpn.ui.foundation

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.asterlink.vpn.R
import com.asterlink.vpn.core.HelloResult
import com.asterlink.vpn.ui.theme.AsterTheme
import com.asterlink.vpn.ui.theme.LocalStatusColors
import com.asterlink.vpn.ui.theme.TabularNumbers
import com.asterlink.vpn.vpn.HostNetwork

/** Build facts shown next to the Core's own identity. */
data class BuildFacts(val appVersion: String, val channel: String, val apiHost: String, val envFromExample: Boolean)

@Composable
fun FoundationScreen(state: FoundationState, facts: BuildFacts, modifier: Modifier = Modifier) {
    Box(
        modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .safeDrawingPadding(),
    ) {
        Column(
            Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 24.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Text("AsterLink", style = MaterialTheme.typography.displaySmall, color = MaterialTheme.colorScheme.onBackground)
            Text(stringResource(R.string.foundation_subtitle), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            StatusBadge(state)
            Card {
                val hello = (state as? FoundationState.Ready)?.hello
                FactRow(R.string.label_core_version, hello?.coreVersion ?: "—")
                FactRow(R.string.label_mihomo, hello?.mihomoRevision?.take(12) ?: "—")
                FactRow(R.string.label_protocol, hello?.protocolVersion?.toString() ?: "—")
            }
            Card {
                FactRow(R.string.label_app_version, facts.appVersion)
                FactRow(R.string.label_channel, facts.channel)
                FactRow(R.string.label_api_host, facts.apiHost)
                val network = (state as? FoundationState.Ready)?.network
                FactRow(
                    R.string.label_network,
                    when {
                        network == null -> "—"
                        !network.available -> stringResource(R.string.network_none)
                        else -> network.interfaceName.orEmpty()
                    },
                )
            }
            if (facts.envFromExample) {
                Text(stringResource(R.string.env_from_example), style = MaterialTheme.typography.bodySmall, color = LocalStatusColors.current.pending)
            }
            Text(stringResource(R.string.next_round_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun StatusBadge(state: FoundationState) {
    val status = LocalStatusColors.current
    val (color, label) = when (state) {
        FoundationState.Starting -> status.pending to stringResource(R.string.core_starting)
        is FoundationState.Ready -> status.connected to stringResource(R.string.core_ready)
        is FoundationState.Failed -> status.error to stringResource(R.string.core_failed) + " (" + state.code + ")"
    }
    Row(
        // Announced by TalkBack whenever the Core's state changes.
        Modifier.semantics { liveRegion = LiveRegionMode.Polite },
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (state == FoundationState.Starting) {
            CircularProgressIndicator(Modifier.size(16.dp), color = color, strokeWidth = 2.dp)
        } else {
            Box(Modifier.size(10.dp).background(color, CircleShape))
        }
        Spacer(Modifier.width(8.dp))
        Text(label, style = MaterialTheme.typography.titleLarge, color = MaterialTheme.colorScheme.onBackground)
    }
}

@Composable
private fun Card(content: @Composable () -> Unit) {
    Surface(
        shape = RoundedCornerShape(16.dp),
        color = MaterialTheme.colorScheme.surface,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(horizontal = 16.dp, vertical = 8.dp)) { content() }
    }
}

@Composable
private fun FactRow(label: Int, value: String) {
    Row(
        Modifier
            .fillMaxWidth()
            .height(48.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(stringResource(label), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f))
        Text(
            value,
            style = MaterialTheme.typography.bodyMedium.merge(TabularNumbers),
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Preview(showBackground = true)
@Composable
private fun ReadyPreview() {
    AsterTheme(darkTheme = false) {
        FoundationScreen(
            FoundationState.Ready(
                HelloResult("asterlink-core", "0.1.0", 1, 1, 1, "70f0570405c3c2c47bb113b88db95006d239b346"),
                HostNetwork(available = true, interfaceName = "wlan0", index = 3, addresses = emptyList(), dns = emptyList(), metered = false),
            ),
            BuildFacts("0.1.0", "development", "demo.hyshentou.cn", envFromExample = true),
        )
    }
}

@Preview(showBackground = true, backgroundColor = 0xFF0B1220)
@Composable
private fun FailedDarkPreview() {
    AsterTheme(darkTheme = true) {
        FoundationScreen(FoundationState.Failed("core_start_failed"), BuildFacts("0.1.0", "development", "demo.hyshentou.cn", envFromExample = false))
    }
}
