#if DEBUG
import AsterLinkShared
import Foundation

/// DEBUG-only: spawns the Core as the current user, bypassing the Helper, so the account
/// and catalog flow can be exercised without a signed bundle. It is selected only when
/// ASTERLINK_DEV_CORE is set, and is compiled out of Release entirely. Stage 2 needs no
/// privileges (there is no tunnel yet); stage 3 testing must use the real Helper.
actor DirectCoreTransport: CoreTransport {
    private let executable: URL
    private var process: Process?
    private var input: FileHandle?
    private var output: FileHandle?

    init(executable: URL) { self.executable = executable }

    func start() async throws {
        if process?.isRunning == true { return }
        let child = Process()
        child.executableURL = executable
        child.environment = [:]
        let toChild = Pipe(), fromChild = Pipe()
        child.standardInput = toChild
        child.standardOutput = fromChild
        child.standardError = FileHandle.nullDevice
        do { try child.run() } catch { throw HelperUnavailable("无法启动内核（调试直连）") }
        process = child
        input = toChild.fileHandleForWriting
        output = fromChild.fileHandleForReading
    }

    func send(command: String, generation: UInt64, payload: Data) async throws -> Data {
        guard let input, let output, process?.isRunning == true else { throw HelperUnavailable("内核未运行") }
        let id = UUID().uuidString
        let built: (body: Data, timeout: TimeInterval)
        do { built = try HelperCommandPolicy.buildRequest(requestID: id, name: command, generation: generation, payload: payload) }
        catch { throw HelperUnavailable("命令被拒绝") }
        try input.write(contentsOf: IPCFrame.encode(built.body))
        let timeout = built.timeout
        return try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global().async {
                let finished = DispatchSemaphore(value: 0)
                var result: Result<Data, Error> = .failure(HelperUnavailable("内核无响应"))
                DispatchQueue.global().async {
                    do {
                        let header = try Self.read(4, from: output)
                        result = .success(try Self.read(try IPCFrame.declaredLength(header: header), from: output))
                    } catch { result = .failure(HelperUnavailable("内核连接中断")) }
                    finished.signal()
                }
                if finished.wait(timeout: .now() + timeout) == .timedOut { Task { await self.kill() } }
                continuation.resume(with: result)
            }
        }
    }

    func stop() async {
        if process?.isRunning == true, let input {
            let id = UUID().uuidString
            if let frame = try? HelperCommandPolicy.buildRequest(requestID: id, name: "logout", generation: 0, payload: Data("{}".utf8)) {
                try? input.write(contentsOf: IPCFrame.encode(frame.body))
            }
        }
        try? input?.close()
        let deadline = Date().addingTimeInterval(4)
        while process?.isRunning == true && Date() < deadline { try? await Task.sleep(for: .milliseconds(50)) }
        kill()
    }

    private func kill() {
        if process?.isRunning == true { process?.terminate() }
        process = nil; input = nil; output = nil
    }

    private static func read(_ count: Int, from handle: FileHandle) throws -> Data {
        var data = Data()
        while data.count < count {
            guard let chunk = try handle.read(upToCount: count - data.count), !chunk.isEmpty else { throw IPCFrameError.truncated }
            data.append(chunk)
        }
        return data
    }
}
#endif
