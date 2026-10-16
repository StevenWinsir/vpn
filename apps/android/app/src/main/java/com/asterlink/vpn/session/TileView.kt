package com.asterlink.vpn.session

import com.asterlink.vpn.core.CoreSnapshot.State

/**
 * What the Quick Settings tile shows and what a tap may do, from the session state alone.
 * Connect is offered only when the connect page would offer it (signed in, plan valid, line
 * chosen, nothing in flight); otherwise the tap opens the app, which says what is missing.
 */
data class TileView(val active: Boolean, val action: Action) {
    enum class Action { Connect, Disconnect, Busy, OpenApp }

    companion object {
        fun from(state: SessionState): TileView {
            if (state.phase != AppPhase.SignedIn) return TileView(false, Action.OpenApp)
            val view = state.connection
            return when {
                view.showDisconnect -> TileView(true, Action.Disconnect)
                view.showProgress || state.busy || state.snapshot.state == State.STOPPING -> TileView(true, Action.Busy)
                view.showConnect && view.canConnect -> TileView(false, Action.Connect)
                else -> TileView(false, Action.OpenApp)
            }
        }
    }
}
