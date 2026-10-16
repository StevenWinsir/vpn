package com.asterlink.vpn.vpn

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.asterlink.vpn.MainActivity
import com.asterlink.vpn.R
import com.asterlink.vpn.session.Notice
import com.asterlink.vpn.session.SessionAlerts
import com.asterlink.vpn.session.TunnelNotice
import com.asterlink.vpn.ui.noticeString

/**
 * The VPN's notifications (handoff_app_android.md 9.3).
 *
 * - The ongoing one: low importance, silent, "AsterLink · 已连接" with the line and the live
 *   rates, one "disconnect" action that is the same as disconnecting in the app. It belongs to
 *   [AsterVpnService], which alone posts and removes it.
 * - The alert: when the tunnel ended without the user asking (quota, plan, line changed or
 *   disabled), on its own channel, so the user is not left believing the VPN still runs.
 *
 * When notifications are denied (Android 13+) the VPN still works and the system key icon
 * still shows; posting is then skipped.
 */
object VpnNotifications {
    const val CHANNEL = "vpn_status"
    const val ALERT_CHANNEL = "vpn_alerts"
    const val ID = 1
    const val ALERT_ID = 2

    fun ensureChannel(context: Context) {
        val manager = context.getSystemService(NotificationManager::class.java)
        if (manager.getNotificationChannel(CHANNEL) != null) return
        val channel = NotificationChannel(CHANNEL, context.getString(R.string.vpn_channel_name), NotificationManager.IMPORTANCE_LOW).apply {
            description = context.getString(R.string.vpn_channel_description)
            setShowBadge(false)
            setSound(null, null)
            enableVibration(false)
        }
        manager.createNotificationChannel(channel)
    }

    private fun ensureAlertChannel(context: Context) {
        val manager = context.getSystemService(NotificationManager::class.java)
        if (manager.getNotificationChannel(ALERT_CHANNEL) != null) return
        val channel = NotificationChannel(ALERT_CHANNEL, context.getString(R.string.alert_channel_name), NotificationManager.IMPORTANCE_DEFAULT).apply {
            description = context.getString(R.string.alert_channel_description)
            setShowBadge(true)
        }
        manager.createNotificationChannel(channel)
    }

    private fun openApp(context: Context): PendingIntent = PendingIntent.getActivity(
        context, 0,
        Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
    )

    fun build(context: Context, notice: TunnelNotice): Notification {
        ensureChannel(context)
        val disconnect = PendingIntent.getService(
            context, 1,
            Intent(context, AsterVpnService::class.java).setAction(AsterVpnService.ACTION_DISCONNECT),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val title = context.getString(if (notice.connected) R.string.vpn_state_connected else R.string.vpn_state_connecting)
        return NotificationCompat.Builder(context, CHANNEL)
            .setSmallIcon(R.drawable.ic_vpn_key)
            .setContentTitle(context.getString(R.string.vpn_notification_title, title))
            .setContentText(notice.text)
            .setContentIntent(openApp(context))
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .setShowWhen(false)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .addAction(0, context.getString(R.string.vpn_action_disconnect), disconnect)
            .build()
    }

    fun mayPost(context: Context): Boolean =
        (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED) &&
            NotificationManagerCompat.from(context).areNotificationsEnabled()

    /** Replaces the ongoing notification's content. Only the running service calls this. */
    fun update(context: Context, notice: TunnelNotice) {
        if (!mayPost(context)) return
        try {
            NotificationManagerCompat.from(context).notify(ID, build(context, notice))
        } catch (error: SecurityException) {
            // The permission was revoked in between.
        }
    }

    fun postStopped(context: Context, reason: String) {
        if (!mayPost(context)) return
        ensureAlertChannel(context)
        val notification = NotificationCompat.Builder(context, ALERT_CHANNEL)
            .setSmallIcon(R.drawable.ic_vpn_key)
            .setContentTitle(context.getString(R.string.alert_title))
            .setContentText(reason)
            .setStyle(NotificationCompat.BigTextStyle().bigText(reason))
            .setContentIntent(openApp(context))
            .setAutoCancel(true)
            .setCategory(NotificationCompat.CATEGORY_STATUS)
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .build()
        try {
            NotificationManagerCompat.from(context).notify(ALERT_ID, notification)
        } catch (error: SecurityException) {
            // The permission was revoked in between.
        }
    }

    fun clearStopped(context: Context) {
        NotificationManagerCompat.from(context).cancel(ALERT_ID)
    }
}

/**
 * [SessionAlerts] as notifications. While the app is on screen the connect page already shows
 * the reason, so the alert is only posted when it is not ([isForeground] is read on the main
 * thread, where the SessionController runs).
 */
class NotificationAlerts(private val context: Context, private val isForeground: () -> Boolean) : SessionAlerts {
    override fun tunnelStopped(reason: Notice) {
        if (isForeground()) return
        VpnNotifications.postStopped(context, context.noticeString(reason))
    }

    override fun clear() = VpnNotifications.clearStopped(context)
}
