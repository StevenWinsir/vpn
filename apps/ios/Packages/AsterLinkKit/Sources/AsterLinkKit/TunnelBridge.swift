import Foundation

/// The app's way to reach the Core that currently owns the session (handoff_app_ios.md 4.1, 4.3).
/// The extension, while it runs.
public protocol ExtensionChannel: Sendable {
    /// One ipc-v1 request document to the extension's Core; the answer is the ipc-v1 response document.
    func send(_ request: String) async throws -> String
}

/// Routes every ipc-v1 request to the process that owns the session: the app's own Core while no
/// VPN runs, the extension's Core while one does (the CoreRouter of handoff_app_ios.md 8.6). The
/// SessionController only ever sees a `CoreTransport`, so the same screens work in both modes.
///
/// A request that cannot reach the extension (it was stopped by the system, by another VPN or by
/// the user in Settings) is answered with the stable code `tunnel_unreachable`; the controller then
/// takes the session back (or signs in again) instead of showing a stale tunnel.
public final class TunnelBridge: CoreTransport, @unchecked Sendable {
    public enum Route: Sendable, Equatable { case app, tunnel }

    public static let unreachableCode = "tunnel_unreachable"

    private let local: any CoreTransport
    private let timeout: TimeInterval
    private let lock = NSLock()
    private var route = Route.app
    private var channel: (any ExtensionChannel)?

    /// `timeout` bounds one routed request (a disconnect settles the tail: seconds on a slow network).
    public init(local: any CoreTransport, timeout: TimeInterval = 60) {
        self.local = local; self.timeout = timeout
    }

    public var currentRoute: Route { lock.withLock { route } }

    /// From now on requests go to the extension.
    public func useTunnel(_ channel: any ExtensionChannel) {
        lock.withLock { route = .tunnel; self.channel = channel }
    }

    /// From now on requests go to the app's own Core.
    public func useApp() {
        lock.withLock { route = .app; channel = nil }
    }

    public func call(_ request: String) -> String {
        let (current, channel) = lock.withLock { (route, self.channel) }
        guard current == .tunnel, let channel else { return local.call(request) }
        let answer = Answer()
        let finished = DispatchSemaphore(value: 0)
        Task.detached {
            answer.set(try? await channel.send(request))
            finished.signal()
        }
        if finished.wait(timeout: .now() + timeout) == .success, let text = answer.value { return text }
        return Self.unreachable(for: request)
    }

    /// An ipc-v1 refusal that echoes the request's id and generation, as the Core would.
    static func unreachable(for request: String) -> String {
        let object = (try? JSONSerialization.jsonObject(with: Data(request.utf8))) as? [String: Any] ?? [:]
        let response: [String: Any] = [
            "request_id": object["request_id"] as? String ?? "",
            "protocol_version": CoreRequest.protocolVersion,
            "generation": (object["generation"] as? NSNumber)?.uint64Value ?? 0,
            "ok": false,
            "error": ["code": unreachableCode, "message": ""],
        ]
        return String(decoding: (try? JSONSerialization.data(withJSONObject: response)) ?? Data(), as: UTF8.self)
    }

    private final class Answer: @unchecked Sendable {
        private let lock = NSLock()
        private var text: String?
        var value: String? { lock.withLock { text } }
        func set(_ value: String?) { lock.withLock { text = value } }
    }
}
