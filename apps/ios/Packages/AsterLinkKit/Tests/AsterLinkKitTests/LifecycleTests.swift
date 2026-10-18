import Foundation
import Testing
@testable import AsterLinkKit

/// Round 5 (S-L01): the app-side lifecycle rules. The system events themselves (sleep, a
/// competing VPN, the Settings switch, jetsam) are device-only; these tests fix what each one means.
struct ForegroundLeaseTests {
    @Test func aHintKeepsTheForegroundAndItsLapseDropsIt() {
        var lease = ForegroundLease(startedAt: 0)
        // The Core starts in the foreground, so a first "visible" is no change.
        #expect(lease.hint(visible: true, at: 1) == nil)
        #expect(lease.tick(at: 20) == nil)
        // Renewed at 25: still valid at 54, gone at 55.
        #expect(lease.hint(visible: true, at: 25) == nil)
        #expect(lease.tick(at: 54) == nil)
        #expect(lease.tick(at: 55) == false)
        // Told once, not on every tick.
        #expect(lease.tick(at: 60) == nil)
        #expect(lease.hint(visible: true, at: 61) == true)
    }

    @Test func aLeftAppIsBackgroundAtOnce() {
        var lease = ForegroundLease(startedAt: 0)
        #expect(lease.hint(visible: false, at: 2) == false)
        #expect(lease.hint(visible: false, at: 3) == nil)
        #expect(lease.tick(at: 1000) == nil)
    }

    @Test func theHeartbeatRenewsWellInsideTheLease() {
        #expect(ForegroundLease.ttl >= 3 * 10)
        #expect(ForegroundLease.heartbeat == .seconds(10))
    }
}

struct TunnelStopReasonTests {
    @Test func theRawValuesAreTheSystemsOwn() {
        // NEProviderStopReason: none 0, userInitiated 1, providerFailed 2, ... superceded 11, ... appUpdate 16.
        #expect(TunnelStopReason(raw: 11).name == "superceded")
        #expect(TunnelStopReason(raw: 16).name == "appUpdate")
        #expect(TunnelStopReason(raw: 15).name == "sleep")
        #expect(TunnelStopReason(raw: 99).name == "unknown99")
    }

    @Test func eachSystemStopGetsItsOwnSentenceExceptTheUsersOwnSwitch() {
        #expect(TunnelStopReason(raw: 0).notice == nil)
        #expect(TunnelStopReason(raw: 1).notice == nil)
        #expect(TunnelStopReason(raw: 11).notice == .local(LocalCodes.vpnSuperseded))
        for raw in [5, 9, 10] { #expect(TunnelStopReason(raw: raw).notice == .local(LocalCodes.vpnRevoked)) }
        #expect(TunnelStopReason(raw: 12).notice == .local(LocalCodes.vpnUserSwitch))
        #expect(TunnelStopReason(raw: 16).notice == .local(LocalCodes.vpnAppUpdate))
        #expect(TunnelStopReason(raw: 2).notice == .local(LocalCodes.vpnSystemStopped))
        for raw in 2...16 {
            let notice = TunnelStopReason(raw: raw).notice!
            #expect(ErrorText.text(for: notice) != ErrorText.local("unmapped-code"), "reason \(raw) falls back to the generic text")
        }
    }
}

struct GateLogTests {
    private func gated(reason: String = "wake") -> CoreSnapshot {
        var snapshot = connectedSnapshot(generation: 3)
        snapshot.gate = GateView(reason: reason, waitingNetwork: false, closedSeconds: 2, attempts: 1)
        return snapshot
    }

    @Test func aClosureIsLoggedOnceWithItsReasonAndOnceMoreWhenItReopens() {
        var log = GateLog()
        #expect(log.observe(connectedSnapshot(generation: 3)).isEmpty)
        #expect(log.observe(gated()) == ["gate closed reason=wake"])
        #expect(log.observe(gated()).isEmpty)
        var open = connectedSnapshot(generation: 3)
        open.lastGate = GateRecord(reason: "wake", outcome: "reopened", closedMillis: 4200, attempts: 2, totalClosures: 1)
        #expect(log.observe(open) == ["gate reopened reason=wake outcome=reopened closed_ms=4200 attempts=2 total=1"])
        #expect(log.observe(open).isEmpty)
    }

    @Test func aSecondClosureIsANewEvent() {
        var log = GateLog()
        var first = connectedSnapshot(generation: 3)
        first.lastGate = GateRecord(reason: "path", outcome: "reopened", closedMillis: 100, attempts: 1, totalClosures: 1)
        _ = log.observe(first)
        var second = first
        second.lastGate = GateRecord(reason: "wake", outcome: "reopened", closedMillis: 900, attempts: 1, totalClosures: 2)
        #expect(log.observe(second).count == 1)
    }
}

struct ResourceSampleTests {
    @Test func aLogLineReadsBackExactly() {
        let sample = ResourceSample(uptimeSeconds: 600, footprint: 31 << 20, peak: 36 << 20, openFiles: 41, threads: 19,
                                    goroutines: 88, goHeap: 12 << 20, goSys: 25 << 20, gcCount: 140)
        #expect(ResourceSample.parse("2026-10-18 12:00:00 asterlink.tunnel \(sample.logLine)") == sample)
        #expect(ResourceSample.parse("stop: reason none") == nil)
        #expect(ResourceSample.parse("sample t=1 footprint=2") == nil)
    }

    @Test func aCaptureCountsThisProcess() {
        let stats = #"{"goroutines":12,"heap_in_use":1000,"sys":2000,"num_gc":3}"#
        let sample = ResourceSample.capture(uptime: 5, footprint: 100, peak: 120, stats: stats)
        #expect(sample.goroutines == 12 && sample.goHeap == 1000 && sample.goSys == 2000 && sample.gcCount == 3)
        #expect(sample.openFiles >= 3 && sample.threads >= 1)
        #expect(ResourceSample.capture(uptime: 0, footprint: 0, peak: 0, stats: "not json").goroutines == 0)
    }
}

/// D-L05 and the foreground lease, through the controller with a scripted extension.
@MainActor
struct LifecycleFlowTests {
    private func appCore() -> ScriptedCore {
        // The app's own Core after a relaunch: signed out, and any login here would be a second session.
        ScriptedCore { command in
            command.name == "login" ? .snapshot(vipSnapshot(generation: 1, selected: "a")) : .snapshot(.empty)
        }
    }

    private func extensionCore() -> ScriptedCore {
        ScriptedCore { _ in .snapshot(connectedSnapshot(generation: 5, upload: 10, download: 20)) }
    }

    @Test func aRelaunchedAppRejoinsTheTunnelWithoutLoggingInAgain() async {
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore())
        tunnel.resumable = true
        // A remembered password must not be used: the session is the extension's.
        let credentials = MemoryCredentials()
        let preferences = MemoryPreferences("vip@example.invalid")
        let controller = makeTunnelController(app: app, tunnel: tunnel, credentials: credentials, preferences: preferences)
        await controller.launch()
        #expect(controller.state.phase == .signedIn)
        #expect(controller.state.snapshot.state == CoreSnapshot.State.connected && controller.state.snapshot.generation == 5)
        #expect(controller.state.email == "vip@example.invalid")
        #expect(app.count("login") == 0)
        #expect(tunnel.bridge.currentRoute == .tunnel)
        #expect(tunnel.foregrounds.first == true)
        // The usage counters of the whole sign-in are the baseline of this view, not "this connection".
        #expect(controller.state.connectedSinceMillis != nil)
    }

    @Test func aRejoinedTunnelStillDisconnectsAndHandsTheSessionBack() async {
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore())
        tunnel.resumable = true
        let controller = makeTunnelController(app: app, tunnel: tunnel)
        await controller.launch()
        await controller.disconnect()
        #expect(tunnel.stops == 1 && tunnel.bridge.currentRoute == .app)
    }

    @Test func aTunnelWithoutAConnectedSessionIsClosedAndTheNormalLaunchContinues() async {
        let app = appCore()
        let idle = ScriptedCore { _ in .snapshot(vipSnapshot(generation: 2, state: "released")) }
        let tunnel = FakeTunnel(app: app, tunnelCore: idle)
        tunnel.resumable = true
        let controller = makeTunnelController(app: app, tunnel: tunnel)
        await controller.launch()
        #expect(controller.state.phase == .signedOut)
        #expect(tunnel.stops == 1 && tunnel.bridge.currentRoute == .app)
    }

    @Test func noRunningTunnelMeansTheOrdinaryLaunch() async {
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore())
        let credentials = MemoryCredentials()
        let controller = makeTunnelController(app: app, tunnel: tunnel, credentials: credentials, preferences: MemoryPreferences("vip@example.invalid"))
        await controller.launch()
        #expect(controller.state.phase == .signedOut && tunnel.stops == 0)
    }

    @Test func theForegroundHintIsRepeatedWhileVisibleAndStopsWhenTheAppLeaves() async throws {
        let app = appCore()
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore())
        let controller = makeTunnelController(app: app, tunnel: tunnel, heartbeat: .milliseconds(20))
        await controller.setForeground(true)
        try await Task.sleep(for: .milliseconds(200))
        #expect(tunnel.foregrounds.filter { $0 }.count >= 4)
        await controller.setForeground(false)
        let afterLeaving = tunnel.foregrounds.count
        try await Task.sleep(for: .milliseconds(120))
        #expect(tunnel.foregrounds.count == afterLeaving && tunnel.foregrounds.last == false)
    }

    @Test func refusedNotificationsAreSaidUpFrontOnceConnected() async {
        let app = ScriptedCore { command in
            command.name == "login" ? .snapshot(vipSnapshot(generation: 7, selected: "a")) : .snapshot(vipSnapshot(generation: 7, selected: "a"))
        }
        let tunnel = FakeTunnel(app: app, tunnelCore: extensionCore())
        tunnel.notifications = false
        let controller = makeTunnelController(app: app, tunnel: tunnel)
        await controller.signIn(email: "vip@example.invalid", password: "secret-pass", remember: false)
        await controller.connect()
        #expect(controller.state.warning == .local(LocalCodes.notificationsOff))
        #expect(controller.state.snapshot.state == CoreSnapshot.State.connected)
    }
}
