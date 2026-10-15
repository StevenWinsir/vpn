package com.asterlink.vpn.ui.foundation

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.asterlink.vpn.core.CoreClient
import com.asterlink.vpn.core.CoreException
import com.asterlink.vpn.core.HelloResult
import com.asterlink.vpn.vpn.HostNetwork
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface FoundationState {
    data object Starting : FoundationState
    data class Ready(val hello: HelloResult, val network: HostNetwork) : FoundationState
    /** [code] is a stable Core code or "core_start_failed"; no secret ever reaches it. */
    data class Failed(val code: String) : FoundationState
}

/** Starts the Core once and shows its build identity: the round-1 proof that the AAR works. */
class FoundationViewModel(
    private val startCore: () -> Unit,
    private val client: CoreClient,
    private val network: () -> HostNetwork,
    private val io: CoroutineDispatcher = Dispatchers.IO,
) : ViewModel() {
    private val mutable = MutableStateFlow<FoundationState>(FoundationState.Starting)
    val state: StateFlow<FoundationState> = mutable.asStateFlow()

    init {
        viewModelScope.launch {
            mutable.value = try {
                withContext(io) { startCore() }
                FoundationState.Ready(client.hello(), withContext(io) { network() })
            } catch (error: CoreException) {
                FoundationState.Failed(error.code)
            } catch (error: Exception) {
                FoundationState.Failed("core_start_failed")
            }
        }
    }
}
