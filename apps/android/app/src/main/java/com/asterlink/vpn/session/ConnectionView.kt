package com.asterlink.vpn.session

import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.core.CoreSnapshot.State
import com.asterlink.vpn.core.NodeView
import java.util.Locale

/** The fixed status words of handoff_app_android.md 9.3 (the UI owns the text). */
enum class ConnectionStatus { Disconnected, Preparing, Connecting, Connected, Stopping, Unavailable }

enum class Tone { Idle, Busy, Good, Bad }

/**
 * What the connect page shows for one snapshot. Derived only from the Core's verdict plus the
 * command in flight; nothing here decides authorization.
 */
data class ConnectionView(
    val status: ConnectionStatus,
    val tone: Tone,
    val showConnect: Boolean,
    val showDisconnect: Boolean,
    val showProgress: Boolean,
    val canConnect: Boolean,
    val cancellable: Boolean,
    /** Why Connect is unavailable (null when it is available). */
    val blocker: Notice?,
    /** The last failure the Core recorded, shown under the status. */
    val failure: Notice?,
    /** The account has no usable plan: offer the website's plans page. */
    val needsPurchase: Boolean,
) {
    companion object {
        /** Entitlement reasons the website fixes (buy, renew, top up). */
        private val PURCHASE_REASONS = setOf("upgrade_required", "paid_vip_required", "subscription_expired", "quota_exhausted")

        fun from(snapshot: CoreSnapshot, busy: Boolean, activity: SessionActivity): ConnectionView {
            val entitlement = snapshot.entitlement
            val authorized = entitlement?.accountAuthorized == true
            val needsPurchase = !authorized && (entitlement == null || entitlement.reason.isEmpty() || entitlement.reason in PURCHASE_REASONS)
            val blocker = when {
                !authorized -> if (entitlement != null && entitlement.reasonMessage.isNotEmpty()) Notice.core(entitlement.reason, entitlement.reasonMessage)
                else Notice.local(LocalCodes.NO_PLAN)
                snapshot.nodes.isEmpty() -> Notice.local(if (snapshot.catalogRevision.isEmpty()) LocalCodes.SYNCING else LocalCodes.NO_LINES)
                snapshot.selectedNode == null -> Notice.local(LocalCodes.SELECT_LINE)
                else -> null
            }

            // While the connect command runs the Core answers nothing else (commands are serial),
            // so the snapshot still says "ready": the activity is the truth until the reply.
            if (activity == SessionActivity.Connecting && snapshot.state != State.CONNECTED) {
                val status = if (snapshot.state == State.CONNECTING) ConnectionStatus.Connecting else ConnectionStatus.Preparing
                return busyView(status, cancellable = true)
            }
            if (activity == SessionActivity.Disconnecting) return busyView(ConnectionStatus.Stopping, cancellable = false)

            when (snapshot.state) {
                State.CONNECTED -> return ConnectionView(ConnectionStatus.Connected, Tone.Good, false, true, false, false, false, null, null, false)
                State.CONNECTING -> return busyView(ConnectionStatus.Connecting, cancellable = false)
                State.PREPARING -> return busyView(ConnectionStatus.Preparing, cancellable = false)
                State.STOPPING -> return busyView(ConnectionStatus.Stopping, cancellable = false)
            }
            val (status, tone) = when (snapshot.state) {
                State.BLOCKED, State.ERROR -> ConnectionStatus.Unavailable to Tone.Bad
                else -> ConnectionStatus.Disconnected to Tone.Idle
            }
            val failure = snapshot.lastError?.takeIf { it.message.isNotEmpty() }?.let { Notice.core(it.code, it.message) }
            return ConnectionView(
                status = status,
                tone = if (blocker != null && tone == Tone.Idle && !authorized) Tone.Bad else tone,
                showConnect = true,
                showDisconnect = false,
                showProgress = false,
                canConnect = blocker == null && !busy,
                cancellable = false,
                blocker = blocker,
                failure = failure,
                needsPurchase = needsPurchase,
            )
        }

        private fun busyView(status: ConnectionStatus, cancellable: Boolean) =
            ConnectionView(status, Tone.Busy, false, false, true, false, cancellable, null, null, false)
    }
}

enum class LineFilter { All, Dedicated, Direct }

data class NodeRow(
    val id: String,
    val name: String,
    val region: String,
    val dedicated: Boolean,
    val ratePermille: Long,
    val nodeMetered: Boolean,
    val selected: Boolean,
    val configReady: Boolean,
) {
    companion object {
        fun from(node: NodeView, snapshot: CoreSnapshot) = NodeRow(
            id = node.id,
            name = node.name,
            region = node.region,
            dedicated = node.lineType == "dedicated",
            ratePermille = node.ratePermille,
            nodeMetered = node.meteringSource == "node_reported",
            selected = node.id == snapshot.selectedNodeId,
            configReady = node.id == snapshot.selectedNodeId && snapshot.configReady,
        )
    }
}

/** Lines of one region, in the order the server listed them. */
data class RegionGroup(val region: String, val rows: List<NodeRow>)

object NodeList {
    /**
     * Search over the metadata the user can already see (name, region, line label); the
     * server's order is kept and regions appear in the order of their first line.
     */
    fun build(snapshot: CoreSnapshot, query: String, filter: LineFilter, lineLabels: (Boolean) -> String): List<RegionGroup> {
        val needle = query.trim().lowercase(Locale.ROOT)
        val rows = snapshot.nodes
            .filter {
                when (filter) {
                    LineFilter.Dedicated -> it.lineType == "dedicated"
                    LineFilter.Direct -> it.lineType != "dedicated"
                    LineFilter.All -> true
                }
            }
            .filter {
                needle.isEmpty() ||
                    it.name.lowercase(Locale.ROOT).contains(needle) ||
                    it.region.lowercase(Locale.ROOT).contains(needle) ||
                    lineLabels(it.lineType == "dedicated").lowercase(Locale.ROOT).contains(needle)
            }
            .map { NodeRow.from(it, snapshot) }
        return rows.groupBy { it.region.trim() }.map { (region, list) -> RegionGroup(region, list) }
    }

    /** So "no plan", "no lines" and "nothing matches" are never confused. Null when rows exist. */
    fun emptyReason(snapshot: CoreSnapshot, filtered: Boolean): String? = when {
        snapshot.entitlement?.accountAuthorized != true -> LocalCodes.NO_PLAN
        // No catalog yet (just signed in): not the same as an administrator offering none.
        snapshot.nodes.isEmpty() && snapshot.catalogRevision.isEmpty() -> LocalCodes.SYNCING
        snapshot.nodes.isEmpty() -> LocalCodes.NO_LINES
        filtered -> LocalCodes.NO_MATCH
        else -> null
    }
}
