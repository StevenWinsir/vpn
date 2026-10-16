package com.asterlink.vpn.ui.navigation

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.asterlink.vpn.R
import com.asterlink.vpn.security.ThemeMode
import com.asterlink.vpn.session.AppPhase
import com.asterlink.vpn.session.SessionState
import com.asterlink.vpn.ui.AppActions
import com.asterlink.vpn.ui.BuildFacts
import com.asterlink.vpn.ui.DeviceStatus
import com.asterlink.vpn.ui.account.AccountScreen
import com.asterlink.vpn.ui.components.AsterIcon
import com.asterlink.vpn.ui.components.BrandMark
import com.asterlink.vpn.ui.components.noticeText
import com.asterlink.vpn.ui.connect.ConnectScreen
import com.asterlink.vpn.ui.lines.LinesScreen
import com.asterlink.vpn.ui.login.LoginScreen
import java.time.Instant

enum class Tab { Connect, Lines, Account }

/** Wall and monotonic time for the connected timer and the days-left label (fixed in screenshot tests). */
data class Clock(val elapsedMillis: Long, val now: Instant)

/**
 * The single activity's content. Signed out: the login screen. Signed in: three tabs (connect,
 * lines, me) under a bottom navigation bar. A plain state machine instead of a navigation
 * library: there are four destinations and no deep links, so a back stack adds nothing.
 */
@Composable
fun AsterRoot(
    state: SessionState,
    facts: BuildFacts,
    themeMode: ThemeMode,
    actions: AppActions,
    clock: Clock,
    initialTab: Tab = Tab.Connect,
    device: DeviceStatus = DeviceStatus(),
) {
    when (state.phase) {
        AppPhase.Starting -> Starting()
        AppPhase.SignedOut, AppPhase.SigningIn -> LoginScreen(state, facts, actions)
        AppPhase.SignedIn -> SignedIn(state, facts, themeMode, actions, clock, initialTab, device)
    }
}

@Composable
private fun Starting() {
    Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Box(contentAlignment = Alignment.Center) {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                BrandMark()
                Spacer(Modifier.height(24.dp))
                CircularProgressIndicator()
            }
        }
    }
}

@Composable
private fun SignedIn(state: SessionState, facts: BuildFacts, themeMode: ThemeMode, actions: AppActions, clock: Clock, initialTab: Tab, device: DeviceStatus) {
    var tab by rememberSaveable { mutableStateOf(initialTab) }
    val snackbar = remember { SnackbarHostState() }
    val warning = state.warning?.let { noticeText(it) }
    LaunchedEffect(warning) {
        if (warning != null) {
            snackbar.showSnackbar(warning)
            actions.dismissMessage()
        }
    }
    Scaffold(
        containerColor = MaterialTheme.colorScheme.background,
        snackbarHost = { SnackbarHost(snackbar) },
        bottomBar = {
            NavigationBar(containerColor = MaterialTheme.colorScheme.surface, tonalElevation = 0.dp) {
                val items = listOf(
                    Triple(Tab.Connect, R.drawable.ic_power, R.string.tab_connect),
                    Triple(Tab.Lines, R.drawable.ic_lines, R.string.tab_lines),
                    Triple(Tab.Account, R.drawable.ic_person, R.string.tab_account),
                )
                items.forEach { (value, icon, label) ->
                    NavigationBarItem(
                        selected = tab == value,
                        onClick = { tab = value },
                        icon = { AsterIcon(icon, null, tint = LocalContentColor.current) },
                        label = { Text(stringResource(label)) },
                        colors = NavigationBarItemDefaults.colors(
                            selectedIconColor = MaterialTheme.colorScheme.primary,
                            selectedTextColor = MaterialTheme.colorScheme.primary,
                            indicatorColor = MaterialTheme.colorScheme.primaryContainer,
                        ),
                    )
                }
            }
        },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding).consumeWindowInsets(padding).safeDrawingPadding()) {
            when (tab) {
                Tab.Connect -> ConnectScreen(
                    state, actions, clock.elapsedMillis, clock.now,
                    onOpenLines = { tab = Tab.Lines },
                    onOpenAccount = { tab = Tab.Account },
                )
                Tab.Lines -> LinesScreen(state, actions)
                Tab.Account -> AccountScreen(state, facts, themeMode, actions, clock.now, device = device)
            }
        }
    }
}
