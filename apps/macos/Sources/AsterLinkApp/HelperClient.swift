import AsterLinkShared
import Foundation
import ServiceManagement

enum HelperRegistration: Equatable {
    case notRegistered
    case requiresApproval
    case enabled
    case notFound
    case unknown
}

/// Thin typed wrapper over SMAppService + the Helper's XPC interface.
@MainActor
final class HelperClient: ObservableObject {
    @Published private(set) var registration: HelperRegistration = .unknown
    @Published private(set) var lastReply: HelperReply?
    @Published private(set) var lastError: String?
    @Published private(set) var busy = false

    private let policy: SigningPolicy
    private var connection: NSXPCConnection?

    init(policy: SigningPolicy) {
        self.policy = policy
        refreshRegistration()
    }

    private var service: SMAppService { SMAppService.daemon(plistName: AsterLinkIdentity.helperPlistName) }

    func refreshRegistration() {
        switch service.status {
        case .notRegistered: registration = .notRegistered
        case .requiresApproval: registration = .requiresApproval
        case .enabled: registration = .enabled
        case .notFound: registration = .notFound
        @unknown default: registration = .unknown
        }
    }

    /// Explicit user action only. macOS shows its own approval UI; we never ask for a password.
    func register() {
        do {
            try service.register()
            lastError = nil
        } catch {
            lastError = "注册失败：\(error.localizedDescription)"
        }
        refreshRegistration()
    }

    func openSystemSettings() { SMAppService.openSystemSettingsLoginItems() }

    func unregister() {
        disconnect()
        Task {
            try? await service.unregister()
            refreshRegistration()
        }
    }

    func hello() { call { $0.hello(reply: $1) } }
    func startWorker() { call { $0.startWorker(reply: $1) } }
    func stopWorker() { call { $0.stopWorker(reply: $1) } }

    private func disconnect() {
        connection?.invalidate()
        connection = nil
    }

    private func makeConnection() throws -> NSXPCConnection {
        if let connection { return connection }
        let created = NSXPCConnection(machServiceName: AsterLinkIdentity.helperMachService, options: .privileged)
        // The app only talks to a Helper signed by the expected identity.
        created.setCodeSigningRequirement(try policy.requirement(forIdentifier: AsterLinkIdentity.helperBundleID))
        created.remoteObjectInterface = NSXPCInterface(with: AsterLinkHelperXPC.self)
        created.invalidationHandler = { [weak self] in Task { @MainActor in self?.connection = nil } }
        created.resume()
        connection = created
        return created
    }

    private func call(_ invoke: @escaping (AsterLinkHelperXPC, @escaping (Data) -> Void) -> Void) {
        guard registration == .enabled else {
            lastError = "特权服务尚未启用"
            return
        }
        busy = true
        do {
            let proxy = try makeConnection().remoteObjectProxyWithErrorHandler { [weak self] error in
                Task { @MainActor in
                    self?.lastError = "连接特权服务失败：\(error.localizedDescription)"
                    self?.busy = false
                }
            } as! AsterLinkHelperXPC
            invoke(proxy) { [weak self] data in
                Task { @MainActor in
                    guard let self else { return }
                    self.busy = false
                    if let reply = try? JSONDecoder().decode(HelperReply.self, from: data),
                       reply.protocolVersion == AsterLinkIdentity.protocolVersion {
                        self.lastReply = reply
                        self.lastError = reply.ok ? nil : "\(reply.errorCode?.rawValue ?? "error")：\(reply.detail ?? "")"
                    } else {
                        self.lastError = "特权服务返回了不兼容的数据"
                    }
                }
            }
        } catch {
            busy = false
            lastError = "无法建立安全连接：\(error)"
        }
    }
}
