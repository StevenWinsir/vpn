#if DEBUG
import AsterLinkKit
import Foundation

/// Prepared screens for the screenshot suite (AsterLinkUITests). The UI tests launch the app
/// with ASTER_UI_FIXTURE=<name>; the app then shows that state without a Core or a network, so
/// every screen of handoff_app_ios.md 10.3 can be captured and asserted on its own. The real
/// Core and backend are exercised by the account acceptance (AccountAcceptanceTests), not here.
/// Debug builds only: Release compiles none of this.
struct UIFixture {
    var screen = LaunchScreen()
    var apply: (inout SessionState) -> Void
}

enum UIFixtures {
    private static let hk = NodeView(id: "11111111-1111-4111-8111-111111111111", name: "香港 01", region: "香港", lineType: "dedicated", ratePermille: 1000)
    private static let hk2 = NodeView(id: "22222222-2222-4222-8222-222222222222", name: "香港 02", region: "香港", lineType: "direct", ratePermille: 500)
    private static let jp = NodeView(id: "33333333-3333-4333-8333-333333333333", name: "东京 01", region: "日本", lineType: "dedicated", ratePermille: 1000)
    private static let us = NodeView(id: "44444444-4444-4444-8444-444444444444", name: "洛杉矶 01", region: "美国", lineType: "direct", ratePermille: 500, meteringSource: "node_reported")
    private static let nodes = [hk, hk2, jp, us]
    private static let revision = "fixture-revision"

    private static func entitlement(plan: String = "VIP", remaining: Int64 = 68 << 30, total: Int64 = 100 << 30) -> EntitlementView {
        EntitlementView(planName: plan, subscriptionExpiresAt: "2030-12-31T15:59:59Z", authorizationExpiresAt: "2030-12-31T15:59:59Z",
                        leaseRemainingSeconds: 87, accountAuthorized: true, canConnect: true, totalBytes: total, remainingBytes: remaining, usedUnits: (total - remaining) * 1000)
    }

    private static func denied(_ reason: String, _ message: String, plan: String = "", total: Int64 = 0, remaining: Int64 = 0, expires: String? = nil) -> EntitlementView {
        EntitlementView(planName: plan, subscriptionExpiresAt: expires, accountAuthorized: false, reason: reason, reasonMessage: message, totalBytes: total, remainingBytes: remaining)
    }

    private static let user = UserView(id: "u1", email: "vip@example.com", name: "VIP 用户", role: "user")
    private static let admin = UserView(id: "u2", email: "test@test.com", name: "Admin", role: "admin")

    private static func ready(selected: NodeView? = hk, entitlement: EntitlementView = entitlement(), user: UserView = user, nodes: [NodeView] = nodes) -> CoreSnapshot {
        CoreSnapshot(generation: 5, state: "ready", user: user, entitlement: entitlement, nodes: nodes, selectedNodeID: selected?.id ?? "",
                     catalogRevision: nodes.isEmpty ? "" : revision, configReady: selected != nil, tunnelHealth: "idle",
                     metering: MeteringView(source: "client_reported", ratePermille: selected?.ratePermille ?? 0))
    }

    private static func connected(_ node: NodeView = hk, source: String = "client_reported", gate: GateView? = nil) -> CoreSnapshot {
        var snapshot = ready(selected: node)
        snapshot.state = "connected"
        snapshot.tunnelHealth = gate == nil ? "up" : "gated"
        snapshot.gate = gate
        var history: [MeterSample] = []
        for second in 0..<60 {
            let up: Int64 = 20_000 + 9_000 * Int64(second % 7)
            let down: Int64 = 400_000 + 120_000 * Int64(second % 11)
            history.append(MeterSample(upload: up, download: down))
        }
        snapshot.metering = MeteringView(source: source, uploadBytes: 22_020_096, downloadBytes: 83_886_080,
                                         uploadBytesPerSecond: 1_258_291, downloadBytesPerSecond: 8_808_038,
                                         confirmedRemainingBytes: 68 << 30, estimatedRemainingBytes: (68 << 30) - 104_857_600,
                                         ratePermille: node.ratePermille, pending: false, history: history)
        return snapshot
    }

    private static func connectedState(_ snapshot: CoreSnapshot, since: Int64 = 751_000) -> (inout SessionState) -> Void {
        { state in
            signedIn(snapshot)(&state)
            state.connectedSinceMillis = SessionController.monotonicMillis() - since
            state.connectionBaseline = MeterSample()
        }
    }

    private static func signedIn(_ snapshot: CoreSnapshot, activity: SessionActivity = .none, message: Notice? = nil, warning: Notice? = nil) -> (inout SessionState) -> Void {
        { state in
            state.phase = .signedIn
            state.snapshot = snapshot
            state.activity = activity
            state.busy = activity != .none
            state.message = message
            state.warning = warning
            state.identity = hello
            state.email = snapshot.user?.email ?? ""
        }
    }

    private static let hello: CoreHello? = {
        let body = #"{"component":"asterlink-core","core_version":"0.1.0","protocol_version":1,"min_protocol_version":1,"max_protocol_version":1,"mihomo_revision":"70f0570405c3c2c47bb113b88db95006d239b346"}"#
        return try? JSONDecoder().decode(CoreHello.self, from: Data(body.utf8))
    }()

    private static func signedOut(email: String = "", remember: Bool = false, message: Notice? = nil, warning: Notice? = nil, problem: Notice? = nil) -> (inout SessionState) -> Void {
        { state in
            state.phase = .signedOut
            state.email = email
            state.rememberPassword = remember
            state.message = message
            state.warning = warning
            state.coreProblem = problem
            state.identity = hello
        }
    }

    static func make(_ name: String) -> UIFixture {
        let lines = LaunchScreen(tab: .lines)
        let account = LaunchScreen(tab: .account)
        switch name {
        // Login
        case "login": return UIFixture(apply: signedOut())
        case "login-remembered": return UIFixture(apply: signedOut(email: "vip@example.com", remember: true))
        case "login-wrong-password": return UIFixture(apply: signedOut(email: "vip@example.com", message: .core("invalid_credentials", "邮箱或密码错误")))
        case "login-offline": return UIFixture(apply: signedOut(email: "vip@example.com", message: .local(LocalCodes.networkUnreachable)))
        case "login-save-failed": return UIFixture(apply: signedOut(email: "vip@example.com", warning: .local(LocalCodes.credentialSaveFailed)))
        // Connect
        case "connect-ready": return UIFixture(apply: signedIn(ready()))
        case "connect-no-plan": return UIFixture(apply: signedIn(CoreSnapshot(generation: 5, state: "blocked", user: admin,
            entitlement: denied("upgrade_required", "当前账号没有有效套餐，请前往网站购买"), tunnelHealth: "idle", metering: MeteringView())))
        case "connect-expired": return UIFixture(apply: signedIn(CoreSnapshot(generation: 5, state: "blocked", user: user,
            entitlement: denied("subscription_expired", "套餐已于 2026-09-30 到期", plan: "VIP", total: 100 << 30, remaining: 12 << 30, expires: "2026-09-30T15:59:59Z"),
            nodes: [], catalogRevision: revision, tunnelHealth: "idle", metering: MeteringView())))
        case "connect-quota-zero": return UIFixture(apply: signedIn(CoreSnapshot(generation: 5, state: "blocked", user: user,
            entitlement: denied("quota_exhausted", "流量已用完", plan: "VIP", total: 100 << 30, remaining: 0, expires: "2030-12-31T15:59:59Z"),
            nodes: [], catalogRevision: revision, tunnelHealth: "idle", metering: MeteringView())))
        // The device limit is checked when connecting (node groups round 6): the connect is refused
        // in the Core's words, the account stays signed in and may try again.
        case "connect-device-limit": return UIFixture(apply: signedIn({ var s = ready(); s.lastError = ErrorView(code: "device_limit", message: "已达到同时在线设备上限，请先断开其他设备"); return s }(),
                                                                       message: .core("device_limit", "已达到同时在线设备上限，请先断开其他设备")))
        // The administrator withdrew this device's server: the Core reconnects by itself.
        case "connect-reassigning": return UIFixture(apply: signedIn({ var s = ready(); s.state = "preparing"; s.lastError = ErrorView(code: "connection_reassigned", message: "线路服务器已调整，正在为你重新连接"); return s }()))
        case "connect-no-line": return UIFixture(apply: signedIn(ready(selected: nil)))
        case "connect-syncing": return UIFixture(apply: signedIn(ready(selected: nil, nodes: [])))
        case "connect-preparing": return UIFixture(apply: signedIn(ready(), activity: .connecting))
        case "connect-connecting": return UIFixture(apply: signedIn({ var s = ready(); s.state = "connecting"; return s }(), activity: .connecting))
        case "connect-connected": return UIFixture(apply: connectedState(connected()))
        case "connect-connected-half": return UIFixture(apply: connectedState(connected(hk2)))
        case "connect-connected-node": return UIFixture(apply: connectedState(connected(us, source: "node_reported")))
        case "connect-gated": return UIFixture(apply: connectedState(connected(gate: GateView(reason: "lifecycle_reconfirmation_required", waitingNetwork: false, closedSeconds: 4, attempts: 1))))
        case "connect-waiting-network": return UIFixture(apply: connectedState(connected(gate: GateView(reason: "network_changed", waitingNetwork: true, closedSeconds: 42, attempts: 3))))
        case "connect-stopping": return UIFixture(apply: signedIn(connected(), activity: .disconnecting))
        case "connect-error": return UIFixture(apply: signedIn({ var s = ready(); s.state = "error"; s.lastError = ErrorView(code: "verify_failed", message: "隧道验证未通过，请稍后重试或更换线路"); return s }()))
        case "connect-update-required": return UIFixture(apply: signedIn({ var s = ready(); s.lastError = ErrorView(code: ConnectionView.updateRequired, message: "当前版本过低，请更新到最新版本后使用"); return s }()))
        case "connect-tun-unavailable": return UIFixture(apply: signedIn(ready(), message: .core("tun_unavailable", ErrorText.local("tun_unavailable"))))
        case "connect-vpn-setup": return UIFixture(apply: { state in signedIn(ready())(&state); state.needsVPNSetup = true })
        case "connect-vpn-denied": return UIFixture(apply: signedIn(ready(), message: .local(LocalCodes.vpnPermissionDenied)))
        case "connect-vpn-revoked": return UIFixture(apply: signedIn(ready(), message: .local(LocalCodes.vpnRevoked)))
        case "connect-probe-failed": return UIFixture(apply: signedIn(ready(), message: .local(LocalCodes.routeProbeFailed)))
        case "connect-ended": return UIFixture(apply: signedIn({ var s = ready(); s.lastError = ErrorView(code: "quota_exhausted", message: "流量已用完，连接已断开"); return s }(),
                                                                message: .core("quota_exhausted", "流量已用完，连接已断开")))
        // Lines
        case "lines": return UIFixture(screen: lines, apply: signedIn(ready()))
        case "lines-search-empty": return UIFixture(screen: LaunchScreen(tab: .lines, linesQuery: "不存在的线路"), apply: signedIn(ready()))
        case "lines-search-hk": return UIFixture(screen: LaunchScreen(tab: .lines, linesQuery: "香港"), apply: signedIn(ready()))
        case "lines-no-plan": return UIFixture(screen: lines, apply: signedIn(CoreSnapshot(generation: 5, state: "blocked", user: admin,
            entitlement: denied("upgrade_required", "当前账号没有有效套餐，请前往网站购买"), tunnelHealth: "idle", metering: MeteringView())))
        case "lines-none": return UIFixture(screen: lines, apply: signedIn({ var s = ready(selected: nil, nodes: []); s.catalogRevision = revision; return s }()))
        case "lines-syncing": return UIFixture(screen: lines, apply: signedIn(ready(selected: nil, nodes: [])))
        case "lines-connected": return UIFixture(screen: lines, apply: connectedState(connected()))
        // Account
        case "account": return UIFixture(screen: account, apply: signedIn(ready()))
        case "account-admin-no-plan": return UIFixture(screen: account, apply: signedIn(CoreSnapshot(generation: 5, state: "blocked", user: admin,
            entitlement: denied("upgrade_required", "当前账号没有有效套餐，请前往网站购买"), tunnelHealth: "idle", metering: MeteringView())))
        case "account-expired": return UIFixture(screen: account, apply: signedIn(CoreSnapshot(generation: 5, state: "blocked", user: user,
            entitlement: denied("subscription_expired", "套餐已于 2026-09-30 到期", plan: "VIP", total: 100 << 30, remaining: 12 << 30, expires: "2026-09-30T15:59:59Z"),
            nodes: [], catalogRevision: revision, tunnelHealth: "idle", metering: MeteringView())))
        default: return UIFixture(apply: signedOut())
        }
    }
}
#endif
