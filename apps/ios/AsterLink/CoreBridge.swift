import AsterCore
import AsterLinkKit
import Foundation

/// The in-process Go Core (handoff_app_ios.md 4.1): ipc-v1 documents in and out, plus the
/// session handoff to and from the tunnel extension (handoff-v1). Every call blocks while the
/// Core works, so callers stay off the main thread for anything but hello.
enum CoreBridge {
    /// The code every session command gets while the Core is not started.
    static let notStartedCode = MobileCodeNotStarted

    /// One ipc-v1 request document in, one response document out.
    static func call(_ request: String) -> String { MobileCall(request) }

    static func hello() -> Result<CoreHello, Error> {
        let raw = call(CoreRequest.hello(requestID: "foundation-hello"))
        return Result {
            let response = try JSONDecoder().decode(CoreResponse.self, from: Data(raw.utf8))
            return try response.decodeResult(CoreHello.self)
        }
    }

    /// Hands this process's session out (the Core is "released" afterwards). The ticket is the
    /// session's bearer credential: memory only, never logged or stored.
    static func exportSession() -> Result<String, CoreFailure> {
        decode(MobileExportSession(), as: ExportedTicket.self).map(\.ticket)
    }

    /// Takes a session back from the extension (or the app's own ticket after a failed start).
    static func adoptSession(_ ticket: String) -> Result<SnapshotHead, CoreFailure> {
        decode(MobileAdoptSession(ticket), as: SnapshotHead.self).flatMap { head in
            head.actionError.map { .failure(CoreFailure(code: $0.code, message: $0.message)) } ?? .success(head)
        }
    }

    static func decode<T: Decodable>(_ raw: String, as type: T.Type) -> Result<T, CoreFailure> {
        guard let response = try? JSONDecoder().decode(CoreResponse.self, from: Data(raw.utf8)) else {
            return .failure(CoreFailure(code: "bad_response", message: "内核返回了无法识别的数据"))
        }
        if let failure = response.error { return .failure(CoreFailure(code: failure.code, message: failure.message)) }
        guard let value = try? response.decodeResult(T.self) else {
            return .failure(CoreFailure(code: "bad_response", message: "内核返回了无法识别的数据"))
        }
        return .success(value)
    }
}

/// A stable Core error code with its Chinese message.
struct CoreFailure: Error, Equatable {
    let code: String
    let message: String
}
