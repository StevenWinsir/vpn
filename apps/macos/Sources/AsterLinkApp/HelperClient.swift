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
    @Published private(set) var versionWarning: String?

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

    /// Asks the Helper to stop its Core, drops the connection, then removes the launchd
    /// registration. Only this product's service is touched; other VPNs are never involved.
    func unregister() async {
        busy = true
        defer { busy = false; refreshRegistration() }
        if registration == .enabled {
            await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
                guard let proxy = try? connection.proxy(onError: { _ in done.resume() }) else { return done.resume() }
                proxy.stopWorker { _ in done.resume() }
            }
        }
        connection.invalidate()
        do {
            try await HelperConnection.service.unregister()
            lastError = nil
        } catch {
            lastError = "移除失败：\(error.localizedDescription)"
        }
    }

    /// Upgrade path: remove the stale registration, then register the Helper from this bundle.
    func reinstall() async {
        await unregister()
        if lastError == nil { register() }
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
                        self.versionWarning = HelperUpgradePolicy.userMessage(for: HelperUpgradePolicy.verdict(
                            helperVersion: reply.helperVersion,
                            appVersion: (Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String) ?? "dev"))
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
