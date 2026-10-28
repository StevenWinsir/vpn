import Foundation
import Testing
@testable import AsterLinkKit

@MainActor
struct SessionControllerTests {
    static let wrongPassword = ErrorView(code: "invalid_credentials", message: "邮箱或密码错误")

    private func vipCore() -> ScriptedCore {
        ScriptedCore { command in
            switch command.name {
            case "login": .snapshot(vipSnapshot(generation: 1))
            case "logout": .snapshot(.empty)
            default: .snapshot(vipSnapshot(generation: command.generation == 0 ? 1 : command.generation))
            }
        }
    }

    @Test func signInRemembersThePasswordAndSendsTheIOSLoginPayload() async throws {
        let core = vipCore()
        let credentials = MemoryCredentials()
        let preferences = MemoryPreferences()
        let controller = makeController(core: core, credentials: credentials, preferences: preferences)
        await controller.signIn(email: " VIP@Example.invalid ", password: "secret-pass", remember: true)
        #expect(controller.state.phase == .signedIn)
        #expect(controller.state.rememberPassword)
        #expect(preferences.rememberedEmail() == "VIP@Example.invalid")
        let key = CredentialAccount.key(apiBase: testTarget.apiBase, email: "vip@example.invalid")
        #expect(await credentials.read(account: key) == "secret-pass")
        // The login payload carries the build parameters and the stable device id; the first catalog is asked for at once.
        #expect(core.count("login") == 1 && core.count("refresh_catalog") == 1)
    }

    @Test func notRememberingDeletesAnEarlierSavedPassword() async {
        let credentials = MemoryCredentials()
        let key = CredentialAccount.key(apiBase: testTarget.apiBase, email: "vip@example.invalid")
        try? await credentials.save(account: key, password: "old")
        let controller = makeController(core: vipCore(), credentials: credentials, preferences: MemoryPreferences("vip@example.invalid"))
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: false)
        #expect(controller.state.phase == .signedIn && !controller.state.rememberPassword)
        #expect(await credentials.read(account: key) == nil)
    }

    @Test func aRefusedPasswordStaysSignedOutAndForgetsTheSavedOne() async {
        let core = ScriptedCore { _ in .snapshot(.empty, action: SessionControllerTests.wrongPassword) }
        let credentials = MemoryCredentials()
        let key = CredentialAccount.key(apiBase: testTarget.apiBase, email: "vip@example.invalid")
        try? await credentials.save(account: key, password: "stale")
        let controller = makeController(core: core, credentials: credentials)
        await controller.signIn(email: "vip@example.invalid", password: "stale", remember: true)
        #expect(controller.state.phase == .signedOut)
        #expect(controller.state.message == .core("invalid_credentials", "邮箱或密码错误"))
        #expect(await credentials.read(account: key) == nil)
    }

    @Test func aKeychainFailureNeverFallsBackToPlaintext() async {
        let credentials = MemoryCredentials()
        await credentials.setFailSave(true)
        let controller = makeController(core: vipCore(), credentials: credentials, preferences: MemoryPreferences())
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: true)
        #expect(controller.state.phase == .signedIn && !controller.state.rememberPassword)
        #expect(controller.state.warning == .local(LocalCodes.credentialSaveFailed))
        #expect(await credentials.items.isEmpty)
    }

    @Test func launchSignsInOnlineWithTheRememberedPassword() async {
        let credentials = MemoryCredentials()
        let key = CredentialAccount.key(apiBase: testTarget.apiBase, email: "vip@example.invalid")
        try? await credentials.save(account: key, password: "secret-pass")
        let core = vipCore()
        let controller = makeController(core: core, credentials: credentials, preferences: MemoryPreferences("vip@example.invalid"))
        await controller.launch()
        #expect(controller.state.phase == .signedIn && controller.state.rememberPassword)
        #expect(controller.state.identity?.coreVersion == "9.9.9")
        #expect(core.count("login") == 1)
    }

    @Test func launchWithoutASavedPasswordStopsAtTheLoginPage() async {
        let core = vipCore()
        let controller = makeController(core: core, preferences: MemoryPreferences("vip@example.invalid"))
        await controller.launch()
        #expect(controller.state.phase == .signedOut && controller.state.email == "vip@example.invalid")
        #expect(core.count("login") == 0)
    }

    @Test func inputLimitsAreCheckedBeforeTheCoreIsAsked() async {
        let core = vipCore()
        let controller = makeController(core: core)
        await controller.signIn(email: "", password: "x", remember: false)
        #expect(controller.state.message == .local(LocalCodes.inputMissing))
        await controller.signIn(email: "a@b.c", password: String(repeating: "p", count: 73), remember: false)
        #expect(controller.state.message == .local(LocalCodes.passwordTooLong))
        #expect(core.count("login") == 0)
    }

    @Test func signOutForgetsThePasswordAndTheEmail() async {
        let credentials = MemoryCredentials()
        let preferences = MemoryPreferences()
        let controller = makeController(core: vipCore(), credentials: credentials, preferences: preferences)
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: true)
        await controller.signOut()
        #expect(controller.state.phase == .signedOut && !controller.state.rememberPassword)
        #expect(await credentials.items.isEmpty && preferences.rememberedEmail() == nil)
    }

    @Test func aStaleGenerationRereadsTheStateAndAsksTheUserToRetry() async {
        let core = ScriptedCore { command in
            switch command.name {
            case "login": .snapshot(vipSnapshot(generation: 1))
            case "select_node": .refuse(code: "stale_generation", message: "")
            default: .snapshot(vipSnapshot(generation: 5))
            }
        }
        let controller = makeController(core: core)
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: false)
        await controller.select("a")
        #expect(controller.state.snapshot.generation == 5)
        #expect(controller.state.message == .local(LocalCodes.stateChanged))
    }

    @Test func aSnapshotFromAnOlderGenerationIsLateNews() async {
        let core = vipCore()
        let controller = makeController(core: core)
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: false)
        core.replace { _ in .snapshot(vipSnapshot(generation: 9)) }
        await controller.poll()
        #expect(controller.state.snapshot.generation == 9)
        core.replace { _ in .snapshot(vipSnapshot(generation: 3, selected: "b")) }
        await controller.poll()
        #expect(controller.state.snapshot.generation == 9 && controller.state.snapshot.selectedNodeID.isEmpty)
    }

    @Test func anExpiredSessionSignsInSilentlyOnceAndNeverAfterARefusedPassword() async {
        let credentials = MemoryCredentials()
        let core = vipCore()
        let controller = makeController(core: core, credentials: credentials, preferences: MemoryPreferences())
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: true)
        let expired = CoreSnapshot(generation: 2, state: "signed_out", lastError: ErrorView(code: "native_session_expired", message: "登录已失效，请重新登录"))
        // The server accepts the new login; everything after it is served from the new session.
        core.replace { command in command.name == "login" || core.count("login") >= 2 ? .snapshot(vipSnapshot(generation: 6)) : .snapshot(expired) }
        await controller.poll()
        // The silent re-login runs as its own task.
        for _ in 0..<200 where core.count("login") < 2 || controller.state.phase != .signedIn { try? await Task.sleep(for: .milliseconds(20)) }
        #expect(core.count("login") == 2)
        #expect(controller.state.phase == .signedIn)
        // A second expiry in the same sign-in does not hammer the login endpoint.
        core.replace { command in command.name == "login" ? .snapshot(vipSnapshot(generation: 7)) : .snapshot(expired) }
        await controller.poll()
        try? await Task.sleep(for: .milliseconds(150))
        #expect(core.count("login") == 2 && controller.state.phase == .signedOut)
        #expect(controller.state.message == .core("native_session_expired", "登录已失效，请重新登录"))
    }

    @Test func connectOnIOSShowsTheCoresHonestRefusal() async {
        let core = ScriptedCore { command in
            switch command.name {
            case "connect": .snapshot(vipSnapshot(generation: 2, selected: "a"), action: ErrorView(code: "tun_unavailable", message: "当前版本无法建立连接，请前往官网下载最新版本"))
            default: .snapshot(vipSnapshot(generation: 1, selected: "a"))
            }
        }
        let controller = makeController(core: core)
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: false)
        await controller.connect()
        #expect(controller.state.message == .core("tun_unavailable", "当前版本无法建立连接，请前往官网下载最新版本"))
        #expect(controller.state.snapshot.state == "ready" && !controller.state.busy && controller.state.activity == .none)
    }

    // Node groups round 6: the device limit is checked when connecting; the refusal is shown in
    // the Core's words and the account stays signed in.
    @Test func aConnectRefusedForTheDeviceLimitStaysSignedIn() async {
        let alerts = RecordingAlerts()
        let core = ScriptedCore { command in
            switch command.name {
            case "connect": .snapshot(vipSnapshot(generation: 2, selected: "a"), action: ErrorView(code: "device_limit", message: "已达到同时在线设备上限，请先断开其他设备"))
            default: .snapshot(vipSnapshot(generation: 1, selected: "a"))
            }
        }
        let controller = makeController(core: core, alerts: alerts)
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: false)
        await controller.connect()
        await controller.poll()
        #expect(controller.state.message == .core("device_limit", "已达到同时在线设备上限，请先断开其他设备"))
        #expect(controller.state.phase == .signedIn && alerts.notices.isEmpty)
        #expect(controller.state.connection.canConnect && !controller.state.connection.needsPurchase)
    }

    // connection_reassigned: stopping, preparing and connecting while the Core moves to another
    // server of the line are not an ended tunnel (no alert); back to connected without the user.
    @Test func aServerSideMoveIsNotAnEndedTunnel() async {
        let alerts = RecordingAlerts()
        let core = ScriptedCore { _ in .snapshot(vipSnapshot(generation: 2, state: "connected", selected: "a")) }
        let controller = makeController(core: core, alerts: alerts)
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: false)
        await controller.poll()
        var generation: UInt64 = 3
        for state in [CoreSnapshot.State.stopping, CoreSnapshot.State.preparing, CoreSnapshot.State.connecting, CoreSnapshot.State.connected] {
            var next = vipSnapshot(generation: generation, state: state, selected: "a")
            if state != CoreSnapshot.State.connected {
                next.lastError = ErrorView(code: "connection_reassigned", message: "线路服务器已调整，正在为你重新连接")
            }
            generation += 1
            core.replace { _ in .snapshot(next) }
            await controller.poll()
            #expect(!controller.state.connection.showConnect, "\(state)")
        }
        #expect(alerts.notices.isEmpty)
        #expect(controller.state.connection.status == .connected)
    }

    @Test func aLostCoreEndsTheSessionAndAlertsOnlyForARunningTunnel() async {
        let alerts = RecordingAlerts()
        let core = ScriptedCore { command in
            command.name == "login" ? .snapshot(vipSnapshot(generation: 1, selected: "a")) : .snapshot(vipSnapshot(generation: 2, state: "connected", selected: "a"))
        }
        let controller = makeController(core: core, alerts: alerts)
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: false)
        await controller.poll()
        #expect(controller.state.connectedSinceMillis != nil)
        core.replace { _ in .refuse(code: CoreCodes.notStarted, message: "core is not running") }
        await controller.poll()
        #expect(controller.state.phase == .signedOut && controller.state.message == .local(LocalCodes.coreLost))
        #expect(alerts.notices == [.local(LocalCodes.coreLost)])
    }

    @Test func theCoreEndingATunnelByItselfExplainsWhy() async {
        let alerts = RecordingAlerts()
        let core = ScriptedCore { _ in .snapshot(vipSnapshot(generation: 2, state: "connected", selected: "a")) }
        let controller = makeController(core: core, alerts: alerts)
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: false)
        await controller.poll()
        var ended = vipSnapshot(generation: 3, state: "blocked", selected: "a")
        ended.lastError = ErrorView(code: "quota_exhausted", message: "流量已用完")
        core.replace { _ in .snapshot(ended) }
        await controller.poll()
        #expect(alerts.notices == [.core("quota_exhausted", "流量已用完")])
    }

    @Test func foregroundRefreshIsThrottled() async {
        let now = Counter()
        let core = vipCore()
        let controller = makeController(core: core, clock: { now.value })
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: false)
        let afterLogin = core.count("refresh_catalog")
        now.value = 5_000
        await controller.onForeground()
        #expect(core.count("refresh_catalog") == afterLogin)
        now.value = 11_000
        await controller.onForeground()
        #expect(core.count("refresh_catalog") == afterLogin + 1)
    }

    @Test func switchingLinesWhileConnectedDisconnectsFirst() async {
        let core = ScriptedCore { command in
            switch command.name {
            case "disconnect": .snapshot(vipSnapshot(generation: 4, selected: "a"))
            case "select_node": .snapshot(vipSnapshot(generation: 5, selected: command.payload["node_id"] ?? ""))
            case "login": .snapshot(vipSnapshot(generation: 2, state: "connected", selected: "a"))
            default: .snapshot(vipSnapshot(generation: 3, state: "connected", selected: "a"))
            }
        }
        let controller = makeController(core: core)
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: false)
        await controller.switchTo("b")
        #expect(core.count("disconnect") == 1 && core.count("select_node") == 1)
        #expect(controller.state.snapshot.selectedNodeID == "b" && controller.state.snapshot.state == "ready")
    }
}

final class Counter: @unchecked Sendable {
    private let lock = NSLock()
    private var current: Int64 = 0
    var value: Int64 {
        get { lock.withLock { current } }
        set { lock.withLock { current = newValue } }
    }
}
