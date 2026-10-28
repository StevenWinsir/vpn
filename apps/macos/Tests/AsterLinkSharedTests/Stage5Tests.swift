import XCTest
@testable import AsterLinkShared

final class Stage5Tests: XCTestCase {
    func testMatchingVersionsAreCompatible() {
        XCTAssertEqual(HelperUpgradePolicy.verdict(helperVersion: "1.2.3", appVersion: "1.2.3"), .compatible)
        XCTAssertNil(HelperUpgradePolicy.userMessage(for: .compatible))
    }

    func testStaleHelperIsRefusedWithActionableMessage() {
        let verdict = HelperUpgradePolicy.verdict(helperVersion: "1.0.0", appVersion: "1.1.0")
        XCTAssertEqual(verdict, .mismatch(helper: "1.0.0", app: "1.1.0"))
        let message = HelperUpgradePolicy.userMessage(for: verdict) ?? ""
        // User-facing copy (handoff_app_standardization.md 7.3): the action, not the internals or version pair.
        XCTAssertTrue(message.contains("后台服务") && message.contains("重新安装"))
        XCTAssertFalse(message.contains("特权") || message.contains("1.0.0"))
    }

    func testDevVersionNeverMatchesARealRelease() {
        XCTAssertNotEqual(HelperUpgradePolicy.verdict(helperVersion: "0.0.0-dev", appVersion: "1.0.0"), .compatible)
    }

    func testServiceChangesOnlyWhenNoTrafficCanFlow() {
        let idle: [CoreState] = [.signedOut, .authenticating, .ready, .blocked, .error]
        let active: [CoreState] = [.preparing, .connecting, .connected, .stopping]
        for state in idle { XCTAssertTrue(HelperUpgradePolicy.mayChangeService(whileState: state), "\(state)") }
        for state in active { XCTAssertFalse(HelperUpgradePolicy.mayChangeService(whileState: state), "\(state)") }
    }
}
