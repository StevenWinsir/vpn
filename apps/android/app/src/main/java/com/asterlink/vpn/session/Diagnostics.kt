package com.asterlink.vpn.session

import com.asterlink.vpn.core.CoreSnapshot
import java.time.Instant

/**
 * The text behind "copy diagnostics" (handoff_app_standardization.md D9). The user pastes it to
 * customer support, so it is user-visible copy: only the app version, the system version, the
 * connection state and the last error code, with the time of the report, in the same six lines as
 * on every other platform. Nothing here can hold a token, password, email, line, address or YAML.
 */
object Diagnostics {
    fun build(appVersion: String, osVersion: String, statusLabel: String, snapshot: CoreSnapshot, now: Instant = Instant.now()): String {
        val code = snapshot.lastError?.code.orEmpty()
        return listOf(
            "AsterLink 诊断信息",
            "时间: ${Formatting.dateTime(now)}",
            "应用版本: $appVersion",
            "系统版本: $osVersion",
            "连接状态: $statusLabel",
            "错误码: ${code.ifEmpty { "无" }}",
        ).joinToString("\n", postfix = "\n")
    }
}
