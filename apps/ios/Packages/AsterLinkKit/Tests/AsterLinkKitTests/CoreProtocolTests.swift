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
}
