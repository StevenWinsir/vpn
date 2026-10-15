package com.asterlink.vpn

import android.app.Application

class AsterApp : Application() {
    /** Constructed eagerly, started lazily: the Core starts on first use, not at process start. */
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this)
    }
}
