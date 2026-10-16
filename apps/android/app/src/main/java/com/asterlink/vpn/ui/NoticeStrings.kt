package com.asterlink.vpn.ui

import android.content.Context
import androidx.annotation.StringRes
import com.asterlink.vpn.R
import com.asterlink.vpn.session.LocalCodes
import com.asterlink.vpn.session.Notice

/**
 * The string resource for a local notice code. A Core refusal carries its own Chinese text and
 * never comes here; unknown codes get the generic failure text. Shared by the screens and by
 * the notifications (which render outside Compose).
 */
@StringRes
fun noticeRes(code: String): Int = when (code) {
    LocalCodes.INPUT_MISSING -> R.string.notice_input_missing
    LocalCodes.EMAIL_TOO_LONG -> R.string.notice_email_too_long
    LocalCodes.PASSWORD_TOO_LONG -> R.string.notice_password_too_long
    LocalCodes.CORE_START_FAILED -> R.string.notice_core_start_failed
    LocalCodes.CORE_LOST, "core_not_started" -> R.string.notice_core_lost
    LocalCodes.CANCELLED -> R.string.notice_connect_cancelled
    LocalCodes.SESSION_ENDED -> R.string.notice_session_ended
    LocalCodes.STATE_CHANGED, "stale_generation" -> R.string.notice_state_changed
    "invalid_request", "payload_too_large", "unknown_command", "protocol_unsupported", "not_implemented" -> R.string.notice_invalid_request
    LocalCodes.VPN_PERMISSION_DENIED -> R.string.notice_vpn_permission_denied
    LocalCodes.VPN_START_FAILED -> R.string.notice_vpn_start_failed
    LocalCodes.VPN_STOPPING -> R.string.notice_vpn_stopping
    LocalCodes.CONNECT_TIMEOUT -> R.string.notice_connect_timeout
    LocalCodes.CREDENTIAL_READ_FAILED -> R.string.notice_credential_read_failed
    LocalCodes.CREDENTIAL_SAVE_FAILED -> R.string.notice_credential_save_failed
    LocalCodes.CREDENTIAL_DELETE_FAILED -> R.string.notice_credential_delete_failed
    LocalCodes.NO_PLAN -> R.string.notice_no_plan
    LocalCodes.NO_LINES -> R.string.notice_no_lines
    LocalCodes.SELECT_LINE -> R.string.notice_select_line
    LocalCodes.NO_MATCH -> R.string.notice_no_match
    LocalCodes.SYNCING -> R.string.notice_syncing
    LocalCodes.TUNNEL_ENDED -> R.string.notice_tunnel_ended
    LocalCodes.VPN_REVOKED -> R.string.notice_vpn_revoked
    else -> R.string.notice_operation_failed
}

fun Context.noticeString(notice: Notice): String = notice.text ?: getString(noticeRes(notice.code))
