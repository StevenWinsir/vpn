import Foundation

/// Carries one framed Core command and returns the Core's raw response document.
/// Production uses the Helper's XPC interface; debug builds may spawn the Core directly.
public protocol CoreTransport: Sendable {
    /// Starts the Core worker if it is not running (idempotent).
    func start() async throws
    func send(command: String, generation: UInt64, payload: Data) async throws -> Data
    /// Stops the Core, which revokes the session best-effort before exiting.
    func stop() async
}

/// Typed commands over a `CoreTransport` (handoff_app.md section 5.3). Passwords appear
/// only in the one-shot `login` payload built here and are not retained.
public final class CoreClient: Sendable {
    private let transport: CoreTransport
    private let environment: ClientEnvironment
    private let appVersion: String
    private let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let text = try decoder.singleValueContainer().decode(String.self)
            let plain = ISO8601DateFormatter()
            let fractional = ISO8601DateFormatter()
            fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = fractional.date(from: text) ?? plain.date(from: text) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad date"))
        }
        return d
    }()

    public init(transport: CoreTransport, environment: ClientEnvironment, appVersion: String) {
        self.transport = transport
        self.environment = environment
        self.appVersion = appVersion
    }

    public func start() async throws { try await transport.start() }
    public func stop() async { await transport.stop() }

    public func login(email: String, password: String, deviceID: String) async throws -> CoreActionReply {
        try await action("login", generation: 0, payload: [
            "email": email, "password": password, "api_base": environment.apiBase.absoluteString,
            "channel": environment.channel.rawValue, "device_id": deviceID, "app_version": appVersion,
        ])
    }

    public func readSnapshot() async throws -> CoreSnapshot {
        let envelope: CoreEnvelope<CoreSnapshot> = try await call("read_snapshot", generation: 0, payload: [:])
        return try unwrap(envelope)
    }

    public func refreshCatalog(generation: UInt64) async throws -> CoreActionReply {
        try await action("refresh_catalog", generation: generation, payload: [:])
    }

    public func selectNode(generation: UInt64, nodeID: String, catalogRevision: String) async throws -> CoreActionReply {
        try await action("select_node", generation: generation, payload: ["node_id": nodeID, "catalog_revision": catalogRevision])
    }

    public func connect(generation: UInt64) async throws -> CoreActionReply {
        try await action("connect", generation: generation, payload: [:])
    }

    public func disconnect() async throws -> CoreSnapshot {
        let envelope: CoreEnvelope<CoreSnapshot> = try await call("disconnect", generation: 0, payload: [:])
        return try unwrap(envelope)
    }

    public func logout() async throws -> CoreSnapshot {
        let envelope: CoreEnvelope<CoreSnapshot> = try await call("logout", generation: 0, payload: [:])
        return try unwrap(envelope)
    }

    private func action(_ command: String, generation: UInt64, payload: [String: String]) async throws -> CoreActionReply {
        let envelope: CoreEnvelope<CoreActionReply> = try await call(command, generation: generation, payload: payload)
        if !envelope.ok, let error = envelope.error {
            // A refused command (stale generation, malformed payload) carries no snapshot.
            throw CoreCommandError(code: error.code, message: error.message)
        }
        return try unwrap(envelope)
    }

    private func call<R: Decodable & Sendable>(_ command: String, generation: UInt64, payload: [String: String]) async throws -> CoreEnvelope<R> {
        let body = try JSONSerialization.data(withJSONObject: payload, options: [.sortedKeys])
        let raw = try await transport.send(command: command, generation: generation, payload: body)
        guard let envelope = try? decoder.decode(CoreEnvelope<R>.self, from: raw) else { throw CoreClientError.malformedReply }
        guard envelope.protocolVersion == AsterLinkIdentity.protocolVersion else { throw CoreClientError.protocolMismatch }
        return envelope
    }

    private func unwrap<R>(_ envelope: CoreEnvelope<R>) throws -> R {
        if !envelope.ok, let error = envelope.error { throw CoreCommandError(code: error.code, message: error.message) }
        guard let result = envelope.result else { throw CoreClientError.malformedReply }
        return result
    }
}

/// A command the Core refused outright (as opposed to an action whose business outcome
/// is reported in `CoreActionReply.actionError`).
public struct CoreCommandError: Error, Equatable {
    public var code: String
    public var message: String
}
