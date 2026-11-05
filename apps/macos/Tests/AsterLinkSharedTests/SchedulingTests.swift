import Darwin
import XCTest
@testable import AsterLinkShared

/// handoff_fix.md F1: the Helper reports a launchd scheduling clamp and the app treats it like a
/// stale Helper (reinstall), without ever refusing a Helper that cannot tell.
final class SchedulingTests: XCTestCase {
    func testClassificationMatchesTheMeasuredBands() {
        // Measured on macOS 26 (handoff_fix.md section 12, round 1): no ProcessType -> 20,
        // Adaptive/Background idle -> 4, Interactive or a user-started process -> 31.
        XCTAssertTrue(SchedulingProbe.isLimited(basePriority: 4))
        XCTAssertTrue(SchedulingProbe.isLimited(basePriority: 20))
        XCTAssertFalse(SchedulingProbe.isLimited(basePriority: 31))
        XCTAssertFalse(SchedulingProbe.isLimited(basePriority: 47))
    }

    func testOwnAndChildProcessesAreReadable() throws {
        // Whatever band the test runner itself is in (a CI runner may be clamped), the probe must read it.
        XCTAssertNotNil(SchedulingProbe.basePriority())
        let child = Process()
        child.executableURL = URL(fileURLWithPath: "/bin/sleep")
        child.arguments = ["2"]
        try child.run()
        defer { child.terminate() }
        let childPriority = try XCTUnwrap(SchedulingProbe.basePriority(of: child.processIdentifier))
        // A child inherits the parent's clamp, never a better band (the reason F1 reaches the Core).
        XCTAssertLessThanOrEqual(childPriority, max(SchedulingProbe.basePriority() ?? 31, 31))
    }

    func testUnreadableProcessIsNeverReportedLimited() {
        XCTAssertNil(SchedulingProbe.basePriority(of: -1))
        XCTAssertNil(SchedulingProbe.isLimited(pid: -1))
    }

    func testLimitedHelperNeedsTheSameReinstallAsAStaleOne() {
        let verdict = HelperUpgradePolicy.verdict(helperVersion: "1.2.3", appVersion: "1.2.3", schedulingLimited: true)
        XCTAssertEqual(verdict, .schedulingLimited)
        XCTAssertEqual(HelperUpgradePolicy.userMessage(for: verdict),
                       HelperUpgradePolicy.userMessage(for: .mismatch(helper: "1.0.0", app: "1.2.3")))
        // A version mismatch wins: it names the more fundamental problem.
        XCTAssertEqual(HelperUpgradePolicy.verdict(helperVersion: "1.0.0", appVersion: "1.2.3", schedulingLimited: true),
                       .mismatch(helper: "1.0.0", app: "1.2.3"))
    }

    func testUnknownSchedulingIsCompatible() {
        // A Helper that could not read its priority (or predates the field) is not blocked for it.
        XCTAssertEqual(HelperUpgradePolicy.verdict(helperVersion: "1.2.3", appVersion: "1.2.3", schedulingLimited: nil), .compatible)
        XCTAssertEqual(HelperUpgradePolicy.verdict(helperVersion: "1.2.3", appVersion: "1.2.3", schedulingLimited: false), .compatible)
    }

    func testReplyFieldIsOptionalOnTheWire() throws {
        let old = #"{"ok":true,"helper_version":"0.1.0","protocol_version":1,"worker_running":false}"#
        let decoded = try JSONDecoder().decode(HelperReply.self, from: Data(old.utf8))
        XCTAssertNil(decoded.schedulingLimited)
        let limited = HelperReply(ok: true, helperVersion: "0.1.1", workerRunning: true, schedulingLimited: true)
        let text = String(decoding: limited.encoded(), as: UTF8.self)
        XCTAssertTrue(text.contains(#""scheduling_limited":true"#), text)
        XCTAssertFalse(String(decoding: HelperReply(ok: true, helperVersion: "0.1.1", workerRunning: false).encoded(), as: UTF8.self)
            .contains("scheduling_limited"))
    }

    func testClampRefusesUntilReinstalledForThisVersion() {
        let limited = HelperUpgradePolicy.Verdict.schedulingLimited
        XCTAssertTrue(HelperUpgradePolicy.refusesConnect(limited, reinstalledForVersion: nil, appVersion: "0.1.1"))
        XCTAssertTrue(HelperUpgradePolicy.refusesConnect(limited, reinstalledForVersion: "0.1.0", appVersion: "0.1.1"))
        XCTAssertFalse(HelperUpgradePolicy.refusesConnect(limited, reinstalledForVersion: "0.1.1", appVersion: "0.1.1"))
    }

    func testStaleVersionAlwaysRefusesAndCompatibleNever() {
        let stale = HelperUpgradePolicy.Verdict.mismatch(helper: "0.1.0", app: "0.1.1")
        XCTAssertTrue(HelperUpgradePolicy.refusesConnect(stale, reinstalledForVersion: "0.1.1", appVersion: "0.1.1"))
        XCTAssertFalse(HelperUpgradePolicy.refusesConnect(.compatible, reinstalledForVersion: nil, appVersion: "0.1.1"))
    }
}
