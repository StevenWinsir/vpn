package com.asterlink.vpn.session

import com.asterlink.vpn.core.CoreSnapshot

/**
 * Redacted diagnostics for support ("copy diagnostics" on the account page). Built from the
 * Core's already-redacted snapshot and public build facts: no line here can hold a token,
 * password, email, node name, address or YAML.
 */
object Diagnostics {
    fun build(
        appVersion: String,
        channel: String,
        apiHost: String,
        device: String,
        coreVersion: String,
        mihomoRevision: String,
        snapshot: CoreSnapshot,
    ): String {
        val lines = mutableListOf(
            "AsterLink Android $appVersion ($channel)",
            "device $device",
            "core ${coreVersion.ifEmpty { "-" }} mihomo ${mihomoRevision.take(12).ifEmpty { "-" }}",
            "api $apiHost",
            "state ${snapshot.state.ifEmpty { "-" }} generation ${snapshot.generation}",
            "nodes ${snapshot.nodes.size} selected ${snapshot.selectedNodeId.isNotEmpty()} config_ready ${snapshot.configReady}",
        )
        snapshot.entitlement?.let {
            lines += "account_authorized ${it.accountAuthorized} can_connect ${it.canConnect} reason ${it.reason.ifEmpty { "-" }}"
        }
        snapshot.metering?.let { lines += "metering ${it.source.ifEmpty { "-" }} pending ${it.pending}" }
        snapshot.lastError?.let { lines += "last_error ${it.code}" }
        return lines.joinToString("\n", postfix = "\n")
    }
}
