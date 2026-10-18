import Foundation
import Testing
@testable import AsterLinkKit

/// The connect loop's app-side logic with two scripted Cores (the app's and the extension's)
/// joined by the same TunnelBridge the app uses. The system VPN itself is device-only.
@MainActor
struct TunnelFlowTests {
    /// The app's own Core: generation counted on its own (here from 7), ready with line "a" chosen.
    private func appCore(state: @escaping @Sendable () -> String = { "ready" }) -> ScriptedCore {
        ScriptedCore { command in
            switch command.name {
            case "login": .snapshot(vipSnapshot(generation: 7, selected: "a"))
            case "logout": .snapshot(.empty)
            default: .snapshot(vipSnapshot(generation: command.generation == 0 ? 8 : command.generation, state: state(), selected: "a"))
            }
        }
    }

    /// The extension's Core: its own, lower generation (3), connected on line "a".
    private func extensionCore(_ snapshot: @escaping @Sendable (ScriptedCore.Command) -> CoreSnapshot) -> ScriptedCore {
        ScriptedCore { .snapshot(snapshot($0)) }
    }

    private func signedIn(_ controller: SessionController) async {
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: true)
    }

    @Test func theFirstConnectExplainsTheSystemDialogBeforeAskingForIt() async {
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore { _ in connectedSnapshot(generation: 3) }, configured: false)
        let controller = makeTunnelController(app: app, tunnel: tunnel)
        await signedIn(controller)
        await controller.connect()
        #expect(controller.state.needsVPNSetup && tunnel.connects == 0)
        await controller.continueVPNSetup()
        #expect(!controller.state.needsVPNSetup && tunnel.connects == 1)
        #expect(controller.state.snapshot.state == CoreSnapshot.State.connected)
        // Once the configuration exists the explanation is not shown again.
        await controller.disconnect()
        await controller.connect()
        #expect(!controller.state.needsVPNSetup && tunnel.connects == 2)
    }

    @Test func cancellingTheExplanationConnectsNothing() async {
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore { _ in connectedSnapshot(generation: 3) }, configured: false)
        let controller = makeTunnelController(app: app, tunnel: tunnel)
        await signedIn(controller)
        await controller.connect()
        controller.cancelVPNSetup()
        #expect(!controller.state.needsVPNSetup && tunnel.connects == 0 && controller.state.snapshot.state == "ready")
    }

    @Test func aRefusedSystemDialogKeepsTheUserReadyWithTheReason() async {
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore { _ in connectedSnapshot(generation: 3) })
        tunnel.failNextConnect(code: LocalCodes.vpnPermissionDenied)
        let controller = makeTunnelController(app: app, tunnel: tunnel)
        await signedIn(controller)
        await controller.connect()
        #expect(controller.state.message == .local(LocalCodes.vpnPermissionDenied))
        #expect(controller.state.snapshot.state == "ready" && !controller.state.busy && controller.state.activity == .none)
        #expect(ErrorText.text(for: controller.state.message!) == "需要允许 AsterLink 添加 VPN 配置才能使用加速")
    }

    @Test func theTwoCoresCountGenerationsApartAndTheControllerFollowsTheOwner() async {
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore { command in
            // After the disconnect the extension's Core would be "ready"; it is only asked while it owns the session.
            connectedSnapshot(generation: command.name == "disconnect" ? 4 : 3, upload: 5, download: 7)
        })
        let controller = makeTunnelController(app: app, tunnel: tunnel)
        await signedIn(controller)
        #expect(controller.state.snapshot.generation == 7)
        await controller.connect()
        // The extension's generation (3) is lower than the app's (7) and still wins: it is a new owner.
        #expect(controller.state.snapshot.generation == 3 && controller.state.snapshot.state == CoreSnapshot.State.connected)
        await controller.poll()
        #expect(controller.state.snapshot.generation == 3)
        await controller.disconnect()
        // Back in the app's Core (generation 8 now), whose snapshot is shown.
        #expect(controller.state.snapshot.generation == 8 && controller.state.snapshot.state == "ready")
        #expect(tunnel.bridge.currentRoute == .app && tunnel.stops == 1)
    }

    @Test func aTunnelTheSystemTookAwayIsReportedOnceAndTheSessionIsTakenBack() async {
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore { _ in connectedSnapshot(generation: 3) })
        let alerts = RecordingAlerts()
        let controller = makeTunnelController(app: app, tunnel: tunnel, alerts: alerts)
        await signedIn(controller)
        await controller.connect()
        tunnel.channel.setDown(true)
        await controller.poll()
        #expect(alerts.notices == [.local(LocalCodes.vpnRevoked)])
        #expect(controller.state.message == .local(LocalCodes.vpnRevoked))
        #expect(tunnel.bridge.currentRoute == .app && controller.state.snapshot.state == "ready")
        #expect(controller.state.phase == .signedIn)
        await controller.poll()
        #expect(alerts.notices.count == 1)
    }

    @Test func aSessionTheServerNoLongerHonoursSignsInAgainSilently() async {
        // The extension revoked the session when the system stopped it: the app's Core stays
        // "released" until the saved password signs in again.
        let phase = StepCounter()
        let lost = Flag()
        let app = ScriptedCore { command in
            switch command.name {
            case "login": .snapshot(vipSnapshot(generation: phase.bump() == 1 ? 7 : 9, selected: "a"))
            case "logout": .snapshot(.empty)
            default: .snapshot(phase.value == 1 && lost.value ? CoreSnapshot(generation: 8, state: CoreSnapshot.State.released) : vipSnapshot(generation: 9, selected: "a"))
            }
        }
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore { _ in connectedSnapshot(generation: 3) })
        let alerts = RecordingAlerts()
        let credentials = MemoryCredentials()
        let controller = makeTunnelController(app: app, tunnel: tunnel, credentials: credentials, alerts: alerts)
        await signedIn(controller)
        await controller.connect()
        lost.set()
        tunnel.channel.setDown(true)
        await controller.poll()
        // The released Core is noticed, the password saved earlier signs in again, nothing is shown as lost.
        for _ in 0..<50 where app.count("login") < 2 { try? await Task.sleep(for: .milliseconds(20)) }
        #expect(app.count("login") == 2)
        #expect(controller.state.phase == .signedIn && controller.state.snapshot.generation == 9)
        #expect(alerts.notices == [.local(LocalCodes.vpnRevoked)])
    }

    @Test func aTunnelTheCoreEndedByItselfAlertsOnceAndHandsTheSessionBack() async {
        let ended = Flag()
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore { _ in
            if ended.value {
                var snapshot = vipSnapshot(generation: 3, state: CoreSnapshot.State.blocked, selected: "a")
                snapshot.lastError = ErrorView(code: "quota_exhausted", message: "流量已用完")
                return snapshot
            }
            return connectedSnapshot(generation: 3)
        })
        let alerts = RecordingAlerts()
        let controller = makeTunnelController(app: app, tunnel: tunnel, alerts: alerts)
        await signedIn(controller)
        await controller.connect()
        ended.set()
        await controller.poll()
        #expect(alerts.notices == [.core("quota_exhausted", "流量已用完")])
        for _ in 0..<50 where tunnel.bridge.currentRoute != .app { try? await Task.sleep(for: .milliseconds(20)) }
        #expect(tunnel.bridge.currentRoute == .app && tunnel.stops == 1)
        await controller.poll()
        #expect(controller.state.snapshot.state == "ready" && alerts.notices.count == 1)
    }

    @Test func aFailedConnectLeavesNoTunnelAndNoStaleBusyState() async {
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore { _ in connectedSnapshot(generation: 3) })
        tunnel.failNextConnect(code: LocalCodes.routeProbeFailed)
        let controller = makeTunnelController(app: app, tunnel: tunnel)
        await signedIn(controller)
        await controller.connect()
        #expect(controller.state.message == .local(LocalCodes.routeProbeFailed))
        #expect(!controller.state.busy && controller.state.snapshot.state == "ready")
    }

    @Test func theConnectionsUsageStartsAtTheTunnelAndBillsAtItsOwnRate() async {
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore { _ in
            connectedSnapshot(generation: 3, rate: 500, upload: 20 << 20, download: 80 << 20)
        })
        let controller = makeTunnelController(app: app, tunnel: tunnel)
        await signedIn(controller)
        await controller.connect()
        // A new tunnel is a new count: the 100 MiB the extension reports at connect are not "this connection".
        let usage = controller.state.usage
        #expect(usage?.physicalBytes == 0)
    }
}

final class StepCounter: @unchecked Sendable {
    private let lock = NSLock()
    private var count = 0
    var value: Int { lock.withLock { count } }
    func bump() -> Int { lock.withLock { count += 1; return count } }
}

final class Flag: @unchecked Sendable {
    private let lock = NSLock()
    private var on = false
    var value: Bool { lock.withLock { on } }
    func set() { lock.withLock { on = true } }
}

struct TunnelBridgeTests {
    private func request(_ command: String, generation: UInt64 = 5) -> String {
        CoreRequest.command(command, requestID: "r-1", generation: generation)
    }

    @Test func requestsGoToTheCoreThatOwnsTheSession() async throws {
        let app = ScriptedCore { _ in .snapshot(vipSnapshot(generation: 1)) }
        let tunnelCore = ScriptedCore { _ in .snapshot(connectedSnapshot(generation: 2)) }
        let bridge = TunnelBridge(local: app, timeout: 5)
        _ = bridge.call(request("read_snapshot"))
        #expect(app.count("read_snapshot") == 1)
        bridge.useTunnel(CoreChannel(tunnelCore))
        _ = bridge.call(request("read_snapshot"))
        #expect(tunnelCore.count("read_snapshot") == 1 && app.count("read_snapshot") == 1)
        bridge.useApp()
        _ = bridge.call(request("read_snapshot"))
        #expect(app.count("read_snapshot") == 2)
    }

    @Test func anUnreachableExtensionAnswersWithAStableCodeThatEchoesTheRequest() throws {
        let channel = CoreChannel(ScriptedCore { _ in .snapshot(.empty) })
        channel.setDown(true)
        let bridge = TunnelBridge(local: ScriptedCore { _ in .snapshot(.empty) }, timeout: 5)
        bridge.useTunnel(channel)
        let raw = bridge.call(request("read_snapshot", generation: 9))
        let response = try JSONDecoder().decode(CoreResponse.self, from: Data(raw.utf8))
        #expect(!response.ok && response.error?.code == LocalCodes.tunnelUnreachable)
        #expect(response.requestID == "r-1" && response.generation == 9)
    }

    @Test func aStuckExtensionTimesOutInsteadOfBlockingForever() {
        struct Stuck: ExtensionChannel {
            func send(_ request: String, completion: @escaping @Sendable (String?) -> Void) {}
        }
        let bridge = TunnelBridge(local: ScriptedCore { _ in .snapshot(.empty) }, timeout: 0.2)
        bridge.useTunnel(Stuck())
        let started = Date()
        let raw = bridge.call(request("read_snapshot"))
        #expect(raw.contains(LocalCodes.tunnelUnreachable) && Date().timeIntervalSince(started) < 5)
    }

    @Test func theClientReadsThroughTheBridgeLikeAnyCore() async throws {
        let tunnelCore = ScriptedCore { _ in .snapshot(connectedSnapshot(generation: 2)) }
        let bridge = TunnelBridge(local: ScriptedCore { _ in .snapshot(.empty) }, timeout: 5)
        bridge.useTunnel(CoreChannel(tunnelCore))
        let snapshot = try await CoreClient(transport: bridge).readSnapshot()
        #expect(snapshot.state == CoreSnapshot.State.connected && snapshot.generation == 2)
    }
}

struct TunnelWatchTests {
    @Test func aConnectedOrSettlingTunnelIsNotNews() {
        for state in [CoreSnapshot.State.connected, CoreSnapshot.State.connecting, CoreSnapshot.State.preparing, CoreSnapshot.State.stopping, CoreSnapshot.State.released, CoreSnapshot.State.authenticating] {
            #expect(TunnelWatch.evaluate(vipSnapshot(state: state)) == .running, "\(state)")
        }
    }

    @Test func aTunnelThatEndedNamesTheCoresReason() {
        var snapshot = vipSnapshot(state: CoreSnapshot.State.blocked)
        snapshot.lastError = ErrorView(code: "quota_exhausted", message: "流量已用完")
        #expect(TunnelWatch.evaluate(snapshot) == .ended(TunnelEnd(notice: .core("quota_exhausted", "流量已用完"))))
    }

    @Test func theEntitlementVerdictExplainsAnEndWithoutAnError() {
        var snapshot = vipSnapshot(state: "ready")
        snapshot.entitlement = EntitlementView(planName: "VIP", accountAuthorized: false, reason: "subscription_expired", reasonMessage: "套餐已到期")
        guard case .ended(let end) = TunnelWatch.evaluate(snapshot) else { Issue.record("expected an end"); return }
        #expect(end.text == "套餐已到期")
    }

    @Test func aSignedOutCoreAndAPlainStopGetFixedSentences() {
        guard case .ended(let signedOut) = TunnelWatch.evaluate(.empty), case .ended(let plain) = TunnelWatch.evaluate(vipSnapshot(state: "ready")) else {
            Issue.record("expected ends"); return
        }
        #expect(signedOut.text == ErrorText.local(LocalCodes.sessionEnded) && plain.text == ErrorText.local(LocalCodes.tunnelEnded))
    }

    @Test func aResponseDocumentIsReadAndGarbageIsKeptWatching() {
        let core = ScriptedCore { _ in .snapshot(connectedSnapshot(generation: 2)) }
        #expect(TunnelWatch.evaluate(response: core.call(CoreRequest.command("read_snapshot", requestID: "w"))) == .running)
        #expect(TunnelWatch.evaluate(response: "not json") == .unreadable)
        #expect(TunnelWatch.evaluate(response: #"{"request_id":"w","protocol_version":1,"generation":0,"ok":false,"error":{"code":"core_not_started","message":""}}"#) == .unreadable)
    }
}

struct MeteringArithmeticTests {
    /// B01 by formula (handoff_app_ios.md 9): 20 MiB up + 80 MiB down = 104,857,600 bytes.
    @Test func oneToOneAndHalfRateChargeExactUnits() {
        let physical: Int64 = (20 + 80) << 20
        #expect(physical == 104_857_600)
        #expect(Formatting.chargedUnits(physical: physical, ratePermille: 1000) == 104_857_600_000)
        #expect(Formatting.chargedUnits(physical: physical, ratePermille: 500) == 52_428_800_000)
        #expect(Formatting.billedBytes(physical: physical, ratePermille: 500) == 52_428_800)
    }

    @Test func aConnectionsUnitsComeFromItsOwnCountersAndRate() throws {
        let before = vipSnapshot()
        var connected = connectedSnapshot(generation: 3, rate: 500, upload: 20 << 20, download: 80 << 20)
        let baseline = ConnectionUsage.baseline(before: before, connected: connected)
        connected.metering?.uploadBytes += 1_000
        let usage = try #require(ConnectionUsage.of(connected, baseline: baseline))
        #expect(usage.physicalBytes == 1_000 && usage.chargedUnits == 500_000)
    }

    @Test func aCorruptCounterSaturatesInsteadOfWrappingIntoARefund() {
        #expect(Formatting.chargedUnits(physical: Int64.max, ratePermille: 1000) == Int64.max)
        #expect(Formatting.chargedUnits(physical: -5, ratePermille: 1000) == 0)
        #expect(Formatting.chargedUnits(physical: 5, ratePermille: -1) == 0)
    }
}
