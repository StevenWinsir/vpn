import AsterCore
import Foundation

/// The Core's platform callbacks in the app process (handoff_app_ios.md 4.1). In round 1
/// there is no tunnel: protect refuses, openTun fails and the network snapshot is empty, so
/// any attempt to connect ends in a Core error rather than an apparent connection. The tunnel
/// extension gets its own implementation in round 2.
final class IOSPlatform: NSObject, MobilePlatformProtocol {
    func protect(_ fd: Int32) -> Bool { false }
    func openTun(_ specJSON: String?) -> Int32 { -1 }
    func networkSnapshot() -> String { "" }
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
