package com.asterlink.vpn.vpn

import android.annotation.SuppressLint
import android.app.PendingIntent
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import com.asterlink.vpn.AsterApp
import com.asterlink.vpn.MainActivity
import com.asterlink.vpn.R
import com.asterlink.vpn.session.TileView
import kotlinx.coroutines.Job
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch

/**
 * The optional Quick Settings tile (handoff_app_android.md 9.3): one tap connects or
 * disconnects when the user is signed in, has chosen a line and has already given VPN
 * consent; in every other case it opens the app, which explains what is missing. It decides
 * nothing itself: the SessionController's state says what a tap may do ([TileView]).
 */
class ConnectTileService : TileService() {
    companion object {
        /** Ask the system to refresh the tile (it binds us and calls onStartListening). */
        fun refresh(context: Context) {
            runCatching { requestListeningState(context, ComponentName(context, ConnectTileService::class.java)) }
        }
    }

    private val container get() = (application as AsterApp).container
    private var scope = MainScope()
    private var listening: Job? = null

    override fun onStartListening() {
        super.onStartListening()
        listening?.cancel()
        listening = scope.launch {
            container.session.state.map(TileView::from).distinctUntilChanged().collect(::render)
        }
    }

    override fun onStopListening() {
        listening?.cancel()
        listening = null
        super.onStopListening()
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    private fun render(view: TileView) {
        val tile = qsTile ?: return
        tile.label = getString(R.string.tile_label)
        tile.state = if (view.active) Tile.STATE_ACTIVE else Tile.STATE_INACTIVE
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            tile.subtitle = getString(
                when (view.action) {
                    TileView.Action.Disconnect -> R.string.tile_connected
                    TileView.Action.Busy -> R.string.tile_connecting
                    TileView.Action.Connect -> R.string.tile_disconnected
                    TileView.Action.OpenApp -> R.string.tile_open_app
                },
            )
        }
        tile.updateTile()
    }

    override fun onClick() {
        super.onClick()
        // Connecting or disconnecting from the lock screen needs the device unlocked.
        if (isSecure) unlockAndRun(::act) else act()
    }

    private fun act() {
        val session = container.session
        when (TileView.from(session.state.value).action) {
            TileView.Action.Disconnect -> session.disconnect()
            TileView.Action.Busy -> Unit
            TileView.Action.Connect -> {
                // Consent cannot be asked for from a tile: without it the app asks.
                val consented = runCatching { VpnService.prepare(this) == null }.getOrDefault(false)
                if (consented) session.connect() else openApp()
            }
            TileView.Action.OpenApp -> openApp()
        }
    }

    @SuppressLint("StartActivityAndCollapseDeprecated")
    private fun openApp() {
        val intent = Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startActivityAndCollapse(PendingIntent.getActivity(this, 0, intent, PendingIntent.FLAG_IMMUTABLE))
        } else {
            @Suppress("DEPRECATION")
            startActivityAndCollapse(intent)
        }
    }
}
