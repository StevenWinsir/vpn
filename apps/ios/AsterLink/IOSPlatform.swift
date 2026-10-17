import AsterCore
import AsterLinkKit
import Foundation

/// The Core's platform callbacks in the app process (handoff_app_ios.md 4.1). The app never
/// owns a tunnel: protect and openTun refuse, so a connect attempted here ends in a Core error
/// (tun_unavailable) rather than an apparent connection. Connecting means handing the session
/// to the packet tunnel extension (TunnelController). The network report lets the Core resolve
/// its API host with the network's own resolvers.
final class IOSPlatform: NSObject, MobilePlatformProtocol, @unchecked Sendable {
    let monitor = PathMonitor()

    override init() {
        super.init()
        monitor.onChange = { MobileNetworkChanged() }
        monitor.start()
    }

    deinit { monitor.cancel() }

    func protect(_ fd: Int32) -> Bool { false }
    func openTun(_ specJSON: String?) -> Int32 { -1 }
    func networkSnapshot() -> String { monitor.latestJSON }
}

enum CoreLifecycle {
    /// Starts the in-process Core with the app's private cache directory.
    static func start(platform: MobilePlatformProtocol = IOSPlatform()) throws {
        let cache = try FileManager.default.url(for: .cachesDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
            .appendingPathComponent("core", isDirectory: true)
        try FileManager.default.createDirectory(at: cache, withIntermediateDirectories: true)
        let build = try JSONSerialization.data(withJSONObject: ["cache_dir": cache.standardizedFileURL.path])
        var error: NSError?
        guard MobileStart(platform, String(decoding: build, as: UTF8.self), &error) else {
            throw error ?? NSError(domain: "com.asterlink.core", code: 1)
        }
    }

    static func stop() { MobileStop() }
}
