import AsterLinkKit
import Foundation
import Testing
@testable import AsterLink

/// Round 2 in the simulator, through the real linked Core: the handoff functions answer with
/// stable codes (shared/contracts/handoff-v1.json; the session semantics are Go-tested), and the
/// app addresses the tunnel extension it actually embeds. The tunnel itself cannot run in the
/// simulator (E-SIM-NE); its data path is a device test. An extension of CoreBindingTests so it
/// is serialised with the other tests that start and stop the process-wide Core.
extension CoreBindingTests {
    @Test func handoffAnswersThroughTheLinkedCore() throws {
        CoreLifecycle.stop()
        #expect(CoreBridge.exportSession() == .failure(CoreFailure(code: CoreBridge.notStartedCode, message: "core is not running")))

        try CoreLifecycle.start()
        defer { CoreLifecycle.stop() }
        guard case .failure(let export) = CoreBridge.exportSession() else { Issue.record("export without a session"); return }
        #expect(export.code == "not_signed_in" && !export.message.isEmpty)
        for ticket in ["", "ticket", #"{"version":1}"#] {
            guard case .failure(let adopt) = CoreBridge.adoptSession(ticket) else { Issue.record("adopted \(ticket)"); continue }
            #expect(adopt.code == "handoff_invalid", "\(ticket)")
        }
        // A refused adoption leaves the Core signed out and usable.
        let head = CoreBridge.decode(CoreBridge.call(CoreRequest.command("read_snapshot", requestID: "s")), as: SnapshotHead.self)
        #expect((try? head.get())?.state == "signed_out")
    }

    @MainActor @Test func theAppAddressesTheExtensionItEmbeds() throws {
        let plugins = try #require(Bundle.main.builtInPlugInsURL)
        let info = try #require(NSDictionary(contentsOf: plugins.appendingPathComponent("AsterLinkTunnel.appex/Info.plist")))
        #expect(info["CFBundleIdentifier"] as? String == TunnelController.providerBundleID)
        let point = (info["NSExtension"] as? [String: Any])?["NSExtensionPointIdentifier"] as? String
        #expect(point == "com.apple.networkextension.packet-tunnel")
    }
}
