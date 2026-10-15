package com.asterlink.vpn

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.runtime.getValue
import androidx.core.net.toUri
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import com.asterlink.vpn.ui.foundation.BuildFacts
import com.asterlink.vpn.ui.foundation.FoundationScreen
import com.asterlink.vpn.ui.foundation.FoundationViewModel
import com.asterlink.vpn.ui.theme.AsterTheme

class MainActivity : ComponentActivity() {
    private val viewModel: FoundationViewModel by viewModels {
        viewModelFactory {
            initializer {
                val container = (application as AsterApp).container
                FoundationViewModel(container.core::start, container.coreClient, container::currentNetwork)
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val facts = BuildFacts(
            appVersion = BuildConfig.VERSION_NAME,
            channel = BuildConfig.APP_ENV,
            apiHost = BuildConfig.API_BASE_URL.toUri().host.orEmpty(),
            envFromExample = BuildConfig.ENV_FROM_EXAMPLE,
        )
        setContent {
            AsterTheme {
                val state by viewModel.state.collectAsStateWithLifecycle()
                FoundationScreen(state, facts)
            }
        }
    }
}
