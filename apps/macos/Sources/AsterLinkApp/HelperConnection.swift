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
        case .requiresApproval: "需要在“系统设置 › 通用 › 登录项”中批准"
        case .notRegistered: "未启用"
        case .notFound: "未找到服务（请使用完整签名的 App 包运行）"
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
            throw HelperUnavailable("无法建立与特权服务的安全连接")
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

    init(connection: HelperConnection) { self.connection = connection }

    private func call(_ invoke: @escaping (AsterLinkHelperXPC, @escaping (Data) -> Void) -> Void) async throws -> Data {
        guard HelperConnection.registration == .enabled else {
            throw HelperUnavailable("特权服务尚未启用，请先在设置中启用")
        }
        return try await withCheckedThrowingContinuation { continuation in
            let once = Once(continuation)
            do {
                let proxy = try connection.proxy { once.resume(throwing: HelperUnavailable("特权服务连接中断：\($0.localizedDescription)")) }
                invoke(proxy) { once.resume(returning: $0) }
            } catch {
                once.resume(throwing: error)
            }
        }
    }

    func start() async throws {
        let data = try await call { $0.startWorker(reply: $1) }
        guard let reply = try? JSONDecoder().decode(HelperReply.self, from: data), reply.protocolVersion == AsterLinkIdentity.protocolVersion else {
            throw HelperUnavailable("特权服务返回了不兼容的数据")
        }
        // worker_busy means one is already running, which is exactly what we want.
        if !reply.ok && reply.errorCode != .workerBusy {
            throw HelperUnavailable("无法启动内核：\(reply.detail ?? reply.errorCode?.rawValue ?? "未知错误")")
        }
    }

    func send(command: String, generation: UInt64, payload: Data) async throws -> Data {
        try await call { $0.command(name: command, generation: generation, payload: payload, reply: $1) }
    }

    func stop() async {
        _ = try? await call { $0.stopWorker(reply: $1) }
    }
}
