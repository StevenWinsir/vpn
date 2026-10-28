package com.asterlink.vpn.session

import com.asterlink.vpn.core.CoreClient
import com.asterlink.vpn.core.CoreSnapshot.State
import com.asterlink.vpn.core.ErrorView
import com.asterlink.vpn.core.LoginTarget
import com.asterlink.vpn.core.MeteringView
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
        val alerts = FakeAlerts()
        val controller = SessionController(
            client, lifecycle, target, credentials, preferences, devices, tunnel,
            scope = scope.backgroundScope,
            clock = { scope.testScheduler.currentTime },
            alerts = alerts,
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
        rig.controller.signInNow(email, "stale", remember = true, automatic = false)
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

    // ---- handoff_auth.md R4-03: the password was reset on the website ---------------------------

    @Test
    fun theOldSavedPasswordIsTriedOnceThenForgottenAndTheNewOneSaved() = runTest {
        val rig = rig() // the server only accepts `password` now
        rig.credentials.items[accountKey] = "old-password"
        rig.preferences.email = email
        rig.controller.launchNow()
        assertEquals(listOf("old-password"), rig.core.loginPasswords)
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals("invalid_credentials", rig.state.message?.code)
        assertTrue(rig.credentials.items.isEmpty())
        assertEquals(email, rig.preferences.email) // the non-secret email still prefills the form
        assertEquals(email, rig.state.email)
        advanceTimeBy(10_000)
        runCurrent()
        assertEquals(1, rig.core.count("login")) // no retry loop after a refused password

        rig.controller.signInNow(email, password, remember = true, automatic = false)
        assertEquals(AppPhase.SignedIn, rig.state.phase)
        assertEquals(mapOf(accountKey to password), rig.credentials.items)
        assertEquals(listOf("33333333-3333-4333-8333-333333333333", "33333333-3333-4333-8333-333333333333"), rig.core.deviceIds)
    }

    @Test
    fun aTypoInAManualLoginKeepsTheSavedPassword() = runTest {
        val rig = rig()
        rig.credentials.items[accountKey] = password
        rig.controller.launchNow()
        rig.controller.signInNow(email, "typo-password", remember = true, automatic = false)
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals(mapOf(accountKey to password), rig.credentials.items)
    }

    @Test
    fun aRefusalNeverDeletesAPasswordSavedMeanwhile() = runTest {
        val rig = rig()
        rig.credentials.items[accountKey] = "old-password"
        rig.controller.launchNow()
        // While the old password is on its way to the server, another login saves the new one.
        rig.core.onLogin = { if (it == "old-password") rig.credentials.items[accountKey] = password }
        rig.controller.signInNow(email, "old-password", remember = true, automatic = false)
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals(mapOf(accountKey to password), rig.credentials.items)
    }

    @Test
    fun aManualLoginDuringTheLaunchKeystoreReadWins() = runTest {
        val rig = rig()
        rig.credentials.items[accountKey] = "old-password"
        rig.preferences.email = email
        val gate = kotlinx.coroutines.CompletableDeferred<Unit>()
        rig.credentials.readGate = gate
        rig.controller.launch()
        runCurrent()
        assertEquals(1, rig.credentials.pendingReads) // the launch is waiting for the Keystore
        rig.credentials.readGate = null
        // The login form is already up: the user types the new password before the Keystore answers.
        rig.controller.signIn(email, password, remember = true)
        runCurrent()
        assertEquals(AppPhase.SignedIn, rig.state.phase)
        gate.complete(Unit)
        runCurrent()
        assertEquals(listOf(password), rig.core.loginPasswords) // the stale launch read never logs in
        assertEquals(AppPhase.SignedIn, rig.state.phase)
        assertEquals(mapOf(accountKey to password), rig.credentials.items)
    }

    @Test
    fun aSilentReLoginIsDroppedWhenTheUserSignsInToAnotherAccountFirst() = runTest {
        val rig = rig()
        rig.core.accounts["other@example.invalid"] = FakeCore.Account("Other-Fixture-Password", FakeCore.VIP)
        rig.credentials.items[accountKey] = password
        rig.preferences.email = email
        rig.controller.launchNow()
        val gate = kotlinx.coroutines.CompletableDeferred<Unit>()
        rig.credentials.readGate = gate
        rig.core.mutate { it.copy(state = State.SIGNED_OUT, lastError = ErrorView("native_session_expired", "登录已失效，请重新登录")) }
        advanceTimeBy(2_001)
        runCurrent()
        assertEquals(AppPhase.SignedOut, rig.state.phase)
        assertEquals(1, rig.credentials.pendingReads) // the silent re-login is reading the Keystore
        rig.credentials.readGate = null
        rig.controller.signIn("other@example.invalid", "Other-Fixture-Password", remember = false)
        runCurrent()
        gate.complete(Unit)
        runCurrent()
        assertEquals(listOf(email, "other@example.invalid"), rig.core.loginEmails)
        assertEquals(AppPhase.SignedIn, rig.state.phase)
        assertEquals("other@example.invalid", rig.state.email)
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
        rig.core.connectFailure = ErrorView("tunnel_unhealthy", "该线路暂时无法访问网络，已断开，请更换线路或稍后重试")
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

    // ---- round 4: per-connection usage and stop alerts -------------------------------------------

    @Test
    fun usageCountsOnlyThisConnectionAtItsOwnRate() = runTest {
        val rig = rig()
        rig.controller.launchNow()
        rig.controller.signInNow(email, password, remember = false, automatic = false)
        // An earlier connection of this sign-in already carried 5 000 + 7 000 bytes.
        rig.core.mutate { it.copy(metering = MeteringView(source = "client_reported", uploadBytes = 5_000, downloadBytes = 7_000)) }
        rig.controller.pollNow()
        rig.controller.selectNow("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb") // 500 permille
        rig.controller.connectNow()
        assertEquals(ConnectionUsage(0, 0, 500), rig.state.usage)
        rig.core.mutate { it.copy(metering = it.metering!!.copy(uploadBytes = 5_100, downloadBytes = 7_301)) }
        rig.controller.pollNow()
        val usage = rig.state.usage!!
        assertEquals(100L, usage.uploadBytes)
        assertEquals(301L, usage.downloadBytes)
        // 401 bytes at 0.5x is 200.5 bytes of quota, floored like the server's remaining_bytes.
        assertEquals(200L, usage.billedBytes)
        rig.controller.disconnectNow()
        assertNull(rig.state.usage)
        assertNull(rig.state.connectionBaseline)
    }

    @Test
    fun theCoreEndingTheTunnelAlertsWithItsReasonOnce() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.controller.connectNow()
        assertEquals(1, rig.alerts.clears) // a new connection clears an old alert
        rig.core.mutate { it.copy(state = State.STOPPING) }
        rig.controller.pollNow()
        assertTrue(rig.alerts.stopped.isEmpty()) // still settling: not yet
        rig.core.mutate { it.copy(state = State.READY, lastError = ErrorView("subscription_expired", "套餐已到期，请前往网站续费")) }
        rig.controller.pollNow()
        rig.controller.pollNow()
        assertEquals(listOf(Notice("subscription_expired", "套餐已到期，请前往网站续费")), rig.alerts.stopped)
    }

    @Test
    fun aDisconnectTheUserAskedForIsNotAnAlert() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.controller.connectNow()
        rig.controller.disconnectNow()
        rig.controller.pollNow()
        rig.controller.switchToNow("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
        assertTrue(rig.alerts.stopped.isEmpty())
    }

    @Test
    fun aSessionEndedByTheServerWhileConnectedAlerts() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.controller.connectNow()
        rig.core.mutate { com.asterlink.vpn.core.CoreSnapshot.Empty.copy(generation = it.generation, lastError = ErrorView("account_disabled", "账号已被停用")) }
        rig.controller.pollNow()
        assertEquals("account_disabled", rig.alerts.stopped.single().code)
        assertEquals(AppPhase.SignedOut, rig.state.phase)
    }

    @Test
    fun aFailedConnectIsShownInTheAppNotAlerted() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.core.connectFailure = ErrorView("tunnel_unhealthy", "该线路暂时无法访问网络，已断开，请更换线路或稍后重试")
        rig.controller.connectNow()
        rig.controller.pollNow()
        assertTrue(rig.alerts.stopped.isEmpty())
    }

    // ---- node groups round 6: online presence -----------------------------------------------------

    @Test
    fun aServerSideMoveIsNotAnEndedTunnel() = runTest {
        // connection_reassigned: the Core stops the segment and reconnects by itself to another
        // server of the line, staying in the busy states throughout. The app must neither alert
        // nor stop its VPN service (the service's Disconnect would cancel that reconnect).
        val rig = rig()
        rig.signedInWithLine()
        rig.controller.connectNow()
        val reassigned = ErrorView("connection_reassigned", "线路服务器已调整，正在为你重新连接")
        for (state in listOf(State.STOPPING, State.PREPARING, State.CONNECTING)) {
            rig.core.mutate { it.copy(state = state, lastError = reassigned) }
            rig.controller.pollNow()
            assertTrue(rig.state.connection.showProgress)
            assertFalse(rig.state.connection.showConnect)
        }
        rig.core.mutate { it.copy(state = State.CONNECTED, lastError = null) }
        rig.controller.pollNow()
        assertEquals(ConnectionStatus.Connected, rig.state.connection.status)
        assertTrue(rig.alerts.stopped.isEmpty())
        assertEquals(0, rig.tunnel.stops)
        assertTrue(rig.tunnel.running)
    }

    @Test
    fun aFailedAutomaticReconnectAlertsWithItsReasonOnce() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.controller.connectNow()
        rig.core.mutate { it.copy(state = State.PREPARING, lastError = ErrorView("connection_reassigned", "线路服务器已调整，正在为你重新连接")) }
        rig.controller.pollNow()
        val full = ErrorView("group_full", "该线路当前已满，请稍后再试或选择其他线路")
        rig.core.mutate { it.copy(state = State.READY, lastError = full) }
        rig.controller.pollNow()
        rig.controller.pollNow()
        assertEquals(listOf(Notice(full.code, full.message)), rig.alerts.stopped)
        assertEquals(1, rig.tunnel.stops)
        assertEquals(full.message, rig.state.connection.failure?.text)
        assertTrue(rig.state.connection.canConnect) // the user may try again or pick another line
    }

    @Test
    fun aConnectRefusedForTheDeviceLimitIsShownNotAlerted() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.core.connectFailure = ErrorView("device_limit", "已达到同时在线设备上限，请先断开其他设备")
        rig.controller.connectNow()
        rig.controller.pollNow()
        assertEquals(Notice("device_limit", "已达到同时在线设备上限，请先断开其他设备"), rig.state.message)
        assertEquals(AppPhase.SignedIn, rig.state.phase) // only this connect is refused, not the sign-in
        assertTrue(rig.alerts.stopped.isEmpty())
        assertFalse(rig.tunnel.running)
        assertTrue(rig.state.connection.canConnect)
        assertFalse(rig.state.connection.needsPurchase)
    }

    // ---- round 5: the authorization gate and a revoked VPN ---------------------------------------

    @Test
    fun aClosedGateKeepsTheTunnelAndSaysWhy() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.controller.connectNow()
        val since = rig.state.connectedSinceMillis
        rig.core.mutate { it.copy(tunnelHealth = "gated", gate = com.asterlink.vpn.core.GateView("lifecycle_reconfirmation_required", attempts = 1)) }
        rig.controller.pollNow()
        assertEquals(ConnectionStatus.Reconfirming, rig.state.connection.status)
        assertTrue(rig.state.connection.showDisconnect)
        rig.core.mutate { it.copy(gate = it.gate!!.copy(waitingNetwork = true, attempts = 3)) }
        rig.controller.pollNow()
        assertEquals(ConnectionStatus.WaitingNetwork, rig.state.connection.status)
        // Neither is an end of the tunnel: no alert, the VPN service stays, the timer continues.
        assertTrue(rig.alerts.stopped.isEmpty())
        assertTrue(rig.tunnel.running)
        assertEquals(0, rig.tunnel.stops)
        rig.core.mutate { it.copy(tunnelHealth = "up", gate = null, lastGate = com.asterlink.vpn.core.GateRecord("lifecycle_reconfirmation_required", "reopened", 1_200, 3, 1)) }
        rig.controller.pollNow()
        assertEquals(ConnectionStatus.Connected, rig.state.connection.status)
        assertEquals(since, rig.state.connectedSinceMillis)
    }

    @Test
    fun aRevokedVpnIsExplainedOnceAndNotAlertedAgain() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.controller.connectNow()
        rig.controller.vpnRevokedNow()
        assertEquals(listOf(Notice.local(LocalCodes.VPN_REVOKED)), rig.alerts.stopped)
        assertEquals(LocalCodes.VPN_REVOKED, rig.state.message?.code)
        // The service then disconnects through the Core; that end is already explained.
        rig.core.mutate { it.copy(state = State.READY) }
        rig.controller.pollNow()
        assertEquals(1, rig.alerts.stopped.size)
    }

    @Test
    fun aRevocationWithoutATunnelSaysNothing() = runTest {
        val rig = rig()
        rig.signedInWithLine()
        rig.controller.vpnRevokedNow()
        assertTrue(rig.alerts.stopped.isEmpty())
        assertNull(rig.state.message)
    }
}
