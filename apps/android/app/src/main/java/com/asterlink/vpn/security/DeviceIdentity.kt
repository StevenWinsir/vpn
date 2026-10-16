package com.asterlink.vpn.security

import com.asterlink.vpn.session.DeviceIdentity
import java.io.File
import java.io.IOException
import java.util.UUID
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

/**
 * The installation's device_id (handoff_app_android.md 5.3): a random UUID created on first
 * use in the app's private files directory. It never reads ANDROID_ID, IMEI or a MAC address,
 * is not backed up, and a reinstall (or a reset emulator) is a new device to the server.
 */
class FileDeviceIdentity(
    private val directory: File,
    private val io: CoroutineDispatcher = Dispatchers.IO,
) : DeviceIdentity {
    companion object {
        private const val NAME = "device_id"
        private val CANONICAL = Regex("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")

        fun isValid(value: String?): Boolean = value != null && CANONICAL.matches(value)
    }

    private val lock = Mutex()
    @Volatile private var cached: String? = null

    override suspend fun deviceId(): String {
        cached?.let { return it }
        return lock.withLock {
            cached ?: withContext(io) { readOrCreate() }.also { cached = it }
        }
    }

    private fun readOrCreate(): String {
        val file = File(directory, NAME)
        val existing = runCatching { file.readText().trim() }.getOrNull()
        if (isValid(existing)) return existing!!
        val fresh = UUID.randomUUID().toString()
        val temporary = File(directory, "$NAME.tmp")
        temporary.writeText(fresh)
        if (!temporary.renameTo(file)) {
            temporary.delete()
            throw IOException("device id not persisted")
        }
        return fresh
    }
}
