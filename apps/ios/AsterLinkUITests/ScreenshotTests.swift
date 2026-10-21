import XCTest

/// The screenshot set of handoff_app_ios.md round 3 (U01). Each case launches the app on a
/// prepared screen (ASTER_UI_FIXTURE, Debug builds only; no Core, no network), asserts what the
/// screen says, then captures it. A shot without its assertion would only prove that something
/// rendered, so every case checks the state it claims to show. Screenshots are attached to the
/// result bundle and, when ASTER_SCREENSHOT_DIR is set (TEST_RUNNER_ASTER_SCREENSHOT_DIR), written there.
final class ScreenshotTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = false
    }

    // MARK: harness

    @discardableResult
    private func launch(_ fixture: String, dark: Bool = false, accessibilityText: Bool = false) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["ASTER_UI_FIXTURE"] = fixture
        app.launchEnvironment["ASTER_APPEARANCE"] = dark ? "dark" : "light"
        var arguments = ["-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]
        if accessibilityText { arguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXL"] }
        app.launchArguments = arguments
        app.launch()
        self.app = app
        return app
    }

    private func el(_ id: String) -> XCUIElement { app.descendants(matching: .any).matching(identifier: id).firstMatch }

    private func present(_ id: String, _ message: String = "", file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertTrue(el(id).waitForExistence(timeout: 8), "missing \(id) \(message)", file: file, line: line)
    }

    private func absent(_ id: String, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertFalse(el(id).exists, "unexpected \(id)", file: file, line: line)
    }

    private func said(_ id: String, _ expected: String, contains: Bool = false, file: StaticString = #filePath, line: UInt = #line) {
        present(id, file: file, line: line)
        let element = el(id)
        let text = element.label.isEmpty ? (element.value as? String ?? "") : element.label
        if contains { XCTAssertTrue(text.contains(expected), "\(id) says '\(text)', expected to contain '\(expected)'", file: file, line: line) }
        else { XCTAssertEqual(text, expected, "\(id)", file: file, line: line) }
    }

    /// The page background (left edge, mid-height) is dark: proves the dark shot really is dark.
    private func assertDark(_ expected: Bool, file: StaticString = #filePath, line: UInt = #line) {
        guard let image = app.screenshot().image.cgImage, let data = image.dataProvider?.data, let bytes = CFDataGetBytePtr(data) else {
            return XCTFail("no screenshot pixels", file: file, line: line)
        }
        let bytesPerPixel = image.bitsPerPixel / 8
        let offset = (image.height / 2) * image.bytesPerRow + 4 * bytesPerPixel
        let luminance = (Int(bytes[offset]) + Int(bytes[offset + 1]) + Int(bytes[offset + 2])) / 3
        XCTAssertEqual(luminance < 90, expected, "background luminance \(luminance)", file: file, line: line)
    }

    private func capture(_ name: String) {
        let screenshot = app.screenshot()
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let directory = ProcessInfo.processInfo.environment["ASTER_SCREENSHOT_DIR"], !directory.isEmpty {
            try? FileManager.default.createDirectory(atPath: directory, withIntermediateDirectories: true)
            try? screenshot.pngRepresentation.write(to: URL(fileURLWithPath: directory).appendingPathComponent(name + ".png"))
        }
    }

    // MARK: login

    func test01_login() {
        launch("login")
        present("login-email"); present("login-password"); present("login-submit"); present("login-register")
        said("login-footer", "Mihomo 70f0570", contains: true)
        XCTAssertEqual(el("login-remember").value as? String, "已关闭")
        absent("login-banner"); absent("login-password-error")
        assertDark(false)
        capture("01-login")
    }

    func test02_loginRemembered() {
        launch("login-remembered")
        XCTAssertEqual(el("login-email").value as? String, "vip@example.com")
        XCTAssertEqual(el("login-remember").value as? String, "已开启")
        capture("02-login-remembered")
    }

    func test03_loginWrongPassword() {
        launch("login-wrong-password")
        said("login-password-error", "邮箱或密码错误")
        capture("03-login-wrong-password")
    }

    func test04_loginOffline() {
        launch("login-offline")
        said("login-banner", "无法连接服务器", contains: true)
        capture("04-login-offline")
    }

    func test05_loginSaveFailed() {
        launch("login-save-failed")
        said("login-warning", "无法安全保存密码", contains: true)
        capture("05-login-save-failed")
    }

    // MARK: connect

    func test06_connectReady() {
        launch("connect-ready")
        said("status-text", "未连接")
        said("line-name", "香港 01"); said("line-summary", "专线 · 计费 1×")
        said("plan-remaining", "剩余 68.00 GiB / 100.00 GiB")
        XCTAssertTrue(el("connect-button").isEnabled)
        XCTAssertEqual(el("connect-button").label, "连接")
        absent("blocker-text"); absent("traffic-card")
        capture("06-connect-ready")
    }

    func test07_connectNoPlan() {
        launch("connect-no-plan")
        said("status-text", "无法连接")
        said("blocker-text", "没有有效套餐", contains: true)
        XCTAssertFalse(el("connect-button").isEnabled)
        capture("07-connect-no-plan")
    }

    func test08_connectExpired() {
        launch("connect-expired")
        said("blocker-text", "2026-09-30 到期", contains: true)
        XCTAssertFalse(el("connect-button").isEnabled)
        capture("08-connect-expired")
    }

    func test09_connectQuotaZero() {
        launch("connect-quota-zero")
        said("blocker-text", "流量已用完")
        said("plan-remaining", "剩余 0 B / 100.00 GiB")
        XCTAssertFalse(el("connect-button").isEnabled)
        capture("09-connect-quota-zero")
    }

    func test10_connectDeviceLimit() {
        launch("connect-device-limit")
        said("message-banner", "已达到同时在线设备上限，请先断开其他设备")
        XCTAssertTrue(el("connect-button").isEnabled) // once another device disconnects, trying again works
        capture("10-connect-device-limit")
    }

    func test10b_connectReassigning() {
        launch("connect-reassigning")
        said("status-text", "正在准备")
        XCTAssertFalse(el("connect-button").isEnabled)
        capture("10b-connect-reassigning")
    }

    func test11_connectNoLineSelected() {
        launch("connect-no-line")
        said("blocker-text", "请先在“线路”页选择一条线路")
        said("line-name", "未选择线路")
        XCTAssertFalse(el("connect-button").isEnabled)
        capture("11-connect-no-line")
    }

    func test12_connectSyncing() {
        launch("connect-syncing")
        said("blocker-text", "正在同步线路…")
        capture("12-connect-syncing")
    }

    func test13_connectPreparing() {
        launch("connect-preparing")
        said("status-text", "正在准备")
        present("cancel-connect"); XCTAssertFalse(el("connect-button").isEnabled)
        capture("13-connect-preparing")
    }

    func test14_connectConnecting() {
        launch("connect-connecting")
        said("status-text", "正在连接")
        present("cancel-connect")
        capture("14-connect-connecting")
    }

    func test15_connectConnected() {
        launch("connect-connected")
        present("status-text")
        XCTAssertTrue(el("status-text").label.hasPrefix("已连接 · 00:"), el("status-text").label)
        said("rate-up", "1.20 MiB/s", contains: true); said("rate-down", "8.40 MiB/s", contains: true)
        said("usage-text", "本次用量 101.00 MiB · 计费 101.00 MiB")
        XCTAssertEqual(el("connect-button").label, "断开连接")
        present("rate-chart")
        said("plan-estimated", "预估剩余", contains: true)
        capture("15-connect-connected")
    }

    func test16_connectConnectedHalfRate() {
        launch("connect-connected-half")
        said("line-summary", "普通 · 计费 0.5×")
        said("usage-text", "本次用量 101.00 MiB · 计费 50.50 MiB")
        capture("16-connect-connected-half-rate")
    }

    func test17_connectConnectedNodeMetered() {
        launch("connect-connected-node")
        said("line-name", "洛杉矶 01"); said("line-summary", "计费 0.5×", contains: true)
        capture("17-connect-connected-node-metered")
    }

    func test18_connectGated() {
        launch("connect-gated")
        said("status-text", "正在重新验证")
        said("status-detail", "确认前不转发任何流量", contains: true)
        XCTAssertEqual(el("connect-button").label, "断开连接")
        capture("18-connect-gated")
    }

    func test19_connectWaitingNetwork() {
        launch("connect-waiting-network")
        said("status-text", "等待网络")
        said("status-detail", "隧道保持但不转发流量", contains: true)
        capture("19-connect-waiting-network")
    }

    func test20_connectStopping() {
        launch("connect-stopping")
        said("status-text", "正在断开并结算")
        XCTAssertFalse(el("connect-button").isEnabled)
        capture("20-connect-stopping")
    }

    func test21_connectError() {
        launch("connect-error")
        said("status-text", "无法连接")
        said("failure-banner", "隧道验证未通过", contains: true)
        capture("21-connect-error")
    }

    func test22_connectUpdateRequired() {
        launch("connect-update-required")
        said("failure-banner", "请更新到最新版本", contains: true)
        capture("22-connect-update-required")
    }

    func test23_connectTunnelUnavailable() {
        launch("connect-tun-unavailable")
        said("message-banner", "暂时无法建立系统隧道", contains: true)
        capture("23-connect-tun-unavailable")
    }

    func test24_connectEndedByTheCore() {
        launch("connect-ended")
        said("message-banner", "流量已用完，连接已断开")
        said("status-text", "未连接")
        capture("24-connect-ended")
    }

    // MARK: lines

    func test25_lines() {
        launch("lines")
        present("lines-filter"); present("line-row-香港 01"); present("line-row-东京 01"); present("line-row-洛杉矶 01")
        XCTAssertEqual(el("line-row-香港 01").value as? String, "已选择")
        XCTAssertTrue(el("line-row-香港 01").label.contains("专线") && el("line-row-香港 02").label.contains("计费 0.5×"))
        XCTAssertTrue(el("line-row-洛杉矶 01").label.contains("节点计量"))
        capture("25-lines")
    }

    func test26_linesSearchNoResult() {
        launch("lines-search-empty")
        present("lines-empty-no_match")
        absent("line-row-香港 01")
        capture("26-lines-no-match")
    }

    func test27_linesSearchFilters() {
        launch("lines-search-hk")
        present("line-row-香港 01"); present("line-row-香港 02")
        absent("line-row-东京 01")
        capture("27-lines-search")
    }

    func test28_linesNoPlan() {
        launch("lines-no-plan")
        present("lines-empty-no_plan")
        capture("28-lines-no-plan")
    }

    func test29_linesNone() {
        launch("lines-none")
        said("lines-empty-no_lines", "管理员尚未配置可用线路", contains: true)
        capture("29-lines-none")
    }

    func test30_linesSyncing() {
        launch("lines-syncing")
        XCTAssertTrue(app.staticTexts["正在同步线路…"].waitForExistence(timeout: 8), "the empty state says the lines are syncing")
        capture("30-lines-syncing")
    }

    func test31_linesWhileConnected() {
        launch("lines-connected")
        present("line-row-香港 02")
        app.buttons["line-row-香港 02"].tap()
        XCTAssertTrue(app.buttons["断开并切换到 香港 02"].waitForExistence(timeout: 5), "the switch must ask for confirmation while connected")
        capture("31-lines-switch-confirm")
    }

    // MARK: account

    func test32_account() {
        launch("account")
        said("account-email", "vip@example.com"); said("account-plan", "VIP")
        said("account-expiry", "2030-12-31", contains: true); said("account-remaining", "剩余 68.00 GiB / 100.00 GiB")
        present("appearance-picker")
        for _ in 0..<5 where !el("sign-out").exists { app.swipeUp() }
        present("sign-out"); present("copy-diagnostics")
        capture("32-account")
    }

    func test33_accountAdminWithoutPlan() {
        launch("account-admin-no-plan")
        said("account-email", "test@test.com"); said("account-role", "管理员账号")
        said("account-plan", "暂无有效套餐")
        capture("33-account-admin-no-plan")
    }

    func test34_accountExpired() {
        launch("account-expired")
        said("account-expiry", "已到期", contains: true)
        said("account-reason", "已于 2026-09-30 到期", contains: true)
        capture("34-account-expired")
    }

    func test35_accountAbout() {
        launch("account")
        let about = el("about-mihomo")
        for _ in 0..<4 where !about.exists { app.swipeUp() }
        present("about-mihomo")
        present("open-licenses")
        capture("35-account-about")
        el("open-licenses").tap()
        present("licenses-sheet")
        capture("36-licenses")
    }

    func test36_signOutAsksFirst() {
        launch("account")
        for _ in 0..<4 where !el("sign-out").isHittable { app.swipeUp() }
        el("sign-out").tap()
        XCTAssertTrue(app.buttons["退出并忘记本机密码"].waitForExistence(timeout: 5))
        capture("37-sign-out-confirm")
    }

    // MARK: navigation

    func test37_tabsNavigate() {
        launch("connect-ready")
        app.tabBars.buttons["线路"].tap()
        present("lines-list")
        app.tabBars.buttons["我的"].tap()
        present("account-email")
        app.tabBars.buttons["连接"].tap()
        present("status-text")
        el("line-card").tap()
        present("lines-list")
    }

    // MARK: dark mode, large text

    func test40_darkConnected() {
        launch("connect-connected", dark: true)
        present("rate-chart")
        assertDark(true)
        capture("40-dark-connected")
    }

    func test41_darkLogin() {
        launch("login-wrong-password", dark: true)
        present("login-password-error")
        assertDark(true)
        capture("41-dark-login")
    }

    func test42_darkLines() {
        launch("lines", dark: true)
        present("line-row-香港 01")
        assertDark(true)
        capture("42-dark-lines")
    }

    func test43_darkAccount() {
        launch("account", dark: true)
        present("account-email")
        assertDark(true)
        capture("43-dark-account")
    }

    func test44_largeTextLogin() {
        launch("login")
        app.terminate()
        launch("login-wrong-password", accessibilityText: true)
        present("login-submit")
        // At AX3 the page scrolls instead of truncating: the button is reachable.
        for _ in 0..<6 where !el("login-submit").isHittable { app.swipeUp() }
        XCTAssertTrue(el("login-submit").isHittable)
        capture("44-ax3-login")
    }

    func test45_largeTextConnect() {
        launch("connect-connected", accessibilityText: true)
        present("connect-button")
        capture("45-ax3-connect")
        for _ in 0..<6 where !el("plan-card").isHittable { app.swipeUp() }
        XCTAssertTrue(el("plan-card").exists)
        capture("46-ax3-connect-bottom")
    }

    func test46_largeTextLines() {
        launch("lines", accessibilityText: true)
        present("line-row-香港 01")
        capture("47-ax3-lines")
    }

    // MARK: round 4: the connect loop's own screens

    func test47_vpnSetupSheetExplainsTheSystemDialog() {
        launch("connect-vpn-setup")
        present("vpn-setup-sheet")
        said("vpn-setup-title", "首次连接需要添加 VPN 配置")
        present("vpn-setup-continue"); present("vpn-setup-later")
        capture("48-vpn-setup")
        el("vpn-setup-later").tap()
        XCTAssertTrue(el("vpn-setup-sheet").waitForNonExistence(timeout: 5))
        said("status-text", "未连接")
        capture("49-vpn-setup-dismissed")
    }

    func test48_vpnPermissionDenied() {
        launch("connect-vpn-denied")
        said("message-banner", "需要允许 AsterLink 添加 VPN 配置", contains: true)
        XCTAssertTrue(el("connect-button").isEnabled)
        capture("50-vpn-permission-denied")
    }

    func test49_vpnTakenAwayBySystem() {
        launch("connect-vpn-revoked")
        said("message-banner", "VPN 已被系统设置或其它 VPN 应用关闭", contains: true)
        said("status-text", "未连接")
        capture("51-vpn-revoked")
    }

    func test50_routeProbeFailed() {
        launch("connect-probe-failed")
        said("message-banner", "系统流量没有经过隧道", contains: true)
        capture("52-route-probe-failed")
    }
}
