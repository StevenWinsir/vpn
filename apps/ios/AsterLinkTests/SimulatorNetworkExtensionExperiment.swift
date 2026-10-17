import Foundation
import NetworkExtension
import Testing

/// Experiment E-SIM-NE (handoff_app_ios.md round 1): can the iOS Simulator save a packet
/// tunnel configuration at all? Apple has never supported running a Packet Tunnel Provider
/// in the Simulator. This records what actually happens on this Xcode and runtime instead of
/// assuming it, so the test passes either way; scripts/test-native-ios.py reads the line it
/// prints. It removes any configuration it managed to save.
struct SimulatorNetworkExtensionExperiment {
    @Test func recordWhetherATunnelConfigurationCanBeSaved() async {
        let manager = NETunnelProviderManager()
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = (Bundle.main.bundleIdentifier ?? "") + ".tunnel"
        proto.serverAddress = "AsterLink"
        manager.protocolConfiguration = proto
        manager.localizedDescription = "AsterLink E-SIM-NE"
        do {
            try await manager.saveToPreferences()
            print("E-SIM-NE: saved")
            try? await manager.removeFromPreferences()
        } catch let error as NSError {
            print("E-SIM-NE: refused domain=\(error.domain) code=\(error.code) description=\(error.localizedDescription)")
        }
    }
}
