import AsterLinkShared
import Foundation
import ServiceManagement

/// Settings-side view of the Helper: registration state and the stage-1 diagnostics.
@MainActor
final class HelperClient: ObservableObject {
    @Published private(set) var registration: HelperRegistration = .unknown
    @Published private(set) var lastReply: HelperReply?
    @Published private(set) var lastError: String?
    @Published private(set) var busy = false

    private let connection: HelperConnection

    init(connection: HelperConnection) {
        self.connection = connection
        refreshRegistration()
    }

    func refreshRegistration() { registration = HelperConnection.registration }

    /// Explicit user action only. macOS shows its own approval UI; we never ask for a password.
    func register() {
        do {
            try HelperConnection.service.register()
            lastError = nil
        } catch {
            lastError = "注册失败：\(error.localizedDescription)"
        }
        refreshRegistration()
    }

    func openSystemSettings() { SMAppService.openSystemSettingsLoginItems() }

    func hello() { diagnose { $0.hello(reply: $1) } }

    private func diagnose(_ invoke: @escaping (AsterLinkHelperXPC, @escaping (Data) -> Void) -> Void) {
        guard registration == .enabled else {
            lastError = "特权服务尚未启用"
            return
        }
        busy = true
        do {
            let proxy = try connection.proxy { [weak self] error in
                Task { @MainActor in
                    self?.lastError = "连接特权服务失败：\(error.localizedDescription)"
                    self?.busy = false
                }
            }
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
