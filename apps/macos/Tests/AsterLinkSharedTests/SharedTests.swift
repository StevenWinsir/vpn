import Foundation
import XCTest
@testable import AsterLinkShared

private func repoRoot() -> URL {
    // apps/macos/Tests/AsterLinkSharedTests/SharedTests.swift -> repo root
    URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
}

private func fixture(_ name: String) throws -> [String: Any] {
    let data = try Data(contentsOf: repoRoot().appendingPathComponent("shared/contracts/\(name)"))
    return try JSONSerialization.jsonObject(with: data) as! [String: Any]
}

final class EnvironmentContractTests: XCTestCase {
    func testSharedFixtureCases() throws {
        let cases = try XCTUnwrap(fixture("env-v1.json")["cases"] as? [[String: Any]])
        XCTAssertGreaterThanOrEqual(cases.count, 16)
        for item in cases {
            let name = item["name"] as! String
            let text = item["env"] as! String
            if item["ok"] as! Bool {
                XCTAssertNoThrow(try ClientEnvironment.parse(text), name)
            } else {
                XCTAssertThrowsError(try ClientEnvironment.parse(text), name)
            }
        }
    }

    func testParsedValues() throws {
        let env = try ClientEnvironment.parse("APP_ENV=release\nAPI_BASE_URL=https://demo.hyshentou.cn/api/v1/client\nWEBSITE_BASE_URL=https://test.hyshentou.cn\n")
        XCTAssertEqual(env.channel, .release)
        XCTAssertEqual(env.apiBase.absoluteString, "https://demo.hyshentou.cn/api/v1/client")
    }
}

final class SigningPolicyTests: XCTestCase {
    func testReleaseRequiresAppleAnchorAndTeam() throws {
        let policy = try SigningPolicy(channel: .release, teamID: "ABCDE12345")
        let requirement = try policy.requirement(forIdentifier: AsterLinkIdentity.appBundleID)
        XCTAssertEqual(requirement,
            "anchor apple generic and identifier \"com.asterlink.vpn\" and certificate leaf[subject.OU] = \"ABCDE12345\"")
    }

    func testReleaseWithoutTeamIsRejected() {
        XCTAssertThrowsError(try SigningPolicy(channel: .release, teamID: nil))
    }

    func testMalformedTeamAndIdentifierAreRejected() throws {
        XCTAssertThrowsError(try SigningPolicy(channel: .development, teamID: "abc\" or true"))
        let policy = try SigningPolicy(channel: .development, teamID: nil)
        XCTAssertThrowsError(try policy.requirement(forIdentifier: "x\" or identifier \"y"))
        XCTAssertEqual(try policy.requirement(forIdentifier: "com.asterlink.vpn"), "identifier \"com.asterlink.vpn\"")
    }
}

final class FrameTests: XCTestCase {
    func testRoundTripAndLimits() throws {
        let frame = try IPCFrame.encode(Data("{}".utf8))
        XCTAssertEqual(Array(frame.prefix(4)), [0, 0, 0, 2])
        XCTAssertEqual(try IPCFrame.decode(frame), Data("{}".utf8))
        XCTAssertThrowsError(try IPCFrame.encode(Data()))
        XCTAssertThrowsError(try IPCFrame.encode(Data(count: AsterLinkIdentity.maxFrame + 1)))
        XCTAssertThrowsError(try IPCFrame.declaredLength(header: Data([0, 0, 0, 0])))
        XCTAssertThrowsError(try IPCFrame.declaredLength(header: Data([0xff, 0xff, 0xff, 0xff])))
        XCTAssertThrowsError(try IPCFrame.decode(frame.dropLast()))
    }

    func testFixtureIsWellFormedAndProtocolMatches() throws {
        let contract = try fixture("ipc-v1.json")
        XCTAssertEqual(contract["protocol_version"] as? Int, AsterLinkIdentity.protocolVersion)
        let commands = contract["commands"] as! [String]
        XCTAssertTrue(commands.contains("hello") && commands.contains("shutdown"))
        // Every fixture request that is meant to be accepted must be what we would send.
        let request = try JSONEncoder().encode(CoreRequest(requestID: "r1", command: "hello", generation: 7))
        let object = try JSONSerialization.jsonObject(with: request) as! [String: Any]
        XCTAssertEqual(Set(object.keys), ["request_id", "protocol_version", "generation", "command"])
    }

    func testDecodesCoreReply() throws {
        let json = """
        {"request_id":"a","protocol_version":1,"generation":0,"ok":true,\
        "result":{"component":"asterlink-core","core_version":"1","protocol_version":1,\
        "min_protocol_version":1,"max_protocol_version":1,"mihomo_revision":"abc"}}
        """
        let response = try JSONDecoder().decode(CoreResponse.self, from: Data(json.utf8))
        XCTAssertTrue(response.ok)
        XCTAssertEqual(response.result?.mihomoRevision, "abc")
    }
}

final class WorkerStoreTests: XCTestCase {
    private var directory: URL!
    private var store: WorkerStore!
    private var source: URL!
    private var workerHash: String!

    override func setUpWithError() throws {
        directory = FileManager.default.temporaryDirectory.appendingPathComponent("asterlink-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        source = directory.appendingPathComponent("core-src")
        try Data("worker-bytes".utf8).write(to: source)
        workerHash = try WorkerStore.sha256Hex(of: source)
        store = WorkerStore(root: directory.appendingPathComponent("Workers"), requiredOwner: getuid())
    }

    override func tearDownWithError() throws { try? FileManager.default.removeItem(at: directory) }

    func testInstallProducesValidatedCopy() throws {
        let installed = try store.install(source: source, version: "1.0.0", expectedSHA256: workerHash)
        XCTAssertNoThrow(try store.validate(installed, expectedSHA256: workerHash))
        var info = stat(); lstat(installed.path, &info)
        XCTAssertEqual(info.st_mode & 0o777, 0o755)
    }

    func testWrongHashIsRejectedBeforeAnythingIsInstalled() {
        XCTAssertThrowsError(try store.install(source: source, version: "1.0.0", expectedSHA256: String(repeating: "0", count: 64))) {
            XCTAssertEqual($0 as? WorkerValidationError, .hashMismatch)
        }
        XCTAssertFalse(FileManager.default.fileExists(atPath: store.root.appendingPathComponent("1.0.0").path))
    }

    func testTamperedInstalledCopyIsRejected() throws {
        let installed = try store.install(source: source, version: "1.0.0", expectedSHA256: workerHash)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: installed.path)
        try Data("evil".utf8).write(to: installed)
        XCTAssertThrowsError(try store.validate(installed, expectedSHA256: workerHash))
    }

    func testSymlinkComponentIsRejected() throws {
        let installed = try store.install(source: source, version: "1.0.0", expectedSHA256: workerHash)
        let elsewhere = directory.appendingPathComponent("elsewhere")
        try FileManager.default.createDirectory(at: elsewhere, withIntermediateDirectories: true)
        try FileManager.default.copyItem(at: installed, to: elsewhere.appendingPathComponent("asterlink-core"))
        try FileManager.default.createSymbolicLink(at: store.root.appendingPathComponent("2.0.0"), withDestinationURL: elsewhere)
        let viaLink = store.root.appendingPathComponent("2.0.0/asterlink-core")
        XCTAssertThrowsError(try store.validate(viaLink, expectedSHA256: workerHash)) {
            guard case .symlink = $0 as! WorkerValidationError else { return XCTFail("\($0)") }
        }
    }

    func testGroupOrWorldWritableIsRejected() throws {
        let installed = try store.install(source: source, version: "1.0.0", expectedSHA256: workerHash)
        try FileManager.default.setAttributes([.posixPermissions: 0o775], ofItemAtPath: installed.path)
        XCTAssertThrowsError(try store.validate(installed, expectedSHA256: workerHash))
    }

    func testWrongOwnerIsRejected() throws {
        let installed = try store.install(source: source, version: "1.0.0", expectedSHA256: workerHash)
        let strict = WorkerStore(root: store.root, requiredOwner: 0)
        if getuid() != 0 { XCTAssertThrowsError(try strict.validate(installed, expectedSHA256: workerHash)) }
    }

    func testUnsafeVersionStringsAreRejected() {
        for bad in ["../x", "", ".hidden", "a/b", "v 1"] {
            XCTAssertThrowsError(try store.installedURL(version: bad), bad)
        }
    }
}
