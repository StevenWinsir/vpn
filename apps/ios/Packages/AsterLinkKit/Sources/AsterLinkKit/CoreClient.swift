import Foundation

/// One synchronous request/response exchange with the Core (MobileCall in the app).
public protocol CoreTransport: Sendable {
    func call(_ request: String) -> String
}

/// A command the Core refused, with its stable code. A business failure (a wrong password, no
/// plan) is not thrown: it comes back inside `SnapshotReply.actionError`.
public struct CoreException: Error, Equatable, Sendable {
    public let code: String
    public let message: String

    public init(code: String, message: String = "") { self.code = code; self.message = message }
}

public enum CoreCodes {
    public static let notStarted = "core_not_started"
    public static let invalidRequest = "invalid_request"
    public static let staleGeneration = "stale_generation"
}

/// The public build parameters every login carries (apps/ios/.env through Info.plist).
public struct LoginTarget: Sendable, Equatable {
    public let apiBase: String
    public let channel: BuildChannel
    public let appVersion: String

    public init(apiBase: String, channel: BuildChannel, appVersion: String) {
        self.apiBase = apiBase; self.channel = channel; self.appVersion = appVersion
    }
}

/// Typed commands over a `CoreTransport` (the Swift mirror of Android's CoreClient). Every
/// exchange runs off the caller's actor: a Core call blocks for as long as the command runs
/// (connect: up to 90 s). A refused command (stale generation, malformed payload, Core not
/// started) throws `CoreException`. The password appears only in the one-shot login payload;
/// nothing here keeps it.
public final class CoreClient: Sendable {
    private let transport: any CoreTransport

    public init(transport: any CoreTransport) { self.transport = transport }

    public func hello() async throws -> CoreHello {
        guard let data = try await send("hello", generation: 0) else { throw malformed() }
        return try decode(CoreHello.self, data)
    }

    public func login(email: String, password: String, deviceID: String, target: LoginTarget) async throws -> SnapshotReply {
        try reply(await send("login", generation: 0, payload: [
            "email": email, "password": password, "api_base": target.apiBase, "channel": target.channel.rawValue,
            "device_id": deviceID, "app_version": target.appVersion,
        ]))
    }

    public func readSnapshot() async throws -> CoreSnapshot { try reply(await send("read_snapshot", generation: 0)).snapshot }

    public func refreshCatalog(generation: UInt64) async throws -> SnapshotReply {
        try reply(await send("refresh_catalog", generation: generation))
    }

    public func selectNode(generation: UInt64, nodeID: String, catalogRevision: String) async throws -> SnapshotReply {
        try reply(await send("select_node", generation: generation, payload: ["node_id": nodeID, "catalog_revision": catalogRevision]))
    }

    /// Connects the managed session (every gate, the first metering ACK, the TUN, then Verify).
    public func connect(generation: UInt64) async throws -> SnapshotReply {
        try reply(await send("connect", generation: generation, payload: [:]))
    }

    /// Always safe (no generation): closes the data path and settles the tail.
    public func disconnect() async throws -> CoreSnapshot { try reply(await send("disconnect", generation: 0)).snapshot }

    /// Always safe: stops, settles, revokes and forgets the session inside the Core.
    public func logout() async throws -> CoreSnapshot { try reply(await send("logout", generation: 0)).snapshot }

    private func send(_ command: String, generation: UInt64, payload: [String: String]? = nil) async throws -> Data? {
        let requestID = UUID().uuidString
        let request = CoreRequest.command(command, requestID: requestID, generation: generation, payload: payload)
        let transport = self.transport
        let raw = await Task.detached(priority: .userInitiated) { transport.call(request) }.value
        guard let response = try? JSONDecoder().decode(CoreResponse.self, from: Data(raw.utf8)) else { throw malformed() }
        // A response to another request is never applied.
        guard response.requestID == requestID else { throw CoreException(code: CoreCodes.invalidRequest, message: "core answered a different request") }
        if let failure = response.error { throw CoreException(code: failure.code, message: failure.message) }
        guard response.ok else { throw CoreException(code: CoreCodes.invalidRequest, message: "core refused without a code") }
        guard response.generation == generation else { throw CoreException(code: CoreCodes.invalidRequest, message: "core answered a different request") }
        return response.result?.data
    }

    private func reply(_ result: Data?) throws -> SnapshotReply {
        guard let result else { throw malformed() }
        do { return try SnapshotReply.parse(result) } catch { throw malformed() }
    }

    private func decode<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try JSONDecoder().decode(type, from: data) } catch { throw malformed() }
    }

    private func malformed() -> CoreException { CoreException(code: CoreCodes.invalidRequest, message: "malformed core result") }
}
