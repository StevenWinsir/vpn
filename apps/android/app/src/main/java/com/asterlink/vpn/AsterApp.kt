package com.asterlink.vpn

import android.app.Application
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner

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
    }
}
