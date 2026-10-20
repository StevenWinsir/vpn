import AsterLinkKit
import Foundation
import Testing
@testable import AsterLink

/// Round-3 account acceptance (handoff_app_ios.md A01-A04), against the real linked Core, the
/// real Keychain, and a real Gin API on an isolated private PostgreSQL (the backend world test
/// TestClientAccountWorld, started by scripts/test-native-ios.py --suite account). The simulator
/// shares the Mac's loopback, so the Core keeps its "HTTP only to loopback" rule and nothing is
/// forwarded. Skipped unless the suite names the world file; a plain test run never touches a network.
///
/// The case list mirrors apps/android AccountAcceptanceTest so the three clients are judged by
/// the same questions. Each case is recorded as PASS or FAIL with what was observed; the script
/// turns the report into the suite result.
@MainActor
@Suite(.serialized, .enabled(if: ProcessInfo.processInfo.environment["ASTERLINK_WORLD_FILE"] != nil))
struct AccountAcceptanceTests {
    struct Case: Encodable { let case_id: String; let status: String; let observed: String }

    @MainActor
    final class Rig {
        var cases: [Case] = []
        let apiBase: String
        let password: String
        let emails: [String: String]
        let nodeIDs: [String: String]
        let credentials = KeychainCredentialStore(service: "com.asterlink.dev.asterlink.accounttest.credentials")
        let preferences = DefaultsPreferences(defaults: UserDefaults(suiteName: "asterlink.accounttest")!)
        let devices = KeychainDeviceID(service: "com.asterlink.dev.asterlink.accounttest.device")
        var controller: SessionController!

        init(world: [String: Any]) {
            apiBase = world["api_base"] as! String
            password = world["password"] as! String
            emails = world["emails"] as! [String: String]
            nodeIDs = world["nodes"] as! [String: String]
        }

        func record(_ id: String, _ ok: Bool, _ observed: String) { cases.append(Case(case_id: id, status: ok ? "PASS" : "FAIL", observed: observed)) }

        func key(_ who: String) -> String { CredentialAccount.key(apiBase: apiBase, email: emails[who]!) }

        /// A fresh Core and controller over the same stores: what a process restart looks like.
        func restart() async {
            await AppCoreHost().stop()
            let client = CoreClient(transport: MobileTransport())
            controller = SessionController(client: client, core: AppCoreHost(),
                                           target: LoginTarget(apiBase: apiBase, channel: .development, appVersion: "0.1.0"),
                                           credentials: credentials, preferences: preferences, devices: devices,
                                           tunnel: DirectCoreTunnel(client: client))
        }

        var state: SessionState { controller.state }

        func waitFor(_ millis: Int, _ predicate: (SessionState) -> Bool) async -> Int? {
            let start = SessionController.monotonicMillis()
            while Int(SessionController.monotonicMillis() - start) < millis {
                if predicate(controller.state) { return Int(SessionController.monotonicMillis() - start) }
                try? await Task.sleep(for: .milliseconds(50))
            }
            return nil
        }

        /// The world's loopback control calls (what an administrator does in the web console).
        func world(_ action: String, _ body: String) async throws {
            var request = URLRequest(url: URL(string: "http://127.0.0.1:\(URL(string: apiBase)!.port!)/__world/\(action)")!)
            request.httpMethod = "POST"
            request.httpBody = Data(body.utf8)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            let (data, response) = try await URLSession(configuration: .ephemeral).data(for: request)
            let status = (response as? HTTPURLResponse)?.statusCode ?? 0
            guard status == 204 else { throw NSError(domain: "world", code: status, userInfo: [NSLocalizedDescriptionKey: String(decoding: data.prefix(300), as: UTF8.self)]) }
        }
    }

    @Test func acceptance() async throws {
        let env = ProcessInfo.processInfo.environment
        let worldPath = try #require(env["ASTERLINK_WORLD_FILE"])
        let reportPath = try #require(env["ASTERLINK_REPORT_FILE"])
        let world = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: URL(fileURLWithPath: worldPath))) as? [String: Any])
        let rig = Rig(world: world)
        // The world's password is test-only, but it still never stays on the device.
        try? FileManager.default.removeItem(atPath: worldPath)
        do { try await run(rig) } catch { rig.record("A.unexpected", false, "\(type(of: error)): \(String(describing: error).prefix(200))") }
        await AppCoreHost().stop()
        let report = try JSONEncoder().encode(["cases": rig.cases])
        try report.write(to: URL(fileURLWithPath: reportPath))
        let failed = rig.cases.filter { $0.status != "PASS" }
        #expect(failed.isEmpty, "failed: \(failed.map { "\($0.case_id): \($0.observed)" })")
        #expect(rig.cases.count >= 15)
    }

    private func run(_ rig: Rig) async throws {
        let vip = rig.emails["vip"]!
        let password = rig.password

        // A01: a wrong password is refused online. A typo leaves the saved item alone; the saved
        // password itself, once refused (changed on the website), is forgotten (handoff_auth.md 9.2).
        await rig.restart()
        try await rig.credentials.save(account: rig.key("vip"), password: "stale-password")
        await rig.controller.launch()
        await rig.controller.signIn(email: vip, password: "definitely-wrong", remember: true)
        var stale = try await rig.credentials.read(account: rig.key("vip"))
        rig.record("A01.typo_keeps_saved", rig.state.phase == .signedOut && rig.state.message?.code == "invalid_credentials" && stale == "stale-password",
                   "phase \(rig.state.phase), message \(rig.state.message?.code ?? "-"), saved item kept \(stale == "stale-password")")
        await rig.controller.signIn(email: vip, password: "stale-password", remember: true)
        stale = try await rig.credentials.read(account: rig.key("vip"))
        rig.record("A01.wrong_password", rig.state.phase == .signedOut && rig.state.message?.code == "invalid_credentials" && stale == nil,
                   "phase \(rig.state.phase), message \(rig.state.message?.code ?? "-"), stale password deleted \(stale == nil)")

        // A01: sign in and remember: Keychain entry under API base + email, email prefilled.
        await rig.controller.signIn(email: vip, password: password, remember: true)
        let saved = try await rig.credentials.read(account: rig.key("vip"))
        let signedIn = rig.state
        rig.record("A01.sign_in_remember",
                   signedIn.phase == .signedIn && saved == password && rig.preferences.rememberedEmail() == vip && signedIn.snapshot.user?.email == vip,
                   "phase \(signedIn.phase), state \(signedIn.snapshot.state), plan \(signedIn.snapshot.entitlement?.planName ?? "-"), \(signedIn.snapshot.nodes.count) lines")
        let catalog = await rig.waitFor(8_000) { $0.snapshot.nodes.count == 2 }
        rig.record("A01.lines_after_login", catalog != nil, "VIP sees dedicated + ordinary lines after \(catalog.map(String.init) ?? "timeout") ms")

        // Nothing secret at rest: no password or bearer token in any file the app wrote (the Keychain is the only place).
        let leaks = filesHolding([password, "Bearer "])
        rig.record("A01.nothing_in_clear", leaks.isEmpty, leaks.isEmpty ? "no file under the app's data directory holds the password or a bearer token" : "found in \(leaks)")

        // A01: a "restart" signs in online with the remembered password (no stored token).
        await rig.restart()
        await rig.controller.launch()
        let relaunched = rig.state
        rig.record("A01.relaunch_online", relaunched.phase == .signedIn && relaunched.rememberPassword && relaunched.snapshot.user?.email == vip,
                   "a fresh Core + controller: phase \(relaunched.phase), signed in online as the remembered account")

        // A01: explicit sign-out revokes and forgets the password and the email.
        await rig.controller.signOut()
        stale = try await rig.credentials.read(account: rig.key("vip"))
        rig.record("A01.sign_out_forgets", rig.state.phase == .signedOut && stale == nil && rig.preferences.rememberedEmail() == nil,
                   "after sign-out: saved password \(stale != nil), remembered email \(rig.preferences.rememberedEmail() ?? "none")")

        // A01: without "remember" nothing is stored and the next launch asks for the password.
        await rig.controller.signIn(email: vip, password: password, remember: false)
        let notSaved = try await rig.credentials.read(account: rig.key("vip")) == nil
        await rig.controller.signOut()
        await rig.restart()
        await rig.controller.launch()
        rig.record("A01.no_remember", notSaved && rig.state.phase == .signedOut, "nothing saved; relaunch waits on the login screen (\(rig.state.phase))")

        // A02: an administrator without a plan is not let through because of the role.
        await rig.controller.signIn(email: rig.emails["admin"]!, password: password, remember: false)
        _ = await rig.waitFor(8_000) { $0.snapshot.state == "blocked" }
        var s = rig.state
        rig.record("A02.admin_without_plan",
                   s.snapshot.user?.role == "admin" && !s.connection.canConnect && s.connection.needsPurchase && s.snapshot.nodes.isEmpty && s.snapshot.entitlement?.reason == "upgrade_required",
                   "role \(s.snapshot.user?.role ?? "-"), state \(s.snapshot.state), reason \(s.snapshot.entitlement?.reason ?? "-"), \(s.snapshot.nodes.count) lines, connect enabled \(s.connection.canConnect)")
        await rig.controller.signOut()

        // A02: a used-up plan.
        await rig.controller.signIn(email: rig.emails["broke"]!, password: password, remember: false)
        _ = await rig.waitFor(8_000) { $0.snapshot.entitlement?.reason == "quota_exhausted" }
        s = rig.state
        rig.record("A02.zero_quota",
                   s.snapshot.entitlement?.reason == "quota_exhausted" && !s.connection.canConnect && s.connection.needsPurchase && s.snapshot.nodes.isEmpty,
                   "reason \(s.snapshot.entitlement?.reason ?? "-"), remaining \(s.snapshot.entitlement?.remainingBytes ?? -1) B, \(s.snapshot.nodes.count) lines")
        await rig.controller.signOut()

        // A02 + A04: a plan that ends while the app is open.
        await rig.controller.signIn(email: rig.emails["expiring"]!, password: password, remember: false)
        _ = await rig.waitFor(8_000) { !$0.snapshot.nodes.isEmpty }
        await rig.controller.select(rig.nodeIDs["dedicated"]!)
        let selectedBefore = rig.state.snapshot.configReady
        try await rig.world("expire", #"{"user":"expiring"}"#)
        let expiredAfter = await rig.waitFor(15_000) { $0.snapshot.entitlement?.accountAuthorized == false && $0.snapshot.nodes.isEmpty }
        s = rig.state
        rig.record("A02.expiry_while_open", selectedBefore && expiredAfter != nil && !s.connection.canConnect,
                   "line selected first; plan ended on the server, reflected after \(expiredAfter.map(String.init) ?? "timeout") ms (reason \(s.snapshot.entitlement?.reason ?? "-"), \(s.snapshot.nodes.count) lines)")
        await rig.controller.signOut()

        // A04: the standard plan sees only the ordinary line (the dedicated one is not in its plan).
        await rig.controller.signIn(email: rig.emails["basic"]!, password: password, remember: false)
        _ = await rig.waitFor(8_000) { !$0.snapshot.nodes.isEmpty }
        s = rig.state
        rig.record("A04.plan_filter", s.snapshot.nodes.map(\.id) == [rig.nodeIDs["ordinary"]!],
                   "basic plan lines: \(s.snapshot.nodes.map { "\($0.lineType)/\($0.ratePermille)" })")
        await rig.controller.select(rig.nodeIDs["ordinary"]!)

        // A04: rename + new rate made by the administrator.
        try await rig.world("edit", #"{"node":"ordinary","name":"JP-Renamed-By-Admin","rate_permille":1500}"#)
        let renamed = await rig.waitFor(15_000) { st in st.snapshot.nodes.contains { $0.name == "JP-Renamed-By-Admin" && $0.ratePermille == 1500 } }
        s = rig.state
        rig.record("A04.rename_and_rate", renamed != nil && (renamed ?? .max) <= 7_000 && !s.snapshot.configReady,
                   "rename + 1.5x seen after \(renamed.map(String.init) ?? "timeout") ms; the bound config was dropped (config_ready \(s.snapshot.configReady), last_error \(s.snapshot.lastError?.code ?? "-"))")

        // A04: disabled, then enabled again, then deleted.
        await rig.controller.select(rig.nodeIDs["ordinary"]!)
        try await rig.world("edit", #"{"node":"ordinary","enabled":false}"#)
        let disabled = await rig.waitFor(15_000) { $0.snapshot.nodes.isEmpty }
        s = rig.state
        rig.record("A04.disable", disabled != nil && (disabled ?? .max) <= 7_000 && s.snapshot.selectedNodeID.isEmpty,
                   "disabled line gone after \(disabled.map(String.init) ?? "timeout") ms, selection cleared \(s.snapshot.selectedNodeID.isEmpty)")
        try await rig.world("edit", #"{"node":"ordinary","enabled":true}"#)
        let enabled = await rig.waitFor(15_000) { $0.snapshot.nodes.count == 1 }
        rig.record("A04.enable", enabled != nil && (enabled ?? .max) <= 7_000, "re-enabled line back after \(enabled.map(String.init) ?? "timeout") ms")
        try await rig.world("delete", #"{"node":"ordinary"}"#)
        let deleted = await rig.waitFor(15_000) { $0.snapshot.nodes.isEmpty }
        rig.record("A04.delete", deleted != nil && (deleted ?? .max) <= 7_000, "deleted line gone after \(deleted.map(String.init) ?? "timeout") ms")

        // Redaction: what the app could show or copy carries no node secret or account detail.
        let diagnostics = Diagnostics.report(appVersion: "0", channel: .development, state: rig.state)
        let text = String(describing: rig.state) + diagnostics
        let secrets = ["metadata-private.example.invalid", "never-reaches-the-ui", "aes-256-gcm", password]
        rig.record("A.redaction", secrets.allSatisfy { !text.contains($0) }, "UI state and diagnostics contain no node address, credential, cipher or password")
        await rig.controller.signOut()

        // A02: the plan's only device slot is taken by another device: binding a line says so.
        try await rig.world("occupy", #"{"user":"vip","node":"dedicated"}"#)
        await rig.controller.signIn(email: vip, password: password, remember: false)
        _ = await rig.waitFor(8_000) { !$0.snapshot.nodes.isEmpty }
        await rig.controller.select(rig.nodeIDs["dedicated"]!)
        s = rig.state
        rig.record("A02.device_limit", s.message?.code == "device_limit" && !s.snapshot.configReady, "second device: \(s.message?.code ?? "-") — \(s.message?.text ?? "-")")
        await rig.controller.signOut()
    }

    /// Files under the app's data directory (outside the Keychain) that contain any of the needles.
    private func filesHolding(_ needles: [String]) -> [String] {
        guard let walker = FileManager.default.enumerator(at: URL(fileURLWithPath: NSHomeDirectory()), includingPropertiesForKeys: [.isRegularFileKey, .fileSizeKey]) else { return [] }
        var found: [String] = []
        for case let url as URL in walker {
            let values = try? url.resourceValues(forKeys: [.isRegularFileKey, .fileSizeKey])
            guard values?.isRegularFile == true, (values?.fileSize ?? 0) < 4_000_000, let data = try? Data(contentsOf: url) else { continue }
            let text = String(decoding: data, as: UTF8.self)
            if needles.contains(where: { text.contains($0) }) { found.append(url.lastPathComponent) }
        }
        return found
    }
}
