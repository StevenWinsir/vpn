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
    /// `lock` serialises worker lifecycle and I/O. `stateLock` guards only the flags below so
    /// status queries and a forced stop never wait behind a slow exchange.
    private let lock = NSLock()
    private let stateLock = NSLock()
    private var running = false
    private var activeProcess: Process?
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
        stateLock.lock(); defer { stateLock.unlock() }
        return running && activeProcess?.isRunning == true
    }

    private func publish(_ child: Process?) {
        stateLock.lock()
        activeProcess = child
        running = child != nil
        stateLock.unlock()
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
        publish(child)
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
        // A command may be blocked on the network inside the Core for tens of seconds.
        // Give it a moment, then kill the worker so a quit or lost caller is not held hostage.
        if !lock.lock(before: Date().addingTimeInterval(1)) {
            stateLock.lock(); let victim = activeProcess; stateLock.unlock()
            victim?.terminate()
            lock.lock()
        }
        defer { lock.unlock() }
        if process?.isRunning == true {
            _ = try? exchangeLocked(CoreRequest(requestID: UUID().uuidString, command: "shutdown"), timeout: 2)
        }
        teardownLocked()
    }

    private func teardownLocked() {
        try? input?.close()
        try? output?.close()
        if let process, process.isRunning {
            // After the shutdown command the Core closes the data path, settles the last traffic
            // batch and revokes the session; give that bounded work time before terminating.
            let deadline = Date().addingTimeInterval(12)
            while process.isRunning && Date() < deadline { usleep(20_000) }
            if process.isRunning { process.terminate() }
            process.waitUntilExit()
        }
        process = nil; input = nil; output = nil
        publish(nil)
    }

    /// One request/one reply with a hard deadline; a worker that does not answer is killed.
    private func exchangeLocked(_ request: CoreRequest, timeout: TimeInterval) throws -> CoreResponse {
        let raw = try exchangeRawLocked(body: JSONEncoder().encode(request), requestID: request.requestID, timeout: timeout)
        guard let response = try? JSONDecoder().decode(CoreResponse.self, from: raw) else {
            throw HelperFailure(code: .workerFailed, detail: "unreadable reply")
        }
        return response
    }

    private func exchangeRawLocked(body: Data, requestID: String, timeout: TimeInterval) throws -> Data {
        guard let input, let output else { throw HelperFailure(code: .workerFailed, detail: "no worker") }
        try input.write(contentsOf: IPCFrame.encode(body))
        var result: Result<Data, Error>?
        let done = DispatchSemaphore(value: 0)
        DispatchQueue.global().async {
            do {
                let header = try WorkerSupervisor.readExactly(4, from: output)
                let length = try IPCFrame.declaredLength(header: header)
                result = .success(try WorkerSupervisor.readExactly(length, from: output))
            } catch { result = .failure(error) }
            done.signal()
        }
        if done.wait(timeout: .now() + timeout) == .timedOut {
            process?.terminate()
            throw HelperFailure(code: .workerFailed, detail: "worker timed out")
        }
        let raw = try result!.get()
        struct Echo: Decodable { var request_id: String }
        guard let echo = try? JSONDecoder().decode(Echo.self, from: raw), echo.request_id == requestID else {
            throw HelperFailure(code: .workerFailed, detail: "mismatched reply")
        }
        return raw
    }

    /// Forwards one validated command and returns the Core's raw response document. A
    /// timeout or broken pipe kills the worker: the App must start a fresh one and log in again.
    func forward(name: String, generation: UInt64, payload: Data) throws -> Data {
        let requestID = UUID().uuidString
        let built: (body: Data, timeout: TimeInterval)
        do {
            built = try HelperCommandPolicy.buildRequest(requestID: requestID, name: name, generation: generation, payload: payload)
        } catch {
            throw HelperFailure(code: .workerInvalid, detail: "command rejected")
        }
        lock.lock(); defer { lock.unlock() }
        guard process?.isRunning == true else { throw HelperFailure(code: .workerFailed, detail: "worker not running") }
        do {
            return try exchangeRawLocked(body: built.body, requestID: requestID, timeout: built.timeout)
        } catch let failure as HelperFailure {
            teardownLocked()
            throw failure
        } catch {
            teardownLocked()
            throw HelperFailure(code: .workerFailed, detail: "worker connection lost")
        }
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
