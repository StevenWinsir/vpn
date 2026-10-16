package com.asterlink.vpn

import android.app.Application
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import com.asterlink.vpn.session.TileView
import com.asterlink.vpn.vpn.ConnectTileService
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch

class AsterApp : Application() {
    /** Constructed eagerly, started lazily: the Core starts on first use, not at process start. */
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this)
        // Back in the foreground: re-read the line catalog (throttled to every 10 s inside the
        // controller; it does nothing until someone has signed in).
        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) {
                container.session.onForeground()
            }
        })
        // The Quick Settings tile is only bound while the shade is open; ask the system to
        // refresh it whenever what a tap would do changes.
        MainScope().launch {
            container.session.state.map(TileView::from).distinctUntilChanged().drop(1).collect { ConnectTileService.refresh(this@AsterApp) }
        }
    }
}
