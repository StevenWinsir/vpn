import AsterCore
import AsterLinkKit
import Foundation
import NetworkExtension
import os

/// The AsterLink packet tunnel (handoff_app_ios.md 4, 8). It only ever runs a session the app
/// handed it: startTunnel without a ticket (Settings, On Demand) is refused. With a ticket the
/// extension's Core adopts the session, connects (authorization, first metering acknowledgement,
/// tunnel settings, utun, Verify stage one) and only then reports the tunnel up. While it runs,
/// the extension is the session's only writer; the app talks to its Core through provider
/// messages. Stopping settles the tail; a stop the app did not ask for (Settings, another VPN,
/// the system) also revokes the session, so the app signs in again online.
final class PacketTunnelProvider: NEPacketTunnelProvider, @unchecked Sendable {
    private let log = Logger(subsystem: "asterlink.tunnel", category: "provider")
    /// Every Core call runs here, one at a time, off the provider's own queue.
    private let work = DispatchQueue(label: "asterlink.tunnel.core")
    private let monitor = PathMonitor()
    private var memory: DispatchSourceTimer?
    private var budget = MemoryBudget()
    private var coreStarted = false
    /// Ends the tunnel when the Core stops it by itself (quota, plan, line); see startEndWatch.
    private var endWatch: DispatchSourceTimer?
    /// The app asked for this stop (disconnect, release, logout) or the system did: no alert.
    private var stopRequested = false
    private var endReported = false
    /// The app's foreground hint as a lease (see ForegroundLease): a killed app stops renewing it.
    private var lease = ForegroundLease(startedAt: ProcessInfo.processInfo.systemUptime)
    private var gateLog = GateLog()
    private var startedAt = ProcessInfo.processInfo.systemUptime
    private var sampleTick = 0

    static let errorDomain = PacketTunnelErrors.domain

    /// A start failure the app can read back (fetchLastDisconnectError): a stable code, the
    /// Chinese message and nothing else.
    static func failure(_ code: String, _ message: String) -> NSError {
        NSError(domain: errorDomain, code: 1, userInfo: [NSLocalizedDescriptionKey: message, "code": code])
    }

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        let done = UncheckedBox(completionHandler)
        guard let ticket = options?[TunnelStartOptions.handoffKey] as? String, !ticket.isEmpty else {
            log.notice("start refused: no handoff ticket")
            done.value(Self.failure("handoff_required", "请在 AsterLink App 内连接"))
            return
        }
        work.async { [self] in
            let result = start(ticket: ticket)
            if case .failure(let error) = result {
                log.notice("start failed: \(error.userInfo["code"] as? String ?? "?", privacy: .public)")
            }
            done.value(result.error)
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        let done = UncheckedBox(completionHandler)
        let why = TunnelStopReason(raw: reason.rawValue)
        log.notice("stop: reason \(why.name, privacy: .public)")
        // A stop nobody in the app asked for (another VPN took over, the configuration was
        // disabled or removed, the user switched, the app was updated): the data path closes and
        // the tail settles below; say why now, because the app may be suspended or gone.
        if !stopRequested, !endReported, let notice = why.notice {
            endReported = true
            TunnelNotifier.post(TunnelEnd(notice: notice))
        }
        stopRequested = true
        // MobileStop is safe from any thread and interrupts a connect still running on the work
        // queue (a cancelled start), so the stop never waits out the 90-second connect budget.
        DispatchQueue.global(qos: .userInitiated).async { [self] in
            MobileStop()
            work.async { [self] in
                stopCore()
                done.value()
            }
        }
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        let done = UncheckedBox(completionHandler)
        work.async { [self] in
            done.value?(answer(messageData))
        }
    }

    override func sleep(completionHandler: @escaping () -> Void) {
        log.notice("sleep")
        completionHandler()
    }

    override func wake() {
        log.notice("wake")
        // The Core's clock watch sees the sleep and closes the gate itself; this makes a closed
        // gate re-verify at once instead of after its backoff.
        work.async { MobileNetworkChanged() }
    }

    // MARK: - Core

    private func start(ticket: String) -> Result<Void, NSError> {
        monitor.onChange = { MobileNetworkChanged() }
        monitor.start()
        let platform = TunnelPlatform(provider: self, monitor: monitor)
        do {
            try startCore(platform)
        } catch {
            return .failure(Self.failure("tun_unavailable", "隧道组件无法启动"))
        }
        MobileTuneMemory(MemoryBudget.goHeapLimit, MemoryBudget.goGCPercent)
        startMemoryWatch()
        stopRequested = false; endReported = false
        lease = ForegroundLease(startedAt: ProcessInfo.processInfo.systemUptime)
        gateLog = GateLog()
        startedAt = ProcessInfo.processInfo.systemUptime
        sampleTick = 0

        let adopted = MobileAdoptSession(ticket)
        guard let head = Self.snapshot(adopted), head.actionError == nil, head.state == "ready" else {
            let failure = Self.failureFrom(adopted) ?? Self.failure("handoff_invalid", "会话交接失败")
            stopCore()
            return .failure(failure)
        }
        let connected = MobileCall(CoreRequest.command("connect", requestID: "tunnel-connect", generation: head.generation))
        if let after = Self.snapshot(connected), after.actionError == nil, after.state == "connected" {
            startEndWatch()
            return .success(())
        }
        let failure = Self.failureFrom(connected) ?? Self.failure("tunnel_failed", "隧道启动失败")
        // The session was never forwarding here: give it up without revoking it, so the app
        // can take back the ticket it still holds.
        _ = MobileExportSession()
        stopCore()
        return .failure(failure)
    }

    private func startCore(_ platform: TunnelPlatform) throws {
        let cache = try FileManager.default.url(for: .cachesDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
            .appendingPathComponent("core", isDirectory: true)
        try FileManager.default.createDirectory(at: cache, withIntermediateDirectories: true)
        let build = try JSONSerialization.data(withJSONObject: ["cache_dir": cache.standardizedFileURL.path])
        var error: NSError?
        guard MobileStart(platform, String(decoding: build, as: UTF8.self), &error) else {
            throw error ?? Self.failure("tun_unavailable", "隧道组件无法启动")
        }
        coreStarted = true
    }

    /// Settles and revokes whatever session the Core still holds (none after a release), then
    /// ends the Core. Bounded by the Core (10 s).
    private func stopCore() {
        memory?.cancel()
        memory = nil
        endWatch?.cancel()
        endWatch = nil
        monitor.cancel()
        if coreStarted {
            MobileStop()
            coreStarted = false
        }
        log.notice("stopped; memory peak \(self.budget.peak >> 20, privacy: .public) MiB")
    }

    private func answer(_ data: Data) -> Data {
        guard let message = try? ProviderMessage.decode(data) else { return ProviderReply.invalidRequest.encoded() }
        switch message {
        case .ipc(let request):
            if Self.endsTheTunnel(request) { stopRequested = true }
            return Data(MobileCall(request).utf8)
        case .release:
            stopRequested = true
            let exported = MobileExportSession()
            guard let response = try? JSONDecoder().decode(CoreResponse.self, from: Data(exported.utf8)) else {
                return ProviderReply.invalidRequest.encoded()
            }
            if let ticket = try? response.decodeResult(ExportedTicket.self) {
                return ProviderReply.released(ticket: ticket.ticket).encoded()
            }
            return ProviderReply.error(code: response.error?.code ?? "handoff_unsettled").encoded()
        case .foreground(let visible):
            if let applied = lease.hint(visible: visible, at: ProcessInfo.processInfo.systemUptime) { MobileSetForeground(applied) }
            return ProviderReply.ack.encoded()
        case .routeProbe(let ok):
            log.notice("route probe from the app: \(ok, privacy: .public)")
            return ProviderReply.ack.encoded()
        }
    }

    /// The Core can end the data path by itself while the app is suspended (quota used up, plan
    /// ended, line disabled or changed, lease lost). The utun would then stay up and carry nothing,
    /// so this watches the Core's state and, once, tells the user with a local notification and
    /// closes the tunnel with the reason (the app reads it back when it next opens). A stop the
    /// app asked for never alerts.
    private func startEndWatch() {
        let timer = DispatchSource.makeTimerSource(queue: work)
        timer.schedule(deadline: .now() + 5, repeating: 5)
        timer.setEventHandler { [self] in
            guard !stopRequested, !endReported else { return }
            let raw = MobileCall(CoreRequest.command("read_snapshot", requestID: "tunnel-watch"))
            if let document = try? JSONDecoder().decode(CoreResponse.self, from: Data(raw.utf8)),
               let snapshot = try? document.decodeResult(CoreSnapshot.self) {
                for line in gateLog.observe(snapshot) { log.notice("\(line, privacy: .public)") }
            }
            guard case .ended(let end) = TunnelWatch.evaluate(response: raw) else { return }
            endReported = true
            log.notice("tunnel ended by the core: \(end.notice.code, privacy: .public)")
            TunnelNotifier.post(end)
            cancelTunnelWithError(Self.failure(end.notice.code, end.text))
        }
        timer.resume()
        endWatch = timer
    }

    /// An ipc-v1 request that ends the tunnel on purpose.
    private static func endsTheTunnel(_ request: String) -> Bool {
        guard let object = try? JSONSerialization.jsonObject(with: Data(request.utf8)) as? [String: Any],
              let command = object["command"] as? String else { return false }
        return command == "disconnect" || command == "logout"
    }

    private func startMemoryWatch() {
        let timer = DispatchSource.makeTimerSource(queue: work)
        timer.schedule(deadline: .now() + 5, repeating: 5)
        timer.setEventHandler { [self] in
            // A hint that stopped being renewed means the app is gone or suspended.
            if let applied = lease.tick(at: ProcessInfo.processInfo.systemUptime) {
                MobileSetForeground(applied)
                log.notice("foreground lease expired")
            }
            guard let footprint = MemoryBudget.footprint() else { return }
            sampleTick += 1
            if sampleTick % 12 == 0 {
                let sample = ResourceSample.capture(uptime: Int(ProcessInfo.processInfo.systemUptime - startedAt), footprint: footprint,
                                                    peak: max(budget.peak, footprint), stats: MobileRuntimeStats())
                log.notice("\(sample.logLine, privacy: .public)")
            }
            switch budget.observe(footprint) {
            case .none: break
            case .release: MobileFreeMemory()
            case .pressure:
                MobileFreeMemory()
                log.warning("memory pressure: \(footprint >> 20, privacy: .public) MiB")
            }
        }
        timer.resume()
        memory = timer
    }

    // MARK: - Decoding

    private static func snapshot(_ raw: String) -> SnapshotHead? {
        guard let response = try? JSONDecoder().decode(CoreResponse.self, from: Data(raw.utf8)) else { return nil }
        return try? response.decodeResult(SnapshotHead.self)
    }

    private static func failureFrom(_ raw: String) -> NSError? {
        guard let response = try? JSONDecoder().decode(CoreResponse.self, from: Data(raw.utf8)) else { return nil }
        if let refused = response.error { return Self.failure(refused.code, refused.message) }
        if let head = try? response.decodeResult(SnapshotHead.self), let action = head.actionError ?? head.lastError {
            return Self.failure(action.code, action.message)
        }
        return nil
    }
}

/// Carries a non-Sendable system callback across the provider's queues; each is called once.
struct UncheckedBox<Value>: @unchecked Sendable {
    let value: Value
    init(_ value: Value) { self.value = value }
}

private extension Result where Failure == NSError {
    var error: NSError? {
        if case .failure(let error) = self { return error }
        return nil
    }
}
