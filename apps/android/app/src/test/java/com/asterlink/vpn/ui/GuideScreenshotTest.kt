package com.asterlink.vpn.ui

import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import com.asterlink.vpn.core.CoreProtocol
import com.asterlink.vpn.core.CoreSnapshot
import com.asterlink.vpn.core.HelloResult
import com.asterlink.vpn.core.MeterSample
import com.asterlink.vpn.core.NodeView
import com.asterlink.vpn.security.ThemeMode
import com.asterlink.vpn.session.AppPhase
import com.asterlink.vpn.session.Notice
import com.asterlink.vpn.session.SessionState
import com.asterlink.vpn.ui.navigation.AsterRoot
import com.asterlink.vpn.ui.navigation.Clock
import com.asterlink.vpn.ui.navigation.Tab
import com.asterlink.vpn.ui.theme.AsterTheme
import com.github.takahirom.roborazzi.captureRoboImage
import java.io.File
import java.time.Instant
import java.util.TimeZone
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The website usage guide's Android pictures (handoff_guide.md 7.3, round 2). Test code only: the
 * same screens as ScreenshotTest, but with release build facts (no "开发版" footer), an example
 * address, made-up line names, light theme and normal font size. The files are prefixed "guide-"
 * so they never mix with the U01 set; frontend's guide:images picks them up from the suite's
 * output directory. Each case asserts the state it shows, like the U01 set.
 */
@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-rCN-w411dp-h891dp-xxhdpi")
class GuideScreenshotTest {
    @get:Rule
    val compose = createComposeRule()

    private val out = File(requireNotNull(System.getProperty("asterlink.screenshots")))
    private val facts = BuildFacts(appVersion = "1.0.0", channel = "release", apiHost = "api.example.com", envFromExample = false)
    private val identity = HelloResult("asterlink-core", "1.0.0", 1, 1, 1, "0000000000000000000000000000000000000000")
    private val clock = Clock(elapsedMillis = 751_000, now = Instant.parse("2029-12-02T08:00:00Z"))

    private val fixtures: Map<String, CoreSnapshot> by lazy {
        val contracts = File(requireNotNull(System.getProperty("asterlink.contracts")))
        Json.parseToJsonElement(contracts.resolve("snapshot-v1.json").readText()).jsonObject.getValue("snapshots").jsonObject
            .mapValues { (_, value) -> CoreProtocol.json.decodeFromJsonElement(CoreSnapshot.serializer(), value) }
    }

    /** Made-up lines, as on the other platforms' guide pictures. */
    private val lines = listOf(
        NodeView("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "香港 01", "HK", "dedicated", 1000),
        NodeView("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab", "香港 02", "HK", "direct", 500),
        NodeView("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "东京 01", "JP", "dedicated", 1500),
        NodeView("cccccccc-cccc-4ccc-8ccc-cccccccccccc", "新加坡 01", "SG", "direct", 1000),
    )

    private fun ready(): CoreSnapshot = fixtures.getValue("ready_with_selection").let { base ->
        base.copy(
            nodes = lines,
            selectedNodeId = lines[0].id,
            user = base.user!!.copy(email = "vip@example.com", name = "Vip"),
            entitlement = base.entitlement!!.copy(totalBytes = 100L shl 30, remainingBytes = 73_229_779_763L, subscriptionExpiresAt = "2029-12-31T15:59:59Z"),
        )
    }

    private fun connected(): CoreSnapshot {
        val metering = fixtures.getValue("connected_metering").metering!!
        val history = (0 until 60).map { i -> MeterSample(upload = 20_000L + (i % 7) * 9_000L, download = 600_000L + ((i * 37) % 23) * 180_000L) }
        return ready().copy(
            state = CoreSnapshot.State.CONNECTED, tunnelHealth = "up",
            metering = metering.copy(uploadBytesPerSecond = 1_258_291, downloadBytesPerSecond = 8_808_038, history = history,
                uploadBytes = 52_428_800, downloadBytes = 274_726_912, estimatedRemainingBytes = 73_100_000_000L),
        )
    }

    private fun signedIn(snapshot: CoreSnapshot, message: Notice? = null) = SessionState(
        phase = AppPhase.SignedIn, snapshot = snapshot, identity = identity, email = "vip@example.com", message = message,
        connectedSinceMillis = if (snapshot.state == CoreSnapshot.State.CONNECTED) 0L else null,
    )

    @Before
    fun fixedZone() {
        TimeZone.setDefault(TimeZone.getTimeZone("Asia/Shanghai"))
    }

    private fun shoot(name: String, state: SessionState, tab: Tab = Tab.Connect, device: DeviceStatus = DeviceStatus(true, true), check: () -> Unit = {}) {
        compose.mainClock.autoAdvance = false
        compose.setContent {
            AsterTheme(darkTheme = false) {
                AsterRoot(state, facts, ThemeMode.Light, AppActions(), clock, initialTab = tab, device = device)
            }
        }
        compose.mainClock.advanceTimeBy(600)
        check()
        val texts = visibleTexts()
        assertEquals("forbidden wording on screen $name", emptyList<String>(), CopyGuard.violations(texts))
        // The guide is for released apps: no development marker, no address but the example one.
        assertFalse("development marker on $name", texts.any { "开发版" in it })
        assertFalse("real address on $name: $texts", texts.any { Regex("""[A-Za-z0-9-]+\.(cn|com|net|vip)\b""").containsMatchIn(it.replace("example.com", "")) })
        out.mkdirs()
        compose.onRoot().captureRoboImage(File(out, "guide-$name.png").path)
    }

    private fun visibleTexts(): List<String> {
        val texts = mutableListOf<String>()
        fun walk(node: SemanticsNode) {
            node.config.getOrNull(SemanticsProperties.Text)?.forEach { texts += it.text }
            node.config.getOrNull(SemanticsProperties.EditableText)?.let { texts += it.text }
            node.config.getOrNull(SemanticsProperties.ContentDescription)?.let { texts += it }
            node.children.forEach(::walk)
        }
        walk(compose.onRoot(useUnmergedTree = true).fetchSemanticsNode())
        return texts.filter { it.isNotBlank() }
    }

    @Test
    fun login() = shoot("login", SessionState(phase = AppPhase.SignedOut, identity = identity, email = "vip@example.com", rememberPassword = true)) {
        compose.onNodeWithText("记住密码").assertExists()
        compose.onNodeWithText("版本 1.0.0", substring = true).assertExists()
    }

    @Test
    fun connectReady() = shoot("connect-ready", signedIn(ready())) {
        compose.onNodeWithText("未连接").assertExists()
        compose.onNodeWithText("香港 01").assertExists()
        compose.onNodeWithContentDescription("连接按钮", substring = true).assertIsEnabled()
    }

    @Test
    fun connectConnected() = shoot("connect-connected", signedIn(connected())) {
        compose.onNodeWithText("已连接 · 00:12:31").assertExists()
    }

    @Test
    fun lines() = shoot("lines", signedIn(ready()), Tab.Lines) {
        compose.onNodeWithText("东京 01").assertExists()
    }

    @Test
    fun account() = shoot("account", signedIn(ready()), Tab.Account) {
        compose.onNodeWithText("复制诊断信息").assertExists()
        compose.onNodeWithText("退出登录").assertExists()
    }

    @Test
    fun accountBackground() = shoot("account-background", signedIn(ready()), Tab.Account, device = DeviceStatus(notificationsAllowed = true, batteryUnrestricted = false)) {
        compose.onNodeWithText("设为“不限制”", substring = true).assertExists()
    }

    @Test
    fun noPlan() = shoot(
        "connect-no-plan",
        signedIn(fixtures.getValue("blocked_no_plan").let { it.copy(user = it.user!!.copy(email = "vip@example.com", name = "Vip")) }),
    ) { compose.onNodeWithText("当前账号没有有效套餐，请前往网站购买").assertExists() }

    @Test
    fun vpnDenied() = shoot("connect-vpn-denied", signedIn(ready(), message = Notice.local(com.asterlink.vpn.session.LocalCodes.VPN_PERMISSION_DENIED)))
}
