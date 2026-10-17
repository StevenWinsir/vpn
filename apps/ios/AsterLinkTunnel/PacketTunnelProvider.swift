import NetworkExtension

/// Round 1 placeholder (handoff_app_ios.md round 2 implements the tunnel). It refuses every
/// start explicitly, so nothing can ever look connected before the real data path exists.
/// The completion-handler forms keep the non-Sendable options dictionary on the caller's side.
final class PacketTunnelProvider: NEPacketTunnelProvider {
    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        completionHandler(NSError(domain: "com.asterlink.tunnel", code: 1,
                                  userInfo: [NSLocalizedDescriptionKey: "隧道尚未实现（第 2 轮）"]))
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        completionHandler()
    }
}
