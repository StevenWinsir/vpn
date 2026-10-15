package com.asterlink.vpn.vpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import com.asterlink.vpn.MainActivity
import com.asterlink.vpn.R

/**
 * The VPN's foreground notification (handoff_app_android.md 9.3): low importance, silent,
 * one "disconnect" action that is the same as disconnecting in the app. Rates and the line
 * name arrive with the session UI in round 4. When notifications are denied (Android 13+)
 * the VPN still works and the system key icon still shows.
 */
object VpnNotifications {
    const val CHANNEL = "vpn_status"
    const val ID = 1

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

    fun build(context: Context, connected: Boolean): Notification {
        ensureChannel(context)
        val open = PendingIntent.getActivity(
            context, 0,
            Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val disconnect = PendingIntent.getService(
            context, 1,
            Intent(context, AsterVpnService::class.java).setAction(AsterVpnService.ACTION_DISCONNECT),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val title = context.getString(if (connected) R.string.vpn_state_connected else R.string.vpn_state_connecting)
        return NotificationCompat.Builder(context, CHANNEL)
            .setSmallIcon(R.drawable.ic_vpn_key)
            .setContentTitle(context.getString(R.string.vpn_notification_title, title))
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .addAction(0, context.getString(R.string.vpn_action_disconnect), disconnect)
            .build()
    }
}
