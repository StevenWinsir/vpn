import AsterCore
import AsterLinkKit
import Foundation

/// The in-process Go Core (handoff_app_ios.md 4.1). Round 1 only asks hello, which needs no
/// running session; login, the catalog and the tunnel hand-off arrive in rounds 2 and 3.
enum CoreBridge {
    /// The code every session command gets while the Core is not started.
    static let notStartedCode = MobileCodeNotStarted

    /// One ipc-v1 request document in, one response document out. Blocks while the command
    /// runs, so callers stay off the main thread for anything but hello.
    static func call(_ request: String) -> String { MobileCall(request) }

    static func hello() -> Result<CoreHello, Error> {
        let raw = call(CoreRequest.hello(requestID: "foundation-hello"))
        return Result {
            let response = try JSONDecoder().decode(CoreResponse.self, from: Data(raw.utf8))
            return try response.decodeResult(CoreHello.self)
        }
    }
}
