import Foundation

/// The only commands the unprivileged App may send through the Helper to the Core
/// (handoff_app.md section 3.3). No command takes a path, URL, shell string or YAML, and
/// `shutdown` is reachable only through `stopWorker`, never as a free-form command.
public enum HelperCommandPolicy {
    public struct Limit: Sendable, Equatable {
        public let maxPayload: Int
        public let timeout: TimeInterval
    }

    public static let limits: [String: Limit] = [
        "login": Limit(maxPayload: 16 << 10, timeout: 40),
        "read_snapshot": Limit(maxPayload: 64, timeout: 10),
        "refresh_catalog": Limit(maxPayload: 64, timeout: 40),
        "select_node": Limit(maxPayload: 4 << 10, timeout: 40),
        "connect": Limit(maxPayload: 64, timeout: 40),
        "disconnect": Limit(maxPayload: 64, timeout: 15),
        "logout": Limit(maxPayload: 64, timeout: 15),
    ]

    public enum Rejection: Error, Equatable { case unknownCommand, payloadTooLarge, payloadNotObject }

    /// Validates the command name and payload and returns the request body to frame.
    public static func buildRequest(requestID: String, name: String, generation: UInt64, payload: Data) throws -> (body: Data, timeout: TimeInterval) {
        guard let limit = limits[name] else { throw Rejection.unknownCommand }
        guard payload.count <= limit.maxPayload else { throw Rejection.payloadTooLarge }
        guard let object = try? JSONSerialization.jsonObject(with: payload), object is [String: Any] else { throw Rejection.payloadNotObject }
        let request: [String: Any] = [
            "request_id": requestID, "protocol_version": AsterLinkIdentity.protocolVersion,
            "generation": generation, "command": name, "payload": object,
        ]
        return (try JSONSerialization.data(withJSONObject: request), limit.timeout)
    }

    /// A Core-shaped failure so the App has one reply format whether the Core or the Helper refused.
    public static func failureReply(code: String, message: String) -> Data {
        let body: [String: Any] = [
            "request_id": "", "protocol_version": AsterLinkIdentity.protocolVersion, "generation": 0,
            "ok": false, "error": ["code": code, "message": message],
        ]
        return (try? JSONSerialization.data(withJSONObject: body)) ?? Data()
    }
}
