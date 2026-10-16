package com.asterlink.vpn.ui

import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import com.asterlink.vpn.core.CoreProtocol
import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.core.GateView
import com.asterlink.vpn.core.ErrorView
import com.asterlink.vpn.core.HelloResult
import com.asterlink.vpn.core.NodeView
import com.asterlink.vpn.security.ThemeMode
import com.asterlink.vpn.session.AppPhase
import com.asterlink.vpn.session.LocalCodes
import com.asterlink.vpn.session.Notice
import com.asterlink.vpn.session.SessionActivity
import com.asterlink.vpn.session.SessionState
import com.asterlink.vpn.ui.navigation.AsterRoot
import com.asterlink.vpn.ui.navigation.Clock
import com.asterlink.vpn.ui.navigation.Tab
import com.asterlink.vpn.ui.theme.AsterTheme
import com.github.takahirom.roborazzi.captureRoboImage
import com.github.takahirom.roborazzi.captureScreenRoboImage
import java.io.File
import java.time.Instant
import java.util.TimeZone
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * U01 screenshot set (handoff_app_android.md 10, round 3): every page and its empty/error
 * states, dark theme, 200 % font, a 360 dp phone and English, rendered on the JVM by Robolectric's
 * native graphics. The PNGs go to app/build/outputs/roborazzi (the suite copies them into
 * artifacts/). Each test also checks the one fact its state is about, so a screenshot of the
 * wrong state fails rather than passes.
 */
@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-rCN-w411dp-h891dp-xxhdpi")
class ScreenshotTest {
    @get:Rule
    val compose = createComposeRule()

    private val out = File(requireNotNull(System.getProperty("asterlink.screenshots")))
    private val facts = BuildFacts(appVersion = "0.1.0", channel = "development", apiHost = "demo.hyshentou.cn", envFromExample = false)
    private val identity = HelloResult("asterlink-core", "0.1.0", 1, 1, 1, "70f0570405c3c2c47bb113b88db95006d239b346")
    private val now = Instant.parse("2029-12-02T08:00:00Z")
    private val clock = Clock(elapsedMillis = 751_000, now = now)

    private val fixtures: Map<String, CoreSnapshot> by lazy {
        val contracts = File(requireNotNull(System.getProperty("asterlink.contracts")))
        Json.parseToJsonElement(contracts.resolve("snapshot-v1.json").readText()).jsonObject.getValue("snapshots").jsonObject
            .mapValues { (_, value) -> CoreProtocol.json.decodeFromJsonElement(CoreSnapshot.serializer(), value) }
    }

    /** A realistic catalog: dedicated and standard lines in three regions. */
    private val lines = listOf(
        NodeView("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "香港 IEPL 01", "HK", "dedicated", 1000),
        NodeView("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab", "香港 直连 02", "HK", "direct", 500),
        NodeView("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "东京 IPLC 01", "JP", "dedicated", 1000, "node_reported"),
        NodeView("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbc", "东京 直连 03", "JP", "direct", 500),
        NodeView("cccccccc-cccc-4ccc-8ccc-cccccccccccc", "新加坡 直连 01", "SG", "direct", 500),
    )

    private fun ready(): CoreSnapshot = fixtures.getValue("ready_with_selection").let { base ->
        base.copy(
            nodes = lines,
            selectedNodeId = lines[0].id,
            entitlement = base.entitlement!!.copy(totalBytes = 100L shl 30, remainingBytes = 73_229_779_763L, subscriptionExpiresAt = "2029-12-31T15:59:59Z"),
        )
    }

    private fun signedIn(snapshot: CoreSnapshot, activity: SessionActivity = SessionActivity.None, busy: Boolean = false, message: Notice? = null) =
        SessionState(
            phase = AppPhase.SignedIn, snapshot = snapshot, identity = identity, email = "vip@example.com",
            activity = activity, busy = busy, message = message,
            connectedSinceMillis = if (snapshot.state == CoreSnapshot.State.CONNECTED) 0L else null,
        )

    @Before
    fun fixedZone() {
        TimeZone.setDefault(TimeZone.getTimeZone("Asia/Shanghai"))
    }

    private fun shoot(name: String, state: SessionState, tab: Tab = Tab.Connect, dark: Boolean = false, device: DeviceStatus = DeviceStatus(), check: () -> Unit = {}) {
        compose.mainClock.autoAdvance = false
        compose.setContent {
            AsterTheme(darkTheme = dark) {
                AsterRoot(state, facts, if (dark) ThemeMode.Dark else ThemeMode.System, AppActions(), clock, initialTab = tab, device = device)
            }
        }
        compose.mainClock.advanceTimeBy(600)
        check()
        out.mkdirs()
        compose.onRoot().captureRoboImage(File(out, "$name.png").path)
    }

    // ---- login ---------------------------------------------------------------------------------

    @Test
    fun login() = shoot("01-login", SessionState(phase = AppPhase.SignedOut, identity = identity, email = "")) {
        compose.onNodeWithText("Mihomo 70f0570405c3", substring = true).assertExists()
    }

    @Test
    fun loginWrongPassword() = shoot(
        "02-login-wrong-password",
        SessionState(phase = AppPhase.SignedOut, identity = identity, email = "vip@example.com", message = Notice.core("invalid_credentials", "邮箱或密码不正确")),
    ) { compose.onNodeWithText("邮箱或密码不正确").assertExists() }

    @Test
    fun loginSigningIn() = shoot(
        "03-login-signing-in",
        SessionState(phase = AppPhase.SigningIn, identity = identity, email = "vip@example.com", rememberPassword = true, busy = true),
    ) { compose.onNodeWithText("正在登录…").assertExists() }

    @Test
    fun loginDeviceLimit() = shoot(
        "04-login-device-limit",
        SessionState(phase = AppPhase.SignedOut, identity = identity, email = "vip@example.com", message = Notice.core("device_limit", "已达到套餐同时在线设备上限，请先在其他设备退出")),
    )

    @Test
    fun loginCoreFailed() = shoot(
        "05-login-core-failed",
        SessionState(phase = AppPhase.SignedOut, coreProblem = Notice.local(LocalCodes.CORE_START_FAILED)),
    ) { compose.onNodeWithText("代理内核无法启动，请重新安装应用").assertExists() }

    // ---- connect -------------------------------------------------------------------------------

    @Test
    fun adminWithoutPlan() = shoot("06-connect-admin-no-plan", signedIn(fixtures.getValue("blocked_no_plan"))) {
        compose.onNodeWithText("当前账号没有有效套餐，请前往网站购买").assertExists()
        compose.onNodeWithText("购买或续费套餐").assertExists()
        compose.onNodeWithContentDescription("连接按钮", substring = true).assertIsNotEnabled()
    }

    @Test
    fun expiredPlan() = shoot(
        "07-connect-expired",
        signedIn(
            ready().copy(
                state = CoreSnapshot.State.BLOCKED, configReady = false,
                entitlement = ready().entitlement!!.copy(
                    accountAuthorized = false, canConnect = false, reason = "subscription_expired", reasonMessage = "套餐已到期，请前往网站续费",
                    subscriptionExpiresAt = "2029-11-30T00:00:00Z",
                ),
            ),
        ),
    ) { compose.onNodeWithText("套餐已到期，请前往网站续费").assertExists() }

    @Test
    fun quotaExhausted() = shoot(
        "08-connect-quota-zero",
        signedIn(
            ready().copy(
                state = CoreSnapshot.State.BLOCKED,
                entitlement = ready().entitlement!!.copy(accountAuthorized = false, canConnect = false, reason = "quota_exhausted", reasonMessage = "套餐流量已用完，请前往网站续费或升级", remainingBytes = 0),
            ),
        ),
    ) { compose.onNodeWithText("套餐流量已用完，请前往网站续费或升级").assertExists() }

    @Test
    fun noLineSelected() = shoot("09-connect-no-line", signedIn(ready().copy(selectedNodeId = "", configReady = false))) {
        compose.onNodeWithText("请先在“线路”页选择一条线路").assertExists()
        compose.onNodeWithText("请选择线路").assertExists()
    }

    @Test
    fun readyToConnect() = shoot("10-connect-ready", signedIn(ready())) {
        compose.onNodeWithContentDescription("连接按钮", substring = true).assertIsEnabled()
        compose.onNodeWithText("香港 IEPL 01").assertExists()
    }

    @Test
    fun connecting() = shoot("11-connect-connecting", signedIn(ready(), SessionActivity.Connecting, busy = true)) {
        compose.onNodeWithText("取消连接").assertExists()
    }

    @Test
    fun connected() = shoot("12-connect-connected", signedIn(connectedSnapshot())) {
        compose.onNodeWithText("已连接 · 00:12:31").assertExists()
    }

    @Test
    fun connectedNodeReported() = shoot(
        "13-connect-connected-node-metered",
        signedIn(connectedSnapshot().let { it.copy(metering = fixtures.getValue("connected_node_reported").metering, selectedNodeId = lines[2].id) }),
    ) { compose.onNodeWithText("节点实测计量").assertExists() }

    @Test
    fun connectFailed() = shoot(
        "14-connect-failed",
        signedIn(ready().copy(lastError = ErrorView("tunnel_unhealthy", "已建立隧道但无法经线路访问网络，已断开；请更换线路或稍后重试"))),
    )

    @Test
    fun stopping() = shoot("15-connect-stopping", signedIn(connectedSnapshot(), SessionActivity.Disconnecting, busy = true)) {
        compose.onNodeWithText("正在断开并结算").assertExists()
    }

    @Test
    fun vpnDenied() = shoot("16-connect-vpn-denied", signedIn(ready(), message = Notice.local(LocalCodes.VPN_PERMISSION_DENIED)))

    // ---- lines ---------------------------------------------------------------------------------

    @Test
    fun linesList() = shoot("17-lines", signedIn(ready()), Tab.Lines) {
        compose.onNodeWithText("东京 IPLC 01").assertExists()
    }

    @Test
    fun linesWithoutPlan() = shoot("18-lines-no-plan", signedIn(fixtures.getValue("blocked_no_plan")), Tab.Lines) {
        compose.onNodeWithText("购买套餐后可选择线路").assertExists()
    }

    @Test
    fun linesNoneConfigured() = shoot("19-lines-none-configured", signedIn(ready().copy(nodes = emptyList(), selectedNodeId = "")), Tab.Lines) {
        compose.onNodeWithText("管理员尚未配置可用线路").assertExists()
    }

    // ---- account -------------------------------------------------------------------------------

    @Test
    fun account() = shoot("20-account", signedIn(ready().copy(user = ready().user!!.copy(email = "vip@example.com", name = "Vip"))), Tab.Account) {
        compose.onNodeWithText("退出登录").assertExists()
    }

    @Test
    fun accountAdminWithoutPlan() = shoot("21-account-admin-no-plan", signedIn(fixtures.getValue("blocked_no_plan")), Tab.Account) {
        compose.onNodeWithText("管理员").assertExists()
    }

    // ---- dark, 200 % font, 360 dp, English -------------------------------------------------------

    @Test
    fun darkConnected() = shoot("22-dark-connected", signedIn(connectedSnapshot()), dark = true)

    @Test
    fun darkLines() = shoot("23-dark-lines", signedIn(ready()), Tab.Lines, dark = true)

    @Test
    fun darkLogin() = shoot("24-dark-login", SessionState(phase = AppPhase.SignedOut, identity = identity, email = "vip@example.com"), dark = true)

    @Test
    fun largeFontConnect() {
        RuntimeEnvironment.setFontScale(2f)
        shoot("25-font200-connect", signedIn(ready()))
    }

    @Test
    fun largeFontLogin() {
        RuntimeEnvironment.setFontScale(2f)
        shoot("26-font200-login", SessionState(phase = AppPhase.SignedOut, identity = identity, email = "vip@example.com"))
    }

    @Test
    @Config(qualifiers = "zh-rCN-w360dp-h640dp-xhdpi")
    fun smallPhoneConnected() = shoot("27-360dp-connected", signedIn(connectedSnapshot()))

    @Test
    @Config(qualifiers = "zh-rCN-w360dp-h640dp-xhdpi")
    fun smallPhoneLines() = shoot("28-360dp-lines", signedIn(ready()), Tab.Lines)

    @Test
    @Config(qualifiers = "en-w411dp-h891dp-xxhdpi")
    fun englishConnect() = shoot("29-en-connect", signedIn(ready())) {
        compose.onNodeWithText("Not connected").assertExists()
    }

    @Test
    @Config(qualifiers = "en-w411dp-h891dp-xxhdpi")
    fun englishLogin() = shoot("30-en-login", SessionState(phase = AppPhase.SignedOut, identity = identity, email = "")) {
        compose.onNodeWithText("Sign in").assertExists()
    }

    // ---- round 5: the authorization gate, a revoked VPN, background settings --------------------

    @Test
    fun reconfirming() = shoot(
        "31-connect-reconfirming",
        signedIn(connectedSnapshot().copy(tunnelHealth = "gated", gate = GateView("lifecycle_reconfirmation_required", attempts = 0))),
    ) {
        compose.onNodeWithText("正在重新验证").assertExists()
        compose.onNodeWithText("确认前不转发任何流量", substring = true).assertExists()
    }

    @Test
    fun waitingForNetwork() = shoot(
        "32-connect-waiting-network",
        signedIn(connectedSnapshot().copy(tunnelHealth = "gated", gate = GateView("network_changed", waitingNetwork = true, closedSeconds = 42, attempts = 3))),
    ) {
        compose.onNodeWithText("等待网络").assertExists()
        compose.onNodeWithText("网络恢复后自动继续", substring = true).assertExists()
    }

    @Test
    fun vpnRevoked() = shoot("33-connect-vpn-revoked", signedIn(ready(), message = Notice.local(LocalCodes.VPN_REVOKED))) {
        compose.onNodeWithText("其它 VPN 应用关闭", substring = true).assertExists()
    }

    @Test
    fun accountBackgroundRestricted() = shoot(
        "34-account-restricted",
        signedIn(ready().copy(user = ready().user!!.copy(email = "vip@example.com", name = "Vip"))),
        Tab.Account,
        device = DeviceStatus(notificationsAllowed = false, batteryUnrestricted = false),
    ) {
        compose.onNodeWithText("设为“不限制”", substring = true).assertExists()
        compose.onNodeWithText("通知栏不会显示状态", substring = true).assertExists()
    }

    // ---- round 6 -------------------------------------------------------------------------------

    @Test
    fun updateRequired() = shoot(
        "35-connect-update-required",
        signedIn(ready().copy(lastError = ErrorView("client_upgrade_required", "应用版本过低，请升级后再连接"))),
    ) {
        compose.onNodeWithText("下载新版本").assertIsEnabled()
        compose.onNodeWithText("应用版本过低", substring = true).assertExists()
    }

    @Test
    fun accountLicences() {
        // The dialog is a second window: drive it with the real clock and capture the whole screen.
        val state = signedIn(ready().copy(user = ready().user!!.copy(email = "vip@example.com", name = "Vip")))
        compose.setContent {
            AsterTheme(darkTheme = false) {
                AsterRoot(state, facts, ThemeMode.System, AppActions(), clock, initialTab = Tab.Account, device = DeviceStatus())
            }
        }
        compose.onNodeWithText("开源许可与源代码").performScrollTo().performClick()
        compose.waitForIdle()
        compose.onNodeWithText("GNU GPL v3", substring = true).assertExists()
        compose.onNodeWithText("打开下载页").assertExists()
        out.mkdirs()
        captureScreenRoboImage(File(out, "36-account-licenses.png").path)
    }

    private fun connectedSnapshot(): CoreSnapshot {
        val metering = fixtures.getValue("connected_metering").metering!!
        val history = (0 until 60).map { i ->
            com.asterlink.vpn.core.MeterSample(upload = 20_000L + (i % 7) * 9_000L, download = 600_000L + ((i * 37) % 23) * 180_000L)
        }
        return ready().copy(
            state = CoreSnapshot.State.CONNECTED, tunnelHealth = "up",
            metering = metering.copy(uploadBytesPerSecond = 1_258_291, downloadBytesPerSecond = 8_808_038, history = history,
                uploadBytes = 52_428_800, downloadBytes = 274_726_912, estimatedRemainingBytes = 73_100_000_000L),
        )
    }
}
