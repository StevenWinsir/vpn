import Foundation

/// The app <-> tunnel extension channel (NETunnelProviderSession.sendProviderMessage,
/// shared/contracts/handoff-v1.json, handoff_app_ios.md 4.3). The extension offers exactly
/// these messages: an ipc-v1 request for its Core, the release of the session back to the
/// app, the foreground hint and the app's route probe result. Nothing can carry YAML, a path
/// or a URL. Decoding is strict on both sides.
public enum ProviderMessage: Equatable, Sendable {
    /// One ipc-v1 request document, forwarded to the extension's Core unchanged.
    case ipc(request: String)
    /// Settle and hand the session back; the reply carries the ticket.
    case release
    case foreground(visible: Bool)
    /// Verify's second stage (handoff_app_ios.md 8.5): did an ordinary app request take the
    /// system route through the tunnel and get the controlled answer?
    case routeProbe(ok: Bool)

    /// Messages are a few kilobytes; ipc-v1 requests are bounded at 2 MiB.
    public static let maxSize = 2 << 20

    public enum Failure: Error, Equatable {
        case malformed
    }

    public func encoded() -> Data {
        switch self {
        case .ipc(let request):
            // The request is embedded as a JSON object, not as a string.
            return Data(#"{"kind":"ipc","request":"#.utf8) + Data(request.utf8) + Data("}".utf8)
        case .release:
            return Data(#"{"kind":"release"}"#.utf8)
        case .foreground(let visible):
            return Data(#"{"kind":"foreground","visible":\#(visible)}"#.utf8)
        case .routeProbe(let ok):
            return Data(#"{"kind":"route_probe","ok":\#(ok)}"#.utf8)
        }
    }

    public static func decode(_ data: Data) throws -> ProviderMessage {
        guard data.count <= maxSize,
              let object = try? JSONSerialization.jsonObject(with: data, options: []) as? [String: Any],
              let kind = object["kind"] as? String
        else { throw Failure.malformed }
        let keys = Set(object.keys)
        switch kind {
        case "ipc":
            guard keys == ["kind", "request"], let request = object["request"] as? [String: Any],
                  let body = try? JSONSerialization.data(withJSONObject: request, options: [.sortedKeys])
            else { throw Failure.malformed }
            return .ipc(request: String(decoding: body, as: UTF8.self))
        case "release":
            guard keys == ["kind"] else { throw Failure.malformed }
            return .release
        case "foreground":
            guard keys == ["kind", "visible"], let visible = boolean(object["visible"]) else { throw Failure.malformed }
            return .foreground(visible: visible)
        case "route_probe":
            guard keys == ["kind", "ok"], let ok = boolean(object["ok"]) else { throw Failure.malformed }
            return .routeProbe(ok: ok)
        default:
            throw Failure.malformed
        }
    }

    private static func boolean(_ value: Any?) -> Bool? {
        guard let number = value as? NSNumber, CFGetTypeID(number) == CFBooleanGetTypeID() else { return nil }
        return number.boolValue
    }
}

/// The extension's answer to a non-ipc message (an ipc message is answered by the ipc-v1
/// response document itself).
public enum ProviderReply: Equatable, Sendable {
    /// The session is the app's again. The ticket is opaque to Swift and secret: memory only.
    case released(ticket: String)
    case ack
    case error(code: String)

    public func encoded() -> Data {
        let object: [String: String]
        switch self {
        case .released(let ticket): object = ["kind": "released", "ticket": ticket]
        case .ack: object = ["kind": "ack"]
        case .error(let code): object = ["kind": "error", "code": code]
        }
        return (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data(#"{"kind":"error","code":"invalid_request"}"#.utf8)
    }

    public static func decode(_ data: Data) throws -> ProviderReply {
        guard let object = try? JSONSerialization.jsonObject(with: data, options: []) as? [String: String],
              let kind = object["kind"] else { throw ProviderMessage.Failure.malformed }
        switch (kind, Set(object.keys)) {
        case ("released", ["kind", "ticket"]):
            guard let ticket = object["ticket"], !ticket.isEmpty else { throw ProviderMessage.Failure.malformed }
            return .released(ticket: ticket)
        case ("ack", ["kind"]):
            return .ack
        case ("error", ["kind", "code"]):
            return .error(code: object["code"] ?? "invalid_request")
        default:
            throw ProviderMessage.Failure.malformed
        }
    }

    /// What the extension answers to anything it does not understand.
    public static let invalidRequest = ProviderReply.error(code: "invalid_request")
}

/// The key under which the app passes the handoff ticket to startVPNTunnel(options:).
public enum TunnelStartOptions {
    public static let handoffKey = "handoff"
}
