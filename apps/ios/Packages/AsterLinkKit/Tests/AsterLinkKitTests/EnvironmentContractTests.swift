import Foundation
import Testing
@testable import AsterLinkKit

/// shared/contracts/env-v1.json, read in place so the iOS parser can never drift from the
/// copy macOS, Windows, Android and scripts/native_env.py are tested against.
struct EnvironmentContractTests {
    struct Contract: Decodable {
        struct Case: Decodable {
            let name: String
            let env: String
            let ok: Bool
        }
        let cases: [Case]
    }

    static func contract() throws -> Contract {
        // Each call drops one component: this file, then the directories up to the repository root.
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent() // -> Tests/AsterLinkKitTests
            .deletingLastPathComponent() // -> Tests
            .deletingLastPathComponent() // -> AsterLinkKit
            .deletingLastPathComponent() // -> Packages
            .deletingLastPathComponent() // -> apps/ios
            .deletingLastPathComponent() // -> apps
            .deletingLastPathComponent() // -> repository root
        let data = try Data(contentsOf: root.appendingPathComponent("shared/contracts/env-v1.json"))
        return try JSONDecoder().decode(Contract.self, from: data)
    }

    @Test func everyContractCaseIsAcceptedOrRejectedExactly() throws {
        let cases = try Self.contract().cases
        #expect(cases.count >= 16, "the contract must not be empty")
        for item in cases {
            let parsed = try? ClientEnvironment.parse(item.env)
            #expect((parsed != nil) == item.ok, "\(item.name)")
        }
    }
}
