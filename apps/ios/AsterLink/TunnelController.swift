import AsterLinkKit
import Foundation
import NetworkExtension

/// The app's side of the system VPN (handoff_app_ios.md 4.2, 4.3, 8.6): the one VPN
/// configuration, starting the extension with the session ticket, the provider-message channel,
/// and taking the session back. Exactly one Core writes to the server at any moment:
///   connect:    app Core exports (released) -> startVPNTunnel(options: ticket) -> extension adopts
///   disconnect: release message -> extension settles and returns a ticket -> stop -> app adopts
///   a stop the app did not ask for (Settings, another VPN, a failed start): once the system
///   reports the tunnel down, the app tries the ticket it exported; the server refuses it if the
///   extension revoked the session, and the user signs in again.
/// The ticket lives only in this object's memory.
@MainActor
final class TunnelController {
    enum Failure: Error, Equatable {
        case noConfiguration
        case core(CoreFailure)
        case system(String)
        case extensionRefused(String)
    }

    /// The extension's bundle id: the app's own plus ".tunnel" (project.pbxproj).
    static var providerBundleID: String { (Bundle.main.bundleIdentifier ?? "com.asterlink.dev.asterlink") + ".tunnel" }

    private(set) var manager: NETunnelProviderManager?
    /// The ticket handed to the extension, kept until the extension's session is accounted for.
    private var handedOut: String?

    var status: NEVPNStatus { manager?.connection.status ?? .invalid }

    /// Loads our configuration if the user already allowed it. Never prompts.
    func load() async throws {
        let all = try await NETunnelProviderManager.loadAllFromPreferences()
        manager = all.first { ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == Self.providerBundleID }
    }

    /// Removes our configuration from the system (the user asked in 我的). Never touches other VPNs.
    func remove() async throws {
        guard let manager else { return }
        try await manager.removeFromPreferences()
        self.manager = nil
    }

    /// Installs the configuration; the system asks the user once ("Add VPN Configurations").
    /// No ticket, token or node data is stored in it; On Demand stays off.
    func install() async throws {
        let manager = manager ?? NETunnelProviderManager()
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = Self.providerBundleID
        proto.serverAddress = "AsterLink"
        proto.providerConfiguration = [:]
        proto.includeAllNetworks = false
        manager.protocolConfiguration = proto
        manager.localizedDescription = "AsterLink"
        manager.isEnabled = true
        manager.isOnDemandEnabled = false
        try await manager.saveToPreferences()
        try await manager.loadFromPreferences()
        self.manager = manager
    }

    /// Hands the session to the extension and starts it. Returns once the system accepted the
    /// start; the tunnel reports connected only after the extension's Core verified it.
    func connect() async throws {
        guard let manager else { throw Failure.noConfiguration }
        let ticket: String
        switch await Task.detached(operation: { CoreBridge.exportSession() }).value {
        case .success(let value): ticket = value
        case .failure(let failure): throw Failure.core(failure)
        }
        handedOut = ticket
        do {
            try manager.connection.startVPNTunnel(options: [TunnelStartOptions.handoffKey: ticket as NSString])
        } catch {
            // The extension never saw the ticket: the session is ours again.
            _ = await takeBack()
            throw Failure.system(String(describing: (error as NSError).code))
        }
    }

    /// Asks the extension to settle and return the session, stops the tunnel and adopts it.
    func disconnect() async throws -> SnapshotHead {
        guard let manager else { throw Failure.noConfiguration }
        let reply = try await send(.release)
        guard case .released(let ticket) = try ProviderReply.decode(reply) else {
            if case .error(let code) = try ProviderReply.decode(reply) { throw Failure.extensionRefused(code) }
            throw Failure.extensionRefused("invalid_request")
        }
        handedOut = nil
        manager.connection.stopVPNTunnel()
        await waitUntilDown()
        switch await Task.detached(operation: { CoreBridge.adoptSession(ticket) }).value {
        case .success(let head): return head
        case .failure(let failure): throw Failure.core(failure)
        }
    }

    /// After a stop the app did not ask for: try the ticket we handed out, once.
    func takeBack() async -> Result<SnapshotHead, CoreFailure>? {
        guard let ticket = handedOut else { return nil }
        handedOut = nil
        return await Task.detached(operation: { CoreBridge.adoptSession(ticket) }).value
    }

    /// One ipc-v1 request to the extension's Core; the reply is the ipc-v1 response document.
    func call(_ request: String) async throws -> String {
        String(decoding: try await send(.ipc(request: request)), as: UTF8.self)
    }

    func send(_ message: ProviderMessage) async throws -> Data {
        guard let session = manager?.connection as? NETunnelProviderSession else { throw Failure.noConfiguration }
        return try await withCheckedThrowingContinuation { continuation in
            do {
                try session.sendProviderMessage(message.encoded()) { reply in
                    if let reply { continuation.resume(returning: reply) } else { continuation.resume(throwing: Failure.extensionRefused("no_reply")) }
                }
            } catch {
                continuation.resume(throwing: Failure.system(String(describing: (error as NSError).code)))
            }
        }
    }

    /// The reason the system or the extension gave for the last stop (handoff_app_ios.md 8.6).
    func lastDisconnectReason() async -> String? {
        guard let connection = manager?.connection else { return nil }
        return await withCheckedContinuation { continuation in
            connection.fetchLastDisconnectError { error in
                let code = (error as NSError?)?.userInfo["code"] as? String
                continuation.resume(returning: code ?? (error as NSError?).map { "\($0.domain)#\($0.code)" })
            }
        }
    }

    private func waitUntilDown() async {
        for _ in 0..<150 where status != .disconnected && status != .invalid {
            try? await Task.sleep(for: .milliseconds(100))
        }
    }
}
