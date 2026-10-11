import Foundation

/// The complete surface the unprivileged app may call. Deliberately no path, URL,
/// shell string or YAML parameter: every argument is absent or a fixed command.
/// Replies are small JSON documents (`HelperReply`) so only `Data` crosses the
/// boundary and the allowed decode classes stay minimal.
@objc public protocol AsterLinkHelperXPC {
    func hello(reply: @escaping (Data) -> Void)
    /// Verifies and installs the bundled worker, starts exactly one, performs the IPC hello.
    func startWorker(reply: @escaping (Data) -> Void)
    func stopWorker(reply: @escaping (Data) -> Void)
}

public enum HelperErrorCode: String, Codable, Sendable {
    case workerBusy = "worker_busy"
    case workerInvalid = "worker_invalid"
    case workerFailed = "worker_failed"
    case versionMismatch = "version_mismatch"
    case notRoot = "not_root"
}

public struct HelperReply: Codable, Sendable, Equatable {
    public var ok: Bool
    public var errorCode: HelperErrorCode?
    public var detail: String?
    public var helperVersion: String
    public var protocolVersion: Int
    public var workerRunning: Bool
    public var core: CoreHello?

    enum CodingKeys: String, CodingKey {
        case ok, errorCode = "error_code", detail, helperVersion = "helper_version"
        case protocolVersion = "protocol_version", workerRunning = "worker_running", core
    }

    public init(ok: Bool, errorCode: HelperErrorCode? = nil, detail: String? = nil, helperVersion: String,
                workerRunning: Bool, core: CoreHello? = nil) {
        self.ok = ok
        self.errorCode = errorCode
        self.detail = detail
        self.helperVersion = helperVersion
        self.protocolVersion = AsterLinkIdentity.protocolVersion
        self.workerRunning = workerRunning
        self.core = core
    }

    public func encoded() -> Data { (try? JSONEncoder().encode(self)) ?? Data() }
}
