import Foundation
import Testing
@testable import AsterLink

/// The linked Go Core answers the shared ipc-v1 contract, starts and stops (handoff_app_ios.md
/// round 1; the same steps as apps/android CoreBindingTest). Runs in the iOS Simulator, whose
/// processes see the Mac's file system, so the fixture is read in place via #filePath.
/// The test bundle reaches the Core only through the host app (BUNDLE_LOADER): linking the Go
/// archive into the bundle as well would put two Go runtimes in one process.
@Suite(.serialized)
struct CoreBindingTests {
    struct Contract: Decodable {
        struct Case: Decodable {
            struct Expect: Decodable {
                let ok: Bool
                let requestID: String
                let generation: UInt64?
                let errorCode: String?
                enum CodingKeys: String, CodingKey { case ok, generation, requestID = "request_id", errorCode = "error_code" }
            }
            let name: String
            let request: String
            let padding: Int?
            let expect: Expect
            enum CodingKeys: String, CodingKey { case name, request, expect, padding = "request_padding_bytes" }
        }
        let cases: [Case]
    }

    static func contract() throws -> Contract {
        // This file, AsterLinkTests, apps/ios, apps -> repository root.
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let data = try Data(contentsOf: root.appendingPathComponent("shared/contracts/ipc-v1.json"))
        return try JSONDecoder().decode(Contract.self, from: data)
    }

    struct Response: Decodable {
        struct Failure: Decodable { let code: String }
        struct Result: Decodable {
            let coreVersion: String?
            let mihomoRevision: String?
            let state: String?
            enum CodingKeys: String, CodingKey { case state, coreVersion = "core_version", mihomoRevision = "mihomo_revision" }
        }
        let ok: Bool
        let requestID: String
        let generation: UInt64
        let error: Failure?
        let result: Result?
        enum CodingKeys: String, CodingKey { case ok, generation, error, result, requestID = "request_id" }
    }

    static func call(_ request: String) throws -> Response {
        try JSONDecoder().decode(Response.self, from: Data(CoreBridge.call(request).utf8))
    }

    @Test func theLinkedCoreAnswersTheContractStartsAndStops() throws {
        CoreLifecycle.stop()

        // 1. Every shared case is decided before a session handler is reached, except the one
        //    naming a session command: before Start that one says the Core is not running.
        let cases = try Self.contract().cases
        #expect(cases.count >= 8)
        for item in cases {
            let request = item.request.replacingOccurrences(of: "%PAD%", with: String(repeating: "a", count: item.padding ?? 0))
            let response = try Self.call(request)
            #expect(response.ok == item.expect.ok, "\(item.name)")
            #expect(response.requestID == item.expect.requestID, "\(item.name)")
            #expect(response.generation == (item.expect.generation ?? 0), "\(item.name)")
            let wanted = item.expect.errorCode == "not_implemented" ? CoreBridge.notStartedCode : item.expect.errorCode
            #expect(response.error?.code == wanted, "\(item.name)")
        }

        // 2. Started: the build identity is this build's, and the session is signed out.
        try CoreLifecycle.start()
        defer { CoreLifecycle.stop() }
        let hello = try Self.call(#"{"request_id":"h","protocol_version":1,"generation":0,"command":"hello"}"#)
        #expect(hello.ok)
        #expect(hello.result?.coreVersion == "0.1.0")
        #expect(hello.result?.mihomoRevision?.count == 40)
        let snapshot = try Self.call(#"{"request_id":"s","protocol_version":1,"generation":0,"command":"read_snapshot"}"#)
        #expect(snapshot.ok)
        #expect(snapshot.result?.state == "signed_out")

        // 3. Stopped: session commands are refused again.
        CoreLifecycle.stop()
        let after = try Self.call(#"{"request_id":"a","protocol_version":1,"generation":0,"command":"read_snapshot"}"#)
        #expect(after.error?.code == CoreBridge.notStartedCode)
    }
}
