import AsterLinkShared
import Foundation

struct WorkerManifest: Codable {
    var version: String
    var sha256: String
}

struct HelperFailure: Error {
    var code: HelperErrorCode
    var detail: String
}

/// Owns at most one Go Core worker. All state is guarded by `lock`; callers get
/// deterministic `worker_busy` for a second start instead of a queued second worker.
final class WorkerSupervisor {
    private let lock = NSLock()
    private var process: Process?
    private var input: FileHandle?
    private var output: FileHandle?
    private let store: WorkerStore
    private let policy: SigningPolicy
    private let bundleResources: URL

    init(store: WorkerStore, policy: SigningPolicy, bundleResources: URL) {
        self.store = store
        self.policy = policy
        self.bundleResources = bundleResources
    }

    var isRunning: Bool {
        lock.lock(); defer { lock.unlock() }
        return process?.isRunning == true
    }

    func start() throws -> CoreHello {
        lock.lock(); defer { lock.unlock() }
        if process?.isRunning == true { throw HelperFailure(code: .workerBusy, detail: "a worker is already running") }
        teardownLocked()

        let manifestURL = bundleResources.appendingPathComponent("worker-manifest.json")
        let source = bundleResources.appendingPathComponent("asterlink-core")
        let installed: URL
        do {
            let manifest = try JSONDecoder().decode(WorkerManifest.self, from: Data(contentsOf: manifestURL))
            installed = try store.install(source: source, version: manifest.version, expectedSHA256: manifest.sha256)
            try WorkerStore.verifySignature(installed, requirement: policy.requirement(forIdentifier: AsterLinkIdentity.coreIdentifier))
        } catch {
            throw HelperFailure(code: .workerInvalid, detail: "worker verification failed: \(type(of: error))")
        }

        let child = Process()
        child.executableURL = installed
        child.arguments = []
        child.environment = [:]
        let toChild = Pipe(), fromChild = Pipe()
        child.standardInput = toChild
        child.standardOutput = fromChild
        child.standardError = FileHandle.nullDevice
        do { try child.run() } catch {
            throw HelperFailure(code: .workerFailed, detail: "worker could not start")
        }
        process = child
        input = toChild.fileHandleForWriting
        output = fromChild.fileHandleForReading

        do {
            let response = try exchangeLocked(CoreRequest(requestID: UUID().uuidString, command: "hello"), timeout: 5)
            guard response.ok, let hello = response.result,
                  hello.minProtocolVersion <= AsterLinkIdentity.protocolVersion,
                  AsterLinkIdentity.protocolVersion <= hello.maxProtocolVersion else {
                teardownLocked()
                throw HelperFailure(code: .versionMismatch, detail: "worker protocol incompatible")
            }
            return hello
        } catch let failure as HelperFailure {
            throw failure
        } catch {
            teardownLocked()
            throw HelperFailure(code: .workerFailed, detail: "worker handshake failed")
        }
    }

    func stop() {
        lock.lock(); defer { lock.unlock() }
        if process?.isRunning == true {
            _ = try? exchangeLocked(CoreRequest(requestID: UUID().uuidString, command: "shutdown"), timeout: 2)
        }
        teardownLocked()
    }

    private func teardownLocked() {
        try? input?.close()
        try? output?.close()
        if let process, process.isRunning {
            let deadline = Date().addingTimeInterval(2)
            while process.isRunning && Date() < deadline { usleep(20_000) }
            if process.isRunning { process.terminate() }
            process.waitUntilExit()
        }
        process = nil; input = nil; output = nil
    }

    /// One request/one reply with a hard deadline; a worker that does not answer is killed.
    private func exchangeLocked(_ request: CoreRequest, timeout: TimeInterval) throws -> CoreResponse {
        guard let input, let output else { throw HelperFailure(code: .workerFailed, detail: "no worker") }
        try input.write(contentsOf: IPCFrame.encode(JSONEncoder().encode(request)))
        var result: Result<CoreResponse, Error>?
        let done = DispatchSemaphore(value: 0)
        DispatchQueue.global().async {
            do {
                let header = try WorkerSupervisor.readExactly(4, from: output)
                let length = try IPCFrame.declaredLength(header: header)
                let body = try WorkerSupervisor.readExactly(length, from: output)
                result = .success(try JSONDecoder().decode(CoreResponse.self, from: body))
            } catch { result = .failure(error) }
            done.signal()
        }
        if done.wait(timeout: .now() + timeout) == .timedOut {
            process?.terminate()
            throw HelperFailure(code: .workerFailed, detail: "worker timed out")
        }
        let response = try result!.get()
        guard response.requestID == request.requestID else {
            throw HelperFailure(code: .workerFailed, detail: "mismatched reply")
        }
        return response
    }

    static func readExactly(_ count: Int, from handle: FileHandle) throws -> Data {
        var data = Data()
        while data.count < count {
            guard let chunk = try handle.read(upToCount: count - data.count), !chunk.isEmpty else {
                throw IPCFrameError.truncated
            }
            data.append(chunk)
        }
        return data
    }
}
