import Foundation
import Security
import XCTest
@testable import AsterLinkShared

private func repoRoot() -> URL {
    URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
}

private func snapshotSamples() throws -> [String: Any] {
    let data = try Data(contentsOf: repoRoot().appendingPathComponent("shared/contracts/snapshot-v1.json"))
    return (try JSONSerialization.jsonObject(with: data) as! [String: Any])["snapshots"] as! [String: Any]
}

private func json(_ object: Any) -> Data { try! JSONSerialization.data(withJSONObject: object) }

private func envelope(result: Any?, ok: Bool = true, error: [String: String]? = nil) -> Data {
    var body: [String: Any] = ["request_id": "r", "protocol_version": 1, "generation": 0, "ok": ok]
    if let result { body["result"] = result }
    if let error { body["error"] = error }
    return json(body)
}

private func reply(_ snapshot: Any, actionError: [String: String]? = nil) -> Data {
    var result = snapshot as! [String: Any]
    result["action_error"] = actionError ?? NSNull()
    return envelope(result: result)
}

/// A scripted stand-in for the Helper+Core pair.
private final class FakeTransport: CoreTransport, @unchecked Sendable {
    private let lock = NSLock()
    var handler: (String, UInt64, [String: Any]) throws -> Data = { _, _, _ in Data() }
    private(set) var commands: [String] = []
    private(set) var payloads: [[String: Any]] = []
    var startError: Error?
    private(set) var stopped = false

    func start() async throws { if let startError { throw startError } }
    func send(command: String, generation: UInt64, payload: Data) async throws -> Data {
        let object = (try? JSONSerialization.jsonObject(with: payload)) as? [String: Any] ?? [:]
        lock.lock(); commands.append(command); payloads.append(object); lock.unlock()
        return try handler(command, generation, object)
    }
    func stop() async { stopped = true }
}

private let environment = try! ClientEnvironment.parse("APP_ENV=development\nAPI_BASE_URL=http://127.0.0.1:8080/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn\n")

private final class MemoryPreferences: LoginPreferences, @unchecked Sendable {
    var rememberedEmail: String?
}

@MainActor
private func makeController(_ transport: FakeTransport, store: InMemoryCredentialStore = InMemoryCredentialStore(),
                            preferences: MemoryPreferences = MemoryPreferences()) -> SessionController {
    let client = CoreClient(transport: transport, environment: environment, appVersion: "test")
    return SessionController(client: client, environment: environment, credentials: store, preferences: preferences,
                             devices: FixedDeviceIdentity("33333333-3333-4333-8333-333333333333"), pollInterval: .milliseconds(20))
}

final class SnapshotContractTests: XCTestCase {
    func testSharedSamplesDecode() async throws {
        let samples = try snapshotSamples()
        XCTAssertGreaterThanOrEqual(samples.count, 3)
        for (name, sample) in samples {
            let transport = FakeTransport()
            transport.handler = { _, _, _ in envelope(result: sample) }
            let client = CoreClient(transport: transport, environment: environment, appVersion: "t")
            let snapshot = try await client.readSnapshot()
            XCTAssertFalse(snapshot.coreVersion.isEmpty, name)
        }
        let transport = FakeTransport()
        transport.handler = { _, _, _ in envelope(result: samples["ready_with_selection"]) }
        let snap = try await CoreClient(transport: transport, environment: environment, appVersion: "t").readSnapshot()
        XCTAssertEqual(snap.state, .ready)
        XCTAssertEqual(snap.nodes.count, 2)
        XCTAssertEqual(snap.selectedNode?.name, "HK Dedicated")
        // Stage 3 metering: physical bytes, confirmed and estimated balance, live history.
        let live = try await {
            let t = FakeTransport()
            t.handler = { _, _, _ in envelope(result: samples["connected_metering"]) }
            return try await CoreClient(transport: t, environment: environment, appVersion: "t").readSnapshot()
        }()
        XCTAssertEqual(live.state, .connected)
        XCTAssertEqual(live.tunnelHealth, "up")
        XCTAssertEqual(live.metering?.history.count, 3)
        XCTAssertEqual(live.metering?.unreportedBytes, 1_048_576)
        XCTAssertEqual(live.metering?.estimatedRemainingBytes, 967_835_648)
        XCTAssertEqual(live.metering?.ratePermille, 1000)
        XCTAssertEqual(snap.entitlement?.remainingBytes, 1073741000)
        XCTAssertNotNil(snap.entitlement?.subscriptionExpiresAt)
        XCTAssertTrue(snap.configReady)
    }

    func testNodeReportedSnapshotDecodesAndLabelsTheLine() async throws {
        let sample = try XCTUnwrap(try snapshotSamples()["connected_node_reported"])
        let transport = FakeTransport()
        transport.handler = { _, _, _ in envelope(result: sample) }
        let snap = try await CoreClient(transport: transport, environment: environment, appVersion: "t").readSnapshot()
        XCTAssertEqual(snap.metering?.source, "node_reported")
        XCTAssertEqual(snap.metering?.confirmedRemainingBytes, 1_073_610_752)
        XCTAssertEqual(snap.nodes.first?.meteringSource, "node_reported")
        XCTAssertTrue(snap.nodes.first?.isNodeMetered ?? false)
        XCTAssertFalse(CoreNode(id: "a", name: "n", region: "HK", lineType: "direct", ratePermille: 1000).isNodeMetered)
    }

    func testNodeLabels() {
        let dedicated = CoreNode(id: "a", name: "n", region: "HK", lineType: "dedicated", ratePermille: 1000)
        let ordinary = CoreNode(id: "b", name: "n", region: "JP", lineType: "direct", ratePermille: 500)
        XCTAssertEqual(dedicated.lineLabel, "专线")
        XCTAssertEqual(ordinary.lineLabel, "普通线路")
        XCTAssertEqual(dedicated.rateLabel, "1×")
        XCTAssertEqual(ordinary.rateLabel, "0.5×")
        XCTAssertEqual(CoreNode.formatRate(1250), "1.25×")
        XCTAssertEqual(CoreNode.formatRate(2000), "2×")
        XCTAssertEqual(CoreNode.formatRate(1001), "1.001×")
    }

    func testRefusedCommandSurfacesCode() async {
        let transport = FakeTransport()
        transport.handler = { _, _, _ in envelope(result: nil, ok: false, error: ["code": "stale_generation", "message": "状态已变化"]) }
        let client = CoreClient(transport: transport, environment: environment, appVersion: "t")
        do {
            _ = try await client.connect(generation: 1)
            XCTFail("expected a refusal")
        } catch let error as CoreCommandError {
            XCTAssertEqual(error.code, "stale_generation")
        } catch { XCTFail("\(error)") }
    }

    func testGarbageAndWrongProtocolRejected() async {
        let transport = FakeTransport()
        transport.handler = { _, _, _ in Data("not json".utf8) }
        let client = CoreClient(transport: transport, environment: environment, appVersion: "t")
        do { _ = try await client.readSnapshot(); XCTFail() } catch { XCTAssertEqual(error as? CoreClientError, .malformedReply) }
        transport.handler = { _, _, _ in json(["request_id": "r", "protocol_version": 9, "generation": 0, "ok": true, "result": try! snapshotSamples()["blocked_no_plan"]!]) }
        do { _ = try await client.readSnapshot(); XCTFail() } catch { XCTAssertEqual(error as? CoreClientError, .protocolMismatch) }
    }
}

final class CommandPolicyTests: XCTestCase {
    func testWhitelistOnly() throws {
        let allowed = ["login", "read_snapshot", "refresh_catalog", "select_node", "connect", "disconnect", "logout"]
        XCTAssertEqual(Set(HelperCommandPolicy.limits.keys), Set(allowed))
        for name in allowed {
            XCTAssertNoThrow(try HelperCommandPolicy.buildRequest(requestID: "r", name: name, generation: 1, payload: Data("{}".utf8)), name)
        }
        // Shutdown, hello and anything exotic are not free-form commands.
        for name in ["shutdown", "hello", "exec", "start_tun", "", "LOGIN", "login "] {
            XCTAssertThrowsError(try HelperCommandPolicy.buildRequest(requestID: "r", name: name, generation: 0, payload: Data("{}".utf8)), name) {
                XCTAssertEqual($0 as? HelperCommandPolicy.Rejection, .unknownCommand)
            }
        }
    }

    func testPayloadBounds() {
        let big = Data(("{\"a\":\"" + String(repeating: "x", count: 70) + "\"}").utf8)
        XCTAssertThrowsError(try HelperCommandPolicy.buildRequest(requestID: "r", name: "connect", generation: 0, payload: big)) {
            XCTAssertEqual($0 as? HelperCommandPolicy.Rejection, .payloadTooLarge)
        }
        for payload in ["[1]", "\"str\"", "12", "not json", ""] {
            XCTAssertThrowsError(try HelperCommandPolicy.buildRequest(requestID: "r", name: "connect", generation: 0, payload: Data(payload.utf8)), payload) {
                XCTAssertEqual($0 as? HelperCommandPolicy.Rejection, .payloadNotObject)
            }
        }
    }

    func testRequestShape() throws {
        let built = try HelperCommandPolicy.buildRequest(requestID: "abc", name: "select_node", generation: 7, payload: Data("{\"node_id\":\"n\"}".utf8))
        let object = try JSONSerialization.jsonObject(with: built.body) as! [String: Any]
        XCTAssertEqual(object["request_id"] as? String, "abc")
        XCTAssertEqual(object["command"] as? String, "select_node")
        XCTAssertEqual(object["generation"] as? Int, 7)
        XCTAssertEqual(object["protocol_version"] as? Int, AsterLinkIdentity.protocolVersion)
        XCTAssertEqual((object["payload"] as? [String: Any])?["node_id"] as? String, "n")
        XCTAssertEqual(Set(object.keys), ["request_id", "protocol_version", "generation", "command", "payload"])
    }
}

final class CredentialStoreTests: XCTestCase {
    func testAccountKeyIsScopedByServerAndEmail() {
        let a = URL(string: "https://demo.hyshentou.cn/api/v1/client")!
        let b = URL(string: "https://evil.example/api/v1/client")!
        XCTAssertEqual(CredentialAccount.key(apiBase: a, email: " Test@Test.com "), CredentialAccount.key(apiBase: a, email: "test@test.com"))
        XCTAssertNotEqual(CredentialAccount.key(apiBase: a, email: "test@test.com"), CredentialAccount.key(apiBase: b, email: "test@test.com"))
        XCTAssertNotEqual(CredentialAccount.key(apiBase: a, email: "a@test.com"), CredentialAccount.key(apiBase: a, email: "b@test.com"))
    }

    /// Uses a throwaway service name so real items are never touched.
    func testKeychainRoundTrip() throws {
        let store = KeychainCredentialStore(service: "com.asterlink.vpn.tests.\(UUID().uuidString)")
        let account = "https://x.invalid/api/v1/client|a@b.cd"
        defer { try? store.delete(account: account) }
        do {
            XCTAssertNil(try store.password(for: account))
            try store.save(account: account, password: "first")
            XCTAssertEqual(try store.password(for: account), "first")
            try store.save(account: account, password: "second")
            XCTAssertEqual(try store.password(for: account), "second")
            try store.delete(account: account)
            XCTAssertNil(try store.password(for: account))
            XCTAssertNoThrow(try store.delete(account: account), "deleting a missing item is idempotent")
        } catch CredentialError.keychain(let status) where status == errSecInteractionNotAllowed || status == errSecNotAvailable || status == -34018 {
            throw XCTSkip("Keychain not available in this environment (status \(status))")
        }
    }

    func testDeviceIdentityIsStable() throws {
        let service = "com.asterlink.vpn.tests.device.\(UUID().uuidString)"
        let first = KeychainDeviceIdentity(service: service)
        do {
            let id = try first.deviceID()
            XCTAssertNotNil(UUID(uuidString: id))
            XCTAssertEqual(try KeychainDeviceIdentity(service: service).deviceID(), id, "a new instance must reuse the stored id")
            XCTAssertEqual(try first.deviceID(), id)
            let other = try KeychainDeviceIdentity(service: service + ".b").deviceID()
            XCTAssertNotEqual(other, id)
            for s in [service, service + ".b"] {
                SecItemDelete([kSecClass: kSecClassGenericPassword, kSecAttrService: s] as CFDictionary)
            }
        } catch CredentialError.keychain(let status) where status == errSecInteractionNotAllowed || status == errSecNotAvailable || status == -34018 {
            throw XCTSkip("Keychain not available in this environment (status \(status))")
        }
    }
}

@MainActor
final class SessionControllerTests: XCTestCase {
    private var ready: Any { try! snapshotSamples()["ready_with_selection"]! }
    private var blocked: Any { try! snapshotSamples()["blocked_no_plan"]! }
    private var expired: Any { try! snapshotSamples()["signed_out_after_expiry"]! }
    private let account = CredentialAccount.key(apiBase: environment.apiBase, email: "fixture@example.invalid")

    private func loginTransport(result: Any? = nil, actionError: [String: String]? = nil) -> FakeTransport {
        let transport = FakeTransport()
        transport.handler = { [self] command, _, _ in
            switch command {
            case "login": return reply(result ?? ready, actionError: actionError)
            case "read_snapshot": return envelope(result: result ?? ready)
            case "logout": return envelope(result: expired)
            default: return reply(ready)
            }
        }
        return transport
    }

    func testLoginWithRememberStoresInKeychainAndSendsDeviceID() async {
        let transport = loginTransport()
        let store = InMemoryCredentialStore()
        let prefs = MemoryPreferences()
        let controller = makeController(transport, store: store, preferences: prefs)
        await controller.signIn(email: "fixture@example.invalid", password: "Fixture-Only-Password!", remember: true)
        XCTAssertEqual(controller.phase, .signedIn)
        XCTAssertEqual(try store.password(for: account), "Fixture-Only-Password!")
        XCTAssertEqual(prefs.rememberedEmail, "fixture@example.invalid")
        let payload = transport.payloads[transport.commands.firstIndex(of: "login")!]
        XCTAssertEqual(payload["device_id"] as? String, "33333333-3333-4333-8333-333333333333")
        XCTAssertEqual(payload["api_base"] as? String, "http://127.0.0.1:8080/api/v1/client")
        XCTAssertEqual(payload["channel"] as? String, "development")
        XCTAssertEqual(Set(payload.keys), ["email", "password", "api_base", "channel", "device_id", "app_version"])
        // The controller must not keep the password anywhere.
        XCTAssertFalse(String(reflecting: Mirror(reflecting: controller).children.map { "\($0.value)" }).contains("Fixture-Only-Password!"))
    }

    func testLoginWithoutRememberDeletesAnySavedPassword() async throws {
        let store = InMemoryCredentialStore()
        try store.save(account: account, password: "old")
        let controller = makeController(loginTransport(), store: store)
        await controller.signIn(email: "fixture@example.invalid", password: "pw", remember: false)
        XCTAssertEqual(controller.phase, .signedIn)
        XCTAssertNil(try store.password(for: account))
    }

    func testKeychainRefusalNeverFallsBackToPlaintext() async throws {
        let store = InMemoryCredentialStore()
        store.failure = .keychain(errSecAuthFailed)
        let prefs = MemoryPreferences()
        let controller = makeController(loginTransport(), store: store, preferences: prefs)
        await controller.signIn(email: "fixture@example.invalid", password: "pw", remember: true)
        XCTAssertEqual(controller.phase, .signedIn, "login itself still succeeds")
        XCTAssertNotNil(controller.warning)
        XCTAssertNil(prefs.rememberedEmail, "nothing is remembered when the Keychain refuses")
        XCTAssertTrue(store.accounts.isEmpty)
    }

    func testWrongPasswordShowsMessageAndDropsStaleSavedPassword() async throws {
        let store = InMemoryCredentialStore()
        try store.save(account: account, password: "stale")
        let transport = loginTransport(result: expired, actionError: ["code": "invalid_credentials", "message": "邮箱或密码不正确"])
        let controller = makeController(transport, store: store)
        await controller.signIn(email: "fixture@example.invalid", password: "stale", remember: true)
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertEqual(controller.message, "邮箱或密码不正确")
        XCTAssertNil(try store.password(for: account), "a rejected saved password must not be offered again")
        XCTAssertTrue(store.accounts.isEmpty)
    }

    func testLaunchAuthenticatesOnlineWithSavedPassword() async throws {
        let store = InMemoryCredentialStore()
        try store.save(account: account, password: "saved")
        let prefs = MemoryPreferences()
        prefs.rememberedEmail = "fixture@example.invalid"
        let transport = loginTransport()
        let controller = makeController(transport, store: store, preferences: prefs)
        XCTAssertEqual(controller.email, "fixture@example.invalid")
        await controller.launch()
        XCTAssertEqual(transport.commands.filter { $0 == "login" }.count, 1, "a remembered password still goes through online login")
        XCTAssertEqual(controller.phase, .signedIn)
    }

    func testLaunchWithoutSavedPasswordDoesNotLogin() async {
        let transport = loginTransport()
        let controller = makeController(transport)
        await controller.launch()
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertFalse(transport.commands.contains("login"))
    }

    // First run: the Helper is approved in System Settings after launch already failed.
    // The App resumes on its own, including the remembered-password sign-in.
    func testApprovalAfterLaunchResumesWithoutUserAction() async throws {
        let store = InMemoryCredentialStore()
        try store.save(account: account, password: "saved")
        let prefs = MemoryPreferences()
        prefs.rememberedEmail = "fixture@example.invalid"
        let transport = loginTransport()
        transport.startError = HelperUnavailable("特权服务尚未启用，请先在设置中启用")
        let controller = makeController(transport, store: store, preferences: prefs)
        await controller.launch()
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertNotNil(controller.serviceProblem)

        transport.startError = nil
        await controller.serviceBecameAvailable()
        XCTAssertNil(controller.serviceProblem)
        XCTAssertEqual(controller.phase, .signedIn)
        XCTAssertEqual(transport.commands.filter { $0 == "login" }.count, 1)

        await controller.serviceBecameAvailable() // already running: nothing repeats
        XCTAssertEqual(transport.commands.filter { $0 == "login" }.count, 1)
    }

    func testServiceAvailableIsANoOpWithoutAPriorProblem() async {
        let transport = loginTransport()
        let controller = makeController(transport)
        await controller.launch()
        let before = transport.commands
        await controller.serviceBecameAvailable()
        XCTAssertEqual(transport.commands, before)
        XCTAssertEqual(controller.phase, .signedOut)
    }

    func testHelperUnavailableIsReportedNotHidden() async {
        let transport = loginTransport()
        transport.startError = HelperUnavailable("特权服务尚未启用，请先在设置中启用")
        let controller = makeController(transport)
        await controller.launch()
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertEqual(controller.serviceProblem, "特权服务尚未启用，请先在设置中启用")
        await controller.signIn(email: "a@b.cd", password: "pw", remember: false)
        XCTAssertEqual(controller.message, "特权服务尚未启用，请先在设置中启用")
        XCTAssertFalse(transport.commands.contains("login"), "no credentials are sent when the Core is unreachable")
    }

    func testSignOutForgetsSavedPassword() async throws {
        let store = InMemoryCredentialStore()
        let prefs = MemoryPreferences()
        let controller = makeController(loginTransport(), store: store, preferences: prefs)
        await controller.signIn(email: "fixture@example.invalid", password: "pw", remember: true)
        XCTAssertNotNil(try store.password(for: account))
        await controller.signOut()
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertNil(try store.password(for: account))
        XCTAssertNil(prefs.rememberedEmail)
    }

    func testNormalQuitKeepsRememberedPasswordButStopsCore() async throws {
        let store = InMemoryCredentialStore()
        let transport = loginTransport()
        let controller = makeController(transport, store: store)
        await controller.signIn(email: "fixture@example.invalid", password: "pw", remember: true)
        await controller.shutdown()
        XCTAssertTrue(transport.stopped)
        XCTAssertEqual(try store.password(for: account), "pw")
    }

    func testExpiredSessionTriggersExactlyOneSilentRelogin() async throws {
        let store = InMemoryCredentialStore()
        try store.save(account: account, password: "saved")
        let prefs = MemoryPreferences()
        prefs.rememberedEmail = "fixture@example.invalid"
        let transport = FakeTransport()
        let state = Counter()
        transport.handler = { [self] command, _, _ in
            switch command {
            case "login":
                state.logins += 1
                return reply(ready)
            case "read_snapshot":
                // After the first login the server drops the session; later logins stay valid
                // but their snapshot immediately expires again to prove there is no loop.
                return envelope(result: expired)
            default: return reply(ready)
            }
        }
        let controller = makeController(transport, store: store, preferences: prefs)
        await controller.launch()
        for _ in 0..<100 where state.logins < 2 { try await Task.sleep(for: .milliseconds(20)) }
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertEqual(state.logins, 2, "one launch login + exactly one silent re-login")
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertEqual(controller.message, "登录已失效，请重新登录")
    }

    func testStaleGenerationRefreshesSnapshotInsteadOfFailing() async {
        let transport = FakeTransport()
        transport.handler = { [self] command, _, _ in
            switch command {
            case "login": return reply(ready)
            case "connect": return envelope(result: nil, ok: false, error: ["code": "stale_generation", "message": "状态已变化，请刷新后重试"])
            default: return envelope(result: ready)
            }
        }
        let controller = makeController(transport)
        await controller.signIn(email: "fixture@example.invalid", password: "pw", remember: false)
        await controller.connect()
        XCTAssertEqual(controller.phase, .signedIn)
        XCTAssertTrue(transport.commands.contains("read_snapshot"))
        XCTAssertEqual(controller.message, "状态已变化，请刷新后重试")
    }

    func testRefusedConnectShowsReasonAndKeepsSnapshot() async {
        let transport = FakeTransport()
        transport.handler = { [self] command, _, _ in
            command == "connect" ? reply(blocked, actionError: ["code": "upgrade_required", "message": "当前账号没有有效套餐，请前往网站购买"]) : reply(ready)
        }
        let controller = makeController(transport)
        await controller.signIn(email: "fixture@example.invalid", password: "pw", remember: false)
        await controller.connect()
        XCTAssertEqual(controller.message, "当前账号没有有效套餐，请前往网站购买")
        XCTAssertEqual(controller.snapshot.entitlement?.reason, "upgrade_required")
        XCTAssertEqual(controller.snapshot.state, .blocked)
    }

    // Node groups round 6: the device limit is checked when connecting. The refusal is the
    // Core's sentence; the account stays signed in and may try again.
    func testDeviceLimitAtConnectIsShownInTheCoresWordsAndStaysSignedIn() async {
        let transport = FakeTransport()
        transport.handler = { [self] command, _, _ in
            command == "connect" ? reply(ready, actionError: ["code": "device_limit", "message": "已达到同时在线设备上限，请先断开其他设备"]) : reply(ready)
        }
        let controller = makeController(transport)
        await controller.signIn(email: "fixture@example.invalid", password: "pw", remember: false)
        await controller.connect()
        XCTAssertEqual(controller.message, "已达到同时在线设备上限，请先断开其他设备")
        XCTAssertEqual(controller.phase, .signedIn)
        XCTAssertEqual(controller.snapshot.state, .ready)
    }

    // connection_reassigned: the Core moves to another server of the line by itself (stopping,
    // preparing, connecting, connected). The app only follows; it neither signs out nor sends
    // a command that would cancel the reconnect.
    func testServerSideMoveIsFollowedWithoutACommand() async throws {
        var connected = ready as! [String: Any]
        connected["state"] = "connected"
        var moving = connected
        moving["state"] = "preparing"
        moving["last_error"] = ["code": "connection_reassigned", "message": "线路服务器已调整，正在为你重新连接"]
        let reads = Counter()
        let transport = FakeTransport()
        transport.handler = { command, _, _ in
            guard command == "read_snapshot" else { return reply(connected) }
            reads.logins += 1
            return envelope(result: reads.logins < 4 ? moving : connected)
        }
        let controller = makeController(transport)
        await controller.signIn(email: "fixture@example.invalid", password: "pw", remember: false)
        var sawMoving = false
        for _ in 0..<200 where !(sawMoving && controller.snapshot.state == .connected) {
            if controller.snapshot.state == .preparing { sawMoving = true }
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertTrue(sawMoving)
        XCTAssertEqual(controller.snapshot.state, .connected)
        XCTAssertEqual(controller.phase, .signedIn)
        XCTAssertFalse(transport.commands.contains("disconnect"))
        XCTAssertFalse(transport.stopped)
    }

    func testLostWorkerSignsOutAndNextLoginStartsAFreshCore() async {
        let transport = FakeTransport()
        let lost = Counter()
        transport.handler = { [self] command, _, _ in
            switch command {
            case "login": return reply(ready)
            case "connect":
                lost.logins += 1
                return HelperCommandPolicy.failureReply(code: HelperErrorCode.workerFailed.rawValue, message: "worker connection lost")
            default: return envelope(result: ready)
            }
        }
        let controller = makeController(transport)
        await controller.signIn(email: "fixture@example.invalid", password: "pw", remember: false)
        await controller.connect()
        XCTAssertEqual(lost.logins, 1)
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertEqual(controller.snapshot.state, .signedOut)
        XCTAssertEqual(controller.message, "内核已意外退出，连接已停止，请重新登录")
        // A dead Core is never driven again: no further command until the user signs in.
        let sent = transport.commands.count
        await controller.connect()
        try? await Task.sleep(for: .milliseconds(80))
        XCTAssertEqual(transport.commands.count, sent)
        await controller.signIn(email: "fixture@example.invalid", password: "pw", remember: false)
        XCTAssertEqual(controller.phase, .signedIn)
        XCTAssertEqual(transport.commands.last, "login")
    }

    func testWebsiteLinksPointAtConfiguredSite() async {
        let controller = makeController(FakeTransport())
        XCTAssertEqual(controller.plansURL.absoluteString, "https://test.hyshentou.cn/plans")
        XCTAssertEqual(controller.registerURL.absoluteString, "https://test.hyshentou.cn/register")
        XCTAssertEqual(controller.forgotPasswordURL.absoluteString, "https://test.hyshentou.cn/forgot-password")
    }
}

/// handoff_auth.md R4-03: the password was reset on the website while a password was saved here.
@MainActor
final class PasswordResetTests: XCTestCase {
    private var ready: Any { try! snapshotSamples()["ready_with_selection"]! }
    private var expired: Any { try! snapshotSamples()["signed_out_after_expiry"]! }
    private let email = "fixture@example.invalid"
    private var account: String { CredentialAccount.key(apiBase: environment.apiBase, email: email) }
    private let refused = ["code": "invalid_credentials", "message": "邮箱或密码不正确"]

    /// Logins succeed only with `accepted`; `refresh_catalog` reports the session revoked.
    private func server(accepted: String, logins: Counter, onLogin: @escaping (String) -> Void = { _ in }) -> FakeTransport {
        let transport = FakeTransport()
        transport.handler = { [self] command, _, payload in
            switch command {
            case "login":
                logins.logins += 1
                let password = payload["password"] as? String ?? ""
                onLogin(password)
                return password == accepted ? reply(ready) : reply(expired, actionError: refused)
            case "refresh_catalog": return reply(expired)
            case "logout": return envelope(result: expired)
            default: return envelope(result: ready)
            }
        }
        return transport
    }

    func testOldSavedPasswordIsTriedOnceThenForgottenAndNewPasswordIsSaved() async throws {
        let store = InMemoryCredentialStore()
        try store.save(account: account, password: "old-password")
        let prefs = MemoryPreferences()
        prefs.rememberedEmail = email
        let logins = Counter()
        let controller = makeController(server(accepted: "new-password", logins: logins), store: store, preferences: prefs)
        await controller.launch()
        XCTAssertEqual(logins.logins, 1, "exactly one online attempt with the saved password")
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertEqual(controller.message, "邮箱或密码不正确")
        XCTAssertNil(try store.password(for: account), "the stale item is gone")
        XCTAssertEqual(prefs.rememberedEmail, email, "the email (non-secret) is kept to prefill the form")
        XCTAssertEqual(controller.email, email)
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(logins.logins, 1, "no retry loop after a refused password")

        await controller.signIn(email: email, password: "new-password", remember: true)
        XCTAssertEqual(controller.phase, .signedIn)
        XCTAssertEqual(try store.password(for: account), "new-password")
    }

    func testTypoInAManualLoginKeepsTheSavedPassword() async throws {
        let store = InMemoryCredentialStore()
        try store.save(account: account, password: "good-password")
        let controller = makeController(server(accepted: "good-password", logins: Counter()), store: store)
        await controller.signIn(email: email, password: "typo-password", remember: true)
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertEqual(try store.password(for: account), "good-password")
    }

    func testRefusalNeverDeletesAPasswordSavedMeanwhile() async throws {
        let store = InMemoryCredentialStore()
        try store.save(account: account, password: "old-password")
        // While the old password is on its way to the server, another login saves the new one.
        let transport = server(accepted: "new-password", logins: Counter()) { password in
            if password == "old-password" { try? store.save(account: self.account, password: "new-password") }
        }
        let controller = makeController(transport, store: store)
        await controller.signIn(email: email, password: "old-password", remember: true)
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertEqual(try store.password(for: account), "new-password")
    }

    func testSilentReloginIsDroppedWhenTheUserSignsOutFirst() async throws {
        let store = InMemoryCredentialStore()
        try store.save(account: account, password: "pw")
        let logins = Counter()
        let controller = makeController(server(accepted: "pw", logins: logins), store: store)
        await controller.signIn(email: email, password: "pw", remember: true)
        await controller.refresh() // the server revoked the session: one silent re-login is scheduled
        XCTAssertEqual(controller.phase, .signedOut)
        await controller.signOut()  // ...but the user signs out before it runs
        try await Task.sleep(for: .milliseconds(150))
        XCTAssertEqual(logins.logins, 1, "the re-login scheduled before the sign-out must not sign back in")
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertNil(try store.password(for: account))
    }

    func testSilentReloginUsesTheAccountThatExpired() async throws {
        let store = InMemoryCredentialStore()
        try store.save(account: account, password: "pw")
        let logins = Counter()
        let transport = server(accepted: "pw", logins: logins)
        let controller = makeController(transport, store: store)
        await controller.signIn(email: email, password: "pw", remember: true)
        await controller.refresh()
        controller.email = "someone-else@example.invalid" // typed into the form before the re-login runs
        for _ in 0..<50 where logins.logins < 2 { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertEqual(logins.logins, 2)
        let emails = zip(transport.commands, transport.payloads).filter { $0.0 == "login" }.map { $0.1["email"] as? String }
        XCTAssertEqual(emails, [email, email], "the saved password is only ever sent for its own account")
    }

    func testLateSnapshotFromBeforeSignOutIsDiscarded() async throws {
        let transport = FakeTransport()
        let gate = DispatchSemaphore(value: 0)
        let reads = Counter()
        transport.handler = { [self] command, _, _ in
            switch command {
            case "login": return reply(ready)
            case "read_snapshot":
                reads.logins += 1
                if reads.logins == 1 { gate.wait() } // the first poll is still in flight during the sign-out
                return envelope(result: ready)
            case "logout": return envelope(result: expired)
            default: return envelope(result: ready)
            }
        }
        let controller = makeController(transport)
        await controller.signIn(email: email, password: "pw", remember: false)
        for _ in 0..<100 where reads.logins < 1 { try await Task.sleep(for: .milliseconds(10)) }
        await controller.signOut()
        gate.signal()
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(controller.phase, .signedOut)
        XCTAssertEqual(controller.snapshot.state, .signedOut, "a poll that started before the sign-out cannot bring the old account back")
    }
}

private final class Counter: @unchecked Sendable {
    private let lock = NSLock()
    private var value = 0
    var logins: Int {
        get { lock.lock(); defer { lock.unlock() }; return value }
        set { lock.lock(); value = newValue; lock.unlock() }
    }
}
