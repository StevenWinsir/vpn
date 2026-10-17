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

    static let errorDomain = "com.asterlink.tunnel"

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
        log.notice("stop: reason \(reason.rawValue, privacy: .public)")
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
        completionHandler()
    }

    override func wake() {
        // The Core's clock watch sees the sleep; this makes a closed gate retry at once.
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

        let adopted = MobileAdoptSession(ticket)
        guard let head = Self.snapshot(adopted), head.actionError == nil, head.state == "ready" else {
            let failure = Self.failureFrom(adopted) ?? Self.failure("handoff_invalid", "会话交接失败")
            stopCore()
            return .failure(failure)
        }
        let connected = MobileCall(CoreRequest.command("connect", requestID: "tunnel-connect", generation: head.generation))
        if let after = Self.snapshot(connected), after.actionError == nil, after.state == "connected" {
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
            return Data(MobileCall(request).utf8)
        case .release:
            let exported = MobileExportSession()
            guard let response = try? JSONDecoder().decode(CoreResponse.self, from: Data(exported.utf8)) else {
                return ProviderReply.invalidRequest.encoded()
            }
            if let ticket = try? response.decodeResult(ExportedTicket.self) {
                return ProviderReply.released(ticket: ticket.ticket).encoded()
            }
            return ProviderReply.error(code: response.error?.code ?? "handoff_unsettled").encoded()
        case .foreground(let visible):
            MobileSetForeground(visible)
            return ProviderReply.ack.encoded()
        case .routeProbe(let ok):
            log.notice("route probe from the app: \(ok, privacy: .public)")
            return ProviderReply.ack.encoded()
        }
    }

    private func startMemoryWatch() {
        let timer = DispatchSource.makeTimerSource(queue: work)
        timer.schedule(deadline: .now() + 5, repeating: 5)
        timer.setEventHandler { [self] in
            guard let footprint = MemoryBudget.footprint() else { return }
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
