import Foundation
import Testing
@testable import AsterLinkKit

struct CoreProtocolTests {
    @Test func helloIdentityDecodes() throws {
        let body = #"{"request_id":"h1","protocol_version":1,"generation":0,"ok":true,"result":{"component":"asterlink-core","core_version":"0.1.0","protocol_version":1,"min_protocol_version":1,"max_protocol_version":1,"mihomo_revision":"70f0570405c3c2c47bb113b88db95006d239b346"}}"#
        let response = try JSONDecoder().decode(CoreResponse.self, from: Data(body.utf8))
        let hello = try response.decodeResult(CoreHello.self)
        #expect(response.requestID == "h1")
        #expect(hello.component == "asterlink-core")
        #expect(hello.coreVersion == "0.1.0")
        #expect(hello.mihomoRevision == "70f0570405c3c2c47bb113b88db95006d239b346")
    }

    @Test func aRefusalCarriesItsCodeAndNoResult() throws {
        let body = #"{"request_id":"r2","protocol_version":1,"generation":0,"ok":false,"error":{"code":"protocol_unsupported","message":"x"}}"#
        let response = try JSONDecoder().decode(CoreResponse.self, from: Data(body.utf8))
        #expect(response.error?.code == "protocol_unsupported")
        #expect(throws: CoreProtocolError.noResult("protocol_unsupported")) { try response.decodeResult(CoreHello.self) }
    }

    @Test func helloRequestIsValidJSON() throws {
        let object = try JSONSerialization.jsonObject(with: Data(CoreRequest.hello(requestID: "h9", generation: 3).utf8)) as? [String: Any]
        #expect(object?["command"] as? String == "hello")
        #expect(object?["generation"] as? Int == 3)
        #expect(object?["protocol_version"] as? Int == 1)
    }

    @Test func commandsEscapeUserInput() throws {
        let raw = CoreRequest.login(requestID: "l1", email: #"a"b@c.d"#, password: #"p\"}{"#, apiBase: "https://x/api/v1/client",
                                    channel: .release, deviceID: "33333333-3333-4333-8333-333333333333", appVersion: "0.1.0")
        let object = try #require(try JSONSerialization.jsonObject(with: Data(raw.utf8)) as? [String: Any])
        let payload = try #require(object["payload"] as? [String: String])
        #expect(object["command"] as? String == "login" && payload["password"] == #"p\"}{"# && payload["email"] == #"a"b@c.d"#)
        #expect(payload["channel"] == "release")
        let select = try #require(try JSONSerialization.jsonObject(with: Data(CoreRequest.selectNode(requestID: "s", generation: 4, nodeID: "n", catalogRevision: "r").utf8)) as? [String: Any])
        #expect(select["generation"] as? Int == 4 && (select["payload"] as? [String: String])?["catalog_revision"] == "r")
    }

    /// Every snapshot in the shared fixture decodes into the head the session plumbing reads.
    @Test func snapshotHeadsDecodeFromTheSharedFixture() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let data = try Data(contentsOf: root.appendingPathComponent("shared/contracts/snapshot-v1.json"))
        let fixture = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        let examples = try #require(fixture["snapshots"] as? [String: Any])
        #expect(examples.count >= 5)
        for (name, example) in examples {
            let body = try JSONSerialization.data(withJSONObject: example)
            let head = try JSONDecoder().decode(SnapshotHead.self, from: body)
            #expect(!head.state.isEmpty, "\(name)")
        }
        let released = try JSONDecoder().decode(SnapshotHead.self, from: Data(#"{"generation":9,"state":"released","user":null,"nodes":[]}"#.utf8))
        #expect(released.state == "released" && released.actionError == nil)
    }
}
