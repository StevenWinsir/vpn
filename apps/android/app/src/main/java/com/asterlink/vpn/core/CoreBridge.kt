package com.asterlink.vpn.core

import com.asterlink.core.mobile.Mobile
import com.asterlink.core.mobile.Platform
import java.io.File
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * The process's one Go Core (native/core/mobile through gomobile). [start] is idempotent;
 * the Core lives until [stop] or the process ends. Nothing here logs request or response
 * bodies: they can carry the user's password (login) and must never reach logcat.
 */
class CoreBridge(private val platform: Platform, private val cacheDir: File) : CoreTransport {
    private val lock = Any()
    @Volatile private var started = false

    /** Whether the Go library is loaded and the Core running (hints are pointless before). */
    val isStarted: Boolean get() = started

    /** The last foreground hint; a Core started later (from the tile, in the background) gets it too. */
    @Volatile private var foreground = true

    /** Start's input (mobile.buildConfig): process facts only, never account or node data. */
    @Serializable
    private data class StartConfig(@SerialName("cache_dir") val cacheDir: String)

    fun start() {
        synchronized(lock) {
            if (started) return
            val build = StartConfig(cacheDir = cacheDir.canonicalPath)
            Mobile.start(platform, CoreProtocol.json.encodeToString(StartConfig.serializer(), build))
            started = true
            Mobile.setForeground(foreground)
        }
    }

    override fun call(requestJson: String): String = Mobile.call(requestJson)

    /**
     * Whether a screen of the app is visible (ProcessLifecycleOwner). In the background the
     * Core pauses its catalog long-poll to spare the radio; authorization is unaffected
     * (handoff_app_android.md 7.6). Called on the main thread, so it never takes [lock] (a stop
     * holds it while settling); the Go side is safe at any time and never waits for a command.
     */
    fun setForeground(visible: Boolean) {
        foreground = visible
        // Before the first start the Go library is not even loaded; start() applies the hint.
        if (started) Mobile.setForeground(visible)
    }

    fun stop() {
        synchronized(lock) {
            Mobile.stop()
            started = false
        }
    }
}
