package com.asterlink.vpn.session

import com.asterlink.vpn.core.CoreClient
import com.asterlink.vpn.core.CoreSnapshot.State
import com.asterlink.vpn.core.ErrorView
import com.asterlink.vpn.core.LoginTarget
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The SessionController against a scripted Core (A01-A03 at unit level; the real Core and
 * backend run in scripts/test-native-android.py --suite account).
 */
class SessionControllerTest {
    private val target = LoginTarget("https://api.example.invalid/api/v1/client/", "development", "0.1.0")
    private val email = "vip@example.invalid"
    private val password = "Fixture-Only-Password!"
    private val accountKey = "https://api.example.invalid/api/v1/client|vip@example.invalid"

    private class Rig(scope: TestScope, target: LoginTarget) {
        val core = FakeCore()
        val lifecycle = FakeLifecycle(core)
        val credentials = FakeCredentials()
        val preferences = FakePreferences()
        val devices = DeviceIdentity { "33333333-3333-4333-8333-333333333333" }
        val client = CoreClient(core, StandardTestDispatcher(scope.testScheduler))
        val tunnel = FakeTunnel { client }
        val controller = SessionController(
            client, lifecycle, target, credentials, preferences, devices, tunnel,
            scope = scope.backgroundScope,
            clock = { scope.testScheduler.currentTime },
        )
        val state get() = controller.state.value
    }

    private fun TestScope.rig(entitlement: com.asterlink.vpn.core.EntitlementView = FakeCore.VIP, role: String = "user") =
        Rig(this, target).also { it.core.accounts[email] = FakeCore.Account(password, entitlement, role) }

    // ---- A01: sign in, remember password, sign out --------------------------------------------

    @Test
    fun launchWithoutARememberedEmailWaitsOnTheLoginScreen() = runTest {
        val rig = rig()
        rig.controller.launchNow()
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals("0.1.0", rig.state.identity?.coreVersion)
        assertEquals(0, rig.core.count("login"))
    }

    @Test
    fun aCoreThatCannotStartIsReportedNotHidden() = runTest {
        val rig = rig()
        rig.lifecycle.failStart = true
        rig.controller.launchNow()
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals(LocalCodes.CORE_START_FAILED, rig.state.coreProblem?.code)
    }

    @Test
    fun signInWithRememberSavesThePasswordUnderApiBaseAndLowerCasedEmail() = runTest {
        val rig = rig()
        rig.controller.launchNow()
        rig.controller.signInNow("  VIP@example.invalid ", password, remember = true, automatic = false)
        // The account map is keyed lower-case like the server; the Core gets the trimmed input.
        assertEquals(AppPhase.SignedIn, rig.state.phase)
        assertEquals(mapOf(accountKey to password), rig.credentials.items)
        assertEquals("VIP@example.invalid", rig.preferences.email)
        assertTrue(rig.state.rememberPassword)
        assertEquals(listOf("33333333-3333-4333-8333-333333333333"), rig.core.deviceIds)
    }

    @Test
    fun signInWithoutRememberDeletesAPreviouslySavedPassword() = runTest {
        val rig = rig()
        rig.credentials.items[accountKey] = "old"
        rig.preferences.email = email
        rig.controller.launchNow() // the saved "old" password is refused: deleted, back to login
        rig.controller.signInNow(email, password, remember = false, automatic = false)
        assertEquals(AppPhase.SignedIn, rig.state.phase)
        assertTrue(rig.credentials.items.isEmpty())
        assertNull(rig.preferences.email)
        assertFalse(rig.state.rememberPassword)
    }

    @Test
    fun aWrongPasswordShowsTheCoreMessageAndForgetsTheSavedOne() = runTest {
        val rig = rig()
        rig.credentials.items[accountKey] = "stale"
        rig.controller.launchNow()
        rig.controller.signInNow(email, "wrong", remember = true, automatic = false)
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals("invalid_credentials", rig.state.message?.code)
        assertEquals("邮箱或密码不正确", rig.state.message?.text)
        assertTrue(rig.credentials.items.isEmpty())
    }

    @Test
    fun aRememberedPasswordSignsInOnlineAtLaunch() = runTest {
        val rig = rig()
        rig.credentials.items[accountKey] = password
        rig.preferences.email = email
        rig.controller.launchNow()
        assertEquals(AppPhase.SignedIn, rig.state.phase)
        assertEquals(1, rig.core.count("login"))
        assertTrue(rig.state.rememberPassword)
        assertEquals(email, rig.state.email)
    }

    @Test
    fun aKeystoreFailureNeverFallsBackToPlaintext() = runTest {
        val rig = rig()
        rig.credentials.failSave = true
        rig.controller.launchNow()
        rig.controller.signInNow(email, password, remember = true, automatic = false)
        assertEquals(AppPhase.SignedIn, rig.state.phase)
        assertEquals(LocalCodes.CREDENTIAL_SAVE_FAILED, rig.state.warning?.code)
        assertFalse(rig.state.rememberPassword)
        assertTrue(rig.credentials.items.isEmpty())
        assertNull(rig.preferences.email)
    }

    @Test
    fun anUnreadableKeystoreAtLaunchAsksForThePassword() = runTest {
        val rig = rig()
        rig.preferences.email = email
        rig.credentials.failRead = true
        rig.controller.launchNow()
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals(LocalCodes.CREDENTIAL_READ_FAILED, rig.state.warning?.code)
        assertEquals(0, rig.core.count("login"))
    }

    @Test
    fun inputOutsideTheCoreLimitsNeverReachesTheCore() = runTest {
        val rig = rig()
        rig.controller.launchNow()
        rig.controller.signInNow(" ", password, remember = false, automatic = false)
        assertEquals(LocalCodes.INPUT_MISSING, rig.state.message?.code)
        rig.controller.signInNow(email, "密".repeat(25), remember = false, automatic = false) // 75 UTF-8 bytes
        assertEquals(LocalCodes.PASSWORD_TOO_LONG, rig.state.message?.code)
        rig.controller.signInNow("a".repeat(250) + "@x.io", password, remember = false, automatic = false)
        assertEquals(LocalCodes.EMAIL_TOO_LONG, rig.state.message?.code)
        assertEquals(0, rig.core.count("login"))
    }

    @Test
    fun signOutStopsTheVpnLogsOutAndForgetsThePassword() = runTest {
        val rig = rig()
        rig.controller.launchNow()
        rig.controller.signInNow(email, password, remember = true, automatic = false)
        rig.tunnel.running = true
        rig.controller.signOutNow()
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals(1, rig.core.count("logout"))
        assertEquals(1, rig.tunnel.stops)
        assertTrue(rig.credentials.items.isEmpty())
        assertNull(rig.preferences.email)
    }

    @Test
    fun deviceLimitIsALoginFailureWithItsOwnReason() = runTest {
        val rig = rig()
        rig.core.deviceLimit = true
        rig.controller.launchNow()
        rig.controller.signInNow(email, password, remember = true, automatic = false)
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals("device_limit", rig.state.message?.code)
        // Not a password problem: nothing is deleted, nothing saved.
        assertTrue(rig.credentials.items.isEmpty())
    }

    // ---- A02: no plan, expired, no quota: the Core says no, the UI cannot connect -----------------

    @Test
    fun anAdministratorWithoutAPlanCannotConnect() = runTest {
        val rig = rig(FakeCore.NO_PLAN, role = "admin")
        rig.controller.launchNow()
        rig.controller.signInNow(email, password, remember = false, automatic = false)
        val view = rig.state.connection
        assertEquals(State.BLOCKED, rig.state.snapshot.state)
        assertFalse(view.canConnect)
        assertTrue(view.needsPurchase)
        assertEquals("当前账号没有有效套餐，请前往网站购买", view.blocker?.text)
        rig.controller.connectNow()
        assertEquals(0, rig.core.count("connect"))
    }

    @Test
    fun expiredAndExhaustedPlansOfferRenewal() = runTest {
        for (entitlement in listOf(FakeCore.EXPIRED, FakeCore.NO_QUOTA)) {
            val rig = rig(entitlement)
            rig.controller.launchNow()
            rig.controller.signInNow(email, password, remember = false, automatic = false)
            assertFalse(rig.state.connection.canConnect)
            assertTrue(rig.state.connection.needsPurchase)
            assertEquals(entitlement.reason, rig.state.connection.blocker?.code)
        }
    }

    // ---- A03 / A04: watcher results, generations, races -------------------------------------------

    @Test
    fun pollingAppliesCatalogChangesFromTheCore() = runTest {
        val rig = rig()
        rig.controller.launchNow()
        rig.controller.signInNow(email, password, remember = false, automatic = false)
        rig.core.mutate { it.copy(nodes = it.nodes.map { n -> if (n.region == "JP") n.copy(name = "JP Renamed", ratePermille = 750) else n }) }
        advanceTimeBy(2_001)
        runCurrent()
        val jp = rig.state.snapshot.nodes.single { it.region == "JP" }
        assertEquals("JP Renamed", jp.name)
        assertEquals(750, jp.ratePermille)
    }

    @Test
    fun aSnapshotOfAnOlderGenerationIsLateNews() = runTest {
        val rig = rig()
        rig.controller.launchNow()
        rig.controller.signInNow(email, password, remember = false, automatic = false)
        val generation = rig.state.snapshot.generation
        rig.core.snapshot = rig.core.snapshot.copy(generation = generation - 1, nodes = emptyList())
        rig.controller.pollNow()
        assertEquals(generation, rig.state.snapshot.generation)
        assertEquals(2, rig.state.snapshot.nodes.size)
    }

    @Test
    fun aStaleGenerationReReadsTheStateAndSaysSo() = runTest {
        val rig = rig()
        rig.controller.launchNow()
        rig.controller.signInNow(email, password, remember = false, automatic = false)
        rig.core.mutate { it } // the watcher moved the session on
        rig.controller.selectNow("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
        assertEquals("stale_generation", rig.state.message?.code)
        assertEquals(rig.core.snapshot.generation, rig.state.snapshot.generation)
        assertEquals("", rig.state.snapshot.selectedNodeId)
        // Deciding again at the new generation works.
        rig.controller.selectNow("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
        assertNull(rig.state.message)
        assertTrue(rig.state.snapshot.configReady)
    }

    @Test
    fun anExpiredSessionGetsExactlyOneSilentReLogin() = runTest {
        val rig = rig()
        rig.credentials.items[accountKey] = password
        rig.preferences.email = email
        rig.controller.launchNow()
        assertEquals(1, rig.core.count("login"))
        repeat(2) {
            rig.core.mutate { it.copy(state = State.SIGNED_OUT, lastError = ErrorView("native_session_expired", "登录已失效，请重新登录")) }
            advanceTimeBy(2_001)
            runCurrent()
        }
        // First expiry: one automatic re-login (signed in again); second: the user is asked.
        assertEquals(2, rig.core.count("login"))
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals("native_session_expired", rig.state.message?.code)
    }

    @Test
    fun aSessionEndedForAnotherReasonIsNeverRetriedSilently() = runTest {
        val rig = rig()
        rig.credentials.items[accountKey] = password
        rig.preferences.email = email
        rig.controller.launchNow()
        rig.core.mutate { it.copy(state = State.SIGNED_OUT, lastError = ErrorView("subscription_changed", "套餐已变更，请重新登录")) }
        advanceTimeBy(2_001)
        runCurrent()
        assertEquals(1, rig.core.count("login"))
        assertEquals("subscription_changed", rig.state.message?.code)
    }

    @Test
    fun theForegroundRefreshIsThrottledToTenSeconds() = runTest {
        val rig = rig()
        rig.controller.launchNow()
        rig.controller.signInNow(email, password, remember = false, automatic = false)
        assertEquals(1, rig.core.count("refresh_catalog")) // read at once after the sign-in
        rig.controller.onForegroundNow()
        assertEquals(1, rig.core.count("refresh_catalog"))
        advanceTimeBy(10_001)
        rig.controller.onForegroundNow()
        rig.controller.onForegroundNow()
        assertEquals(2, rig.core.count("refresh_catalog"))
    }

    @Test
    fun aLostCoreEndsTheSession() = runTest {
        val rig = rig()
        rig.controller.launchNow()
        rig.controller.signInNow(email, password, remember = false, automatic = false)
        rig.core.started = false
        rig.controller.pollNow()
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals(LocalCodes.CORE_LOST, rig.state.message?.code)
    }

    // ---- connect hand-off (the full path is round 4) ----------------------------------------------

    private suspend fun Rig.signedInWithLine() {
        controller.launchNow()
        controller.signInNow(email, password, remember = false, automatic = false)
        controller.selectNow("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
    }

    @Test
    fun connectGoesThroughTheVpnServiceAtTheDecidedGeneration() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        assertTrue(rig.state.connection.canConnect)
        rig.controller.connectNow()
        assertEquals(State.CONNECTED, rig.state.snapshot.state)
        assertEquals(ConnectionStatus.Connected, rig.state.connection.status)
        assertEquals(0L, rig.state.connectedSinceMillis)
        assertFalse(rig.state.busy)
    }

    @Test
    fun aRefusedConnectShowsTheCoreReasonAndStaysReady() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.core.connectFailure = ErrorView("tunnel_unhealthy", "已建立隧道但无法经线路访问网络，已断开；请更换线路或稍后重试")
        rig.controller.connectNow()
        assertEquals(State.READY, rig.state.snapshot.state)
        assertEquals("tunnel_unhealthy", rig.state.message?.code)
        assertFalse(rig.tunnel.running)
    }

    @Test
    fun aDeniedVpnDialogIsExplained() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.controller.vpnConsentDenied()
        assertEquals(LocalCodes.VPN_PERMISSION_DENIED, rig.state.message?.code)
        assertEquals(0, rig.core.count("connect"))
    }

    @Test
    fun cancellingAConnectEndsTheSession() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        // The user presses cancel while the service is inside the Core's connect.
        rig.tunnel.beforeReply = { rig.controller.cancelConnect() }
        rig.controller.connectNow()
        runCurrent()
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals(LocalCodes.CANCELLED, rig.state.message?.code)
        assertEquals(1, rig.lifecycle.stops)
    }

    @Test
    fun whenTheCoreEndsTheTunnelTheVpnServiceIsStopped() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.controller.connectNow()
        assertTrue(rig.tunnel.running)
        rig.core.mutate { it.copy(state = State.READY, lastError = ErrorView("quota_exhausted", "套餐流量已用完，请前往网站续费或升级")) }
        // The poll scheduled before the connect still waits its idle interval.
        advanceTimeBy(2_001)
        runCurrent()
        assertEquals(1, rig.tunnel.stops)
        assertEquals("quota_exhausted", rig.state.connection.failure?.code)
    }

    @Test
    fun switchingLinesWhileConnectedDisconnectsThenSelects() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.controller.connectNow()
        rig.controller.switchToNow("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
        assertEquals(1, rig.core.count("disconnect"))
        assertEquals(State.READY, rig.state.snapshot.state)
        assertEquals("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", rig.state.snapshot.selectedNodeId)
        assertEquals(1, rig.core.count("connect")) // stays disconnected until the user connects
    }

    @Test
    fun theVpnServiceRefusingIsShownWithALocalReason() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.tunnel.refuseWith = LocalCodes.VPN_STOPPING
        rig.controller.connectNow()
        assertEquals(LocalCodes.VPN_STOPPING, rig.state.message?.code)
        assertNull(rig.state.message?.text)
    }
}
