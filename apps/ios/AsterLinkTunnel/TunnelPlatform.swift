import AsterCore
import AsterLinkKit
import Foundation
import NetworkExtension

/// The Core's platform callbacks inside the packet tunnel extension (handoff_app_ios.md 7, 8.2).
/// openTun is called by the Go Core on its own thread, after every authorization gate and the
/// first metering acknowledgement: it applies the spec as tunnel settings, waits for the system
/// to install them, finds the utun and returns a dup() the Core then owns.
final class TunnelPlatform: NSObject, MobilePlatformProtocol, @unchecked Sendable {
    private weak var provider: NEPacketTunnelProvider?
    let monitor: PathMonitor
    /// How long the system may take to install the settings.
    private let applyTimeout: DispatchTimeInterval = .seconds(20)

    init(provider: NEPacketTunnelProvider, monitor: PathMonitor) {
        self.provider = provider
        self.monitor = monitor
    }

    /// The extension's own sockets do not enter its tunnel; nothing to protect.
    func protect(_ fd: Int32) -> Bool { true }

    func networkSnapshot() -> String { monitor.latestJSON }

    func openTun(_ specJSON: String?) -> Int32 {
        guard let provider, let specJSON, let spec = try? TunnelSpec.parse(specJSON) else { return -1 }
        // The resolvers seen from now on are the tunnel's own; keep the physical network's.
        monitor.freezeResolvers()
        let outcome = Outcome()
        provider.setTunnelNetworkSettings(TunnelSettings.make(spec.plan)) { error in
            outcome.finish(error)
        }
        guard outcome.wait(applyTimeout), outcome.error == nil else { return -1 }
        return (try? UtunDescriptor.duplicate()) ?? -1
    }

    /// The completion arrives on a system queue while the Core's thread waits here.
    private final class Outcome: @unchecked Sendable {
        private let done = DispatchSemaphore(value: 0)
        private(set) var error: Error?

        func finish(_ error: Error?) {
            self.error = error
            done.signal()
        }

        func wait(_ timeout: DispatchTimeInterval) -> Bool { done.wait(timeout: .now() + timeout) == .success }
    }
}
