import AsterLinkShared
import Foundation
import Observation
import ServiceManagement

/// Settings-side view of the Helper: registration state and the stage-1 diagnostics.
/// `@Observable` (like SessionController) so every view reading it re-renders when the
/// registration changes; as an unobserved ObservableObject "启用"/"刷新" looked dead.
@MainActor
@Observable
final class HelperClient {
    private(set) var registration: HelperRegistration = .unknown
    private(set) var lastReply: HelperReply?
    private(set) var lastError: String?
    private(set) var busy = false
    private(set) var versionWarning: String?
    /// The App runs from the DMG or a translocated copy: launchd cannot use a daemon
    /// from there, so the user must move it to /Applications first.
    let installProblem: String? = HelperClient.installLocationProblem(Bundle.main.bundlePath)

    private let connection: HelperConnection

    init(connection: HelperConnection) {
        self.connection = connection
        refreshRegistration()
    }

    func refreshRegistration() {
        let current = HelperConnection.registration
        if current != registration { registration = current }
        if current == .enabled, lastError == Self.approvalHint { lastError = nil }
    }

    static let approvalHint = "请在打开的“系统设置 › 通用 › 登录项与扩展”中，打开“允许在后台”里 AsterLink 的开关；完成后本窗口会自动继续。"

    /// Explicit user action only, and the only one first-run needs: registers the daemon and,
    /// since macOS always requires the user to allow a new background service, opens the
    /// exact System Settings page at once. `watchRegistration` then notices the approval.
    /// We never ask for a password.
    func register() {
        if let installProblem {
            lastError = installProblem
            return
        }
        var failure: Error?
        if registration != .requiresApproval {
            do { try HelperConnection.service.register() } catch { failure = error }
        }
        refreshRegistration()
        switch registration {
        case .enabled:
            lastError = nil
        case .requiresApproval:
            // register() reports "Operation not permitted" here; that is the normal
            // pending-approval state, not an error worth showing.
            lastError = Self.approvalHint
            openSystemSettings()
        default:
            lastError = "启用失败：\(failure?.localizedDescription ?? registration.summary)"
        }
    }

    /// Polls the registration while the window is open and the service is not enabled
    /// yet. SMAppService posts no change notification, and the approval happens in
    /// another app, so without this the user would have to come back and press 刷新.
    func watchRegistration() async {
        while !Task.isCancelled {
            refreshRegistration()
            try? await Task.sleep(for: registration == .enabled ? .seconds(5) : .seconds(1))
        }
    }

    nonisolated static func installLocationProblem(_ path: String) -> String? {
        if path.hasPrefix("/Volumes/") || path.contains("/AppTranslocation/") {
            return "请先把 AsterLink 拖到“应用程序”文件夹，从那里重新打开（不能直接在安装盘里运行）。"
        }
        return nil
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
