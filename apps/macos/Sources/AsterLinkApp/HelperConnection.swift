import AsterLinkShared
import Foundation
import ServiceManagement

enum HelperRegistration: Equatable {
    case notRegistered
    case requiresApproval
    case enabled
    case notFound
    case unknown

    init(_ status: SMAppService.Status) {
        switch status {
        case .notRegistered: self = .notRegistered
        case .requiresApproval: self = .requiresApproval
        case .enabled: self = .enabled
        case .notFound: self = .notFound
        @unknown default: self = .unknown
        }
    }

    var summary: String {
        switch self {
        case .enabled: "已启用"
        case .requiresApproval: "需要在“系统设置 › 通用 › 登录项与扩展”中允许"
        case .notRegistered: "未启用"
        case .notFound: "应用安装不完整，请从官网重新下载安装"
        case .unknown: "未知"
        }
    }
}

/// The single XPC connection to the Helper, shared by the diagnostics UI and the Core transport.
final class HelperConnection: @unchecked Sendable {
    private let policy: SigningPolicy
    private let lock = NSLock()
    private var connection: NSXPCConnection?

    init(policy: SigningPolicy) { self.policy = policy }

    static var service: SMAppService { SMAppService.daemon(plistName: AsterLinkIdentity.helperPlistName) }
    static var registration: HelperRegistration { HelperRegistration(service.status) }

    func invalidate() {
        lock.lock(); defer { lock.unlock() }
        connection?.invalidate()
        connection = nil
    }

    /// Returns a proxy whose failures go to `onError`. The connection requires the Helper to
    /// carry the expected code-signing identity before any message is sent.
    func proxy(onError: @escaping @Sendable (Error) -> Void) throws -> AsterLinkHelperXPC {
        lock.lock(); defer { lock.unlock() }
        if connection == nil {
            let created = NSXPCConnection(machServiceName: AsterLinkIdentity.helperMachService, options: .privileged)
            created.setCodeSigningRequirement(try policy.requirement(forIdentifier: AsterLinkIdentity.helperBundleID))
            created.remoteObjectInterface = NSXPCInterface(with: AsterLinkHelperXPC.self)
            created.invalidationHandler = { [weak self] in
                self?.lock.lock(); self?.connection = nil; self?.lock.unlock()
            }
            created.resume()
            connection = created
        }
        guard let proxy = connection?.remoteObjectProxyWithErrorHandler({ onError($0) }) as? AsterLinkHelperXPC else {
            throw HelperUnavailable("无法连接后台服务，请稍后重试")
        }
        return proxy
    }
}

/// Resumes a continuation exactly once even if both the reply and the error handler fire.
private final class Once<T>: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<T, Error>?
    init(_ continuation: CheckedContinuation<T, Error>) { self.continuation = continuation }
    func resume(returning value: T) { take()?.resume(returning: value) }
    func resume(throwing error: Error) { take()?.resume(throwing: error) }
    private func take() -> CheckedContinuation<T, Error>? {
        lock.lock(); defer { lock.unlock() }
        defer { continuation = nil }
        return continuation
    }
}

/// Production transport: App -> Helper (XPC, caller-signature checked) -> Core (private pipe).
final class XPCCoreTransport: CoreTransport {
    private let connection: HelperConnection

    private let appVersion: String

    init(connection: HelperConnection, appVersion: String) {
        self.connection = connection
        self.appVersion = appVersion
    }

    private func call(_ invoke: @escaping (AsterLinkHelperXPC, @escaping (Data) -> Void) -> Void) async throws -> Data {
        guard HelperConnection.registration == .enabled else {
            throw HelperUnavailable("后台服务尚未启用，请先按提示启用")
        }
        return try await withCheckedThrowingContinuation { continuation in
            let once = Once(continuation)
            do {
                let proxy = try connection.proxy { _ in once.resume(throwing: HelperUnavailable("与后台服务的连接已中断，请稍后重试")) }
                invoke(proxy) { once.resume(returning: $0) }
            } catch {
                once.resume(throwing: error)
            }
        }
    }

    func start() async throws {
        // Version gate first, so a stale Helper left over from an upgrade never starts a Core.
        let greeting = try await call { $0.hello(reply: $1) }
        guard let hello = try? JSONDecoder().decode(HelperReply.self, from: greeting), hello.protocolVersion == AsterLinkIdentity.protocolVersion else {
            throw HelperUnavailable("后台服务需要更新，请在“账户”页重新安装后台服务")
        }
        if let message = HelperUpgradePolicy.userMessage(for: HelperUpgradePolicy.verdict(helperVersion: hello.helperVersion, appVersion: appVersion)) {
            throw HelperUnavailable(message)
        }
        let data = try await call { $0.startWorker(reply: $1) }
        guard let reply = try? JSONDecoder().decode(HelperReply.self, from: data), reply.protocolVersion == AsterLinkIdentity.protocolVersion else {
            throw HelperUnavailable("后台服务需要更新，请在“账户”页重新安装后台服务")
        }
        // worker_busy means one is already running, which is exactly what we want.
        if !reply.ok && reply.errorCode != .workerBusy {
            throw HelperUnavailable("连接服务启动失败，请在“账户”页重新安装后台服务")
        }
    }

    func send(command: String, generation: UInt64, payload: Data) async throws -> Data {
        try await call { $0.command(name: command, generation: generation, payload: payload, reply: $1) }
    }

    func stop() async {
        _ = try? await call { $0.stopWorker(reply: $1) }
    }
}
