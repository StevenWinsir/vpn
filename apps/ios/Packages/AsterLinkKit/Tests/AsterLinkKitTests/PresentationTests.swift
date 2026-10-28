import Foundation
import Testing
@testable import AsterLinkKit

struct PresentationTests {
    static func fixture() throws -> [String: Data] {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let data = try Data(contentsOf: root.appendingPathComponent("shared/contracts/snapshot-v1.json"))
        let object = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        let snapshots = try #require(object["snapshots"] as? [String: Any])
        return try snapshots.mapValues { try JSONSerialization.data(withJSONObject: $0) }
    }

    static func snapshot(_ name: String) throws -> CoreSnapshot {
        try JSONDecoder().decode(CoreSnapshot.self, from: try #require(try fixture()[name]))
    }

    // Node groups round 6: the online-presence codes are real server codes (native-v3.json); each
    // is a refusal shown in the Core's words that sends nobody to buy or update.
    @Test func onlinePresenceRefusalsShowTheCoreTextAndOfferNoPurchase() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let data = try Data(contentsOf: root.appendingPathComponent("shared/contracts/native-v3.json"))
        let v3 = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        let codes = Set(try #require(v3["errors"] as? [String: Any]).keys).union(try #require(v3["session_reasons"] as? [String: Any]).keys)
        let ready = try Self.snapshot("ready_with_selection")
        for code in ["device_limit", "group_full", "group_unavailable", "grant_required", "connection_not_found", "connection_expired", "connection_reassigned"] {
            #expect(codes.contains(code), "\(code)")
            let view = ConnectionView.from(ready.with { $0.lastError = ErrorView(code: code, message: "core text") }, busy: false, activity: .none)
            #expect(view.failure == .core(code, "core text") && view.canConnect && !view.needsPurchase && !view.needsUpdate, "\(code)")
        }
        let moving = ConnectionView.from(ready.with { $0.state = "preparing"; $0.lastError = ErrorView(code: "connection_reassigned", message: "x") }, busy: false, activity: .none)
        #expect(moving.status == .preparing && !moving.showConnect)
    }

    // E02: the shared snapshot fixture decodes into the full view model.
    @Test func everySharedSnapshotDecodes() throws {
        let all = try Self.fixture()
        #expect(all.count >= 6)
        for (name, data) in all { _ = try #require(try? JSONDecoder().decode(CoreSnapshot.self, from: data), "\(name)") }
        let connected = try Self.snapshot("connected_metering")
        #expect(connected.metering?.uploadBytes == 22_020_096 && connected.metering?.history.count == 3)
        #expect(connected.entitlement?.usedUnits == 104_857_600_000 && connected.selectedNode?.name == "HK Dedicated")
        let gated = try Self.snapshot("connected_gated")
        #expect(gated.gate?.waitingNetwork == true && gated.lastGate?.totalClosures == 2)
    }

    @Test func countersAbove2To53StayExact() throws {
        let body = #"{"generation":1,"state":"connected","metering":{"upload_bytes":9007199254740993,"download_bytes":9223372036854775000,"rate_permille":1000}}"#
        let snapshot = try JSONDecoder().decode(CoreSnapshot.self, from: Data(body.utf8))
        #expect(snapshot.metering?.uploadBytes == 9_007_199_254_740_993)
        #expect(snapshot.metering?.downloadBytes == 9_223_372_036_854_775_000)
    }

    @Test func actionErrorIsSplitOffTheSnapshot() throws {
        let body = #"{"generation":2,"state":"signed_out","action_error":{"code":"invalid_credentials","message":"邮箱或密码错误"}}"#
        let reply = try SnapshotReply.parse(Data(body.utf8))
        #expect(!reply.succeeded && reply.actionError?.code == "invalid_credentials" && reply.snapshot.state == "signed_out")
        let ok = try SnapshotReply.parse(Data(#"{"generation":2,"state":"ready","action_error":null}"#.utf8))
        #expect(ok.succeeded)
    }

    @Test func formattingIsIntegerExact() {
        #expect(Formatting.rate(500) == "0.5×" && Formatting.rate(1000) == "1×" && Formatting.rate(1250) == "1.25×" && Formatting.rate(2000) == "2×")
        #expect(Formatting.bytes(0) == "0 B" && Formatting.bytes(1536) == "1.50 KB" && Formatting.bytes(1 << 30) == "1.00 GB")
        // 20 MiB up + 80 MiB down = 104,857,600 bytes: 1× bills all of it, 0.5× half.
        #expect(Formatting.billedBytes(physical: 104_857_600, ratePermille: 1000) == 104_857_600)
        #expect(Formatting.billedBytes(physical: 104_857_600, ratePermille: 500) == 52_428_800)
        #expect(Formatting.billedBytes(physical: 999, ratePermille: 500) == 499)
        #expect(Formatting.billedBytes(physical: 9_007_199_254_740_993, ratePermille: 1000) == 9_007_199_254_740_993)
        #expect(Formatting.elapsed(milliseconds: 751_000) == "00:12:31" && Formatting.elapsed(milliseconds: 93_784_000) == "1:02:03:04")
        #expect(Formatting.initial(name: "", email: "test@test.com") == "T" && Formatting.initial(name: "王", email: "") == "王")
        #expect(Formatting.usedFraction(totalBytes: 100, remainingBytes: 25) == 0.75 && Formatting.usedFraction(totalBytes: 0, remainingBytes: 0) == 0)
    }

    @Test func datesParseWithAndWithoutFractionsAndCountDaysUp() {
        let plain = Formatting.parseInstant("2030-01-01T00:10:00Z")
        let fraction = Formatting.parseInstant("2030-01-01T00:10:00.123456789Z")
        #expect(plain != nil && fraction != nil && Formatting.parseInstant("") == nil && Formatting.parseInstant("nope") == nil)
        let utc = TimeZone(identifier: "UTC")!
        #expect(Formatting.date(plain, timeZone: utc) == "2030-01-01" && Formatting.dateTime(plain, timeZone: utc) == "2030-01-01 00:10")
        let now = Date(timeIntervalSince1970: 1_000)
        #expect(Formatting.daysLeft(Date(timeIntervalSince1970: 1_001), now: now) == 1)
        #expect(Formatting.daysLeft(Date(timeIntervalSince1970: 1_000 + 86_400), now: now) == 1)
        #expect(Formatting.daysLeft(Date(timeIntervalSince1970: 1_001 + 86_400), now: now) == 2)
        #expect(Formatting.daysLeft(Date(timeIntervalSince1970: 500), now: now) == 0 && Formatting.daysLeft(nil, now: now) == nil)
    }

    // A02: the verdict comes from the Core's entitlement; an administrator without a plan cannot connect.
    @Test func connectionViewFollowsTheCoresVerdict() throws {
        let noPlan = ConnectionView.from(try Self.snapshot("blocked_no_plan"), busy: false, activity: .none)
        #expect(noPlan.status == .unavailable && !noPlan.canConnect && noPlan.needsPurchase && noPlan.tone == .bad)
        #expect(noPlan.blocker?.code == "upgrade_required" && noPlan.blocker?.text == "当前账号没有有效套餐，请前往网站购买")

        let unselected = ConnectionView.from(try Self.snapshot("ready_with_selection").with { $0.selectedNodeID = "" }, busy: false, activity: .none)
        #expect(!unselected.canConnect && unselected.blocker == .local(LocalCodes.selectLine) && !unselected.needsPurchase)

        let ready = ConnectionView.from(try Self.snapshot("ready_with_selection"), busy: false, activity: .none)
        #expect(ready.canConnect && ready.status == .disconnected && ready.showConnect && ready.blocker == nil)

        let connected = ConnectionView.from(try Self.snapshot("connected_metering"), busy: false, activity: .none)
        #expect(connected.status == .connected && connected.showDisconnect && connected.tone == .good)

        let gated = ConnectionView.from(try Self.snapshot("connected_gated"), busy: false, activity: .none)
        #expect(gated.status == .waitingNetwork && gated.showDisconnect && gated.showProgress)
        let reconfirming = ConnectionView.from(try Self.snapshot("connected_gated").with { $0.gate?.waitingNetwork = false }, busy: false, activity: .none)
        #expect(reconfirming.status == .reconfirming)

        // While the connect command is in flight the snapshot still says "ready".
        let connecting = ConnectionView.from(try Self.snapshot("ready_with_selection"), busy: true, activity: .connecting)
        #expect(connecting.status == .preparing && connecting.cancellable && !connecting.canConnect)
        let stopping = ConnectionView.from(try Self.snapshot("connected_metering"), busy: true, activity: .disconnecting)
        #expect(stopping.status == .stopping && !stopping.cancellable)
    }

    @Test func updateRequiredIsRecognisedFromTheFailure() {
        var snapshot = vipSnapshot(selected: "a")
        snapshot.lastError = ErrorView(code: ConnectionView.updateRequired, message: "请升级应用")
        let view = ConnectionView.from(snapshot, busy: false, activity: .none)
        #expect(view.needsUpdate && view.failure?.code == "client_upgrade_required")
    }

    @Test func lineListSearchesFiltersAndGroupsInServerOrder() throws {
        let snapshot = try Self.snapshot("ready_with_selection")
        let label: (Bool) -> String = { ErrorText.lineLabel(dedicated: $0) }
        let all = NodeList.build(snapshot, query: "", filter: .all, lineLabel: label)
        #expect(all.map(\.region) == ["HK", "JP"] && all[0].rows[0].selected && all[0].rows[0].configReady && all[0].rows[0].dedicated)
        #expect(NodeList.build(snapshot, query: "jp", filter: .all, lineLabel: label).flatMap(\.rows).map(\.name) == ["JP Standard"])
        #expect(NodeList.build(snapshot, query: "专线", filter: .all, lineLabel: label).flatMap(\.rows).map(\.name) == ["HK Dedicated"])
        #expect(NodeList.build(snapshot, query: "", filter: .direct, lineLabel: label).flatMap(\.rows).map(\.name) == ["JP Standard"])
        #expect(NodeList.emptyReason(snapshot, filtered: false) == nil && NodeList.emptyReason(snapshot, filtered: true) == LocalCodes.noMatch)
        #expect(NodeList.emptyReason(try Self.snapshot("blocked_no_plan"), filtered: false) == LocalCodes.noPlan)
        #expect(NodeList.emptyReason(vipSnapshot(nodes: false), filtered: false) == LocalCodes.syncing)
        var none = vipSnapshot(nodes: false)
        none.catalogRevision = "rev"
        #expect(NodeList.emptyReason(none, filtered: false) == LocalCodes.noLines)
    }

    @Test func aConnectionCountsFromItsOwnBaselineAtItsOwnRate() throws {
        let before = try Self.snapshot("ready_with_selection")
        let connected = try Self.snapshot("connected_metering")
        let baseline = ConnectionUsage.baseline(before: before, connected: connected)
        let usage = try #require(ConnectionUsage.of(connected, baseline: baseline))
        // Client-reported counters continue from the snapshot before the connect (0 here).
        #expect(usage.physicalBytes == 22_020_096 + 83_886_080 && usage.billedBytes == usage.physicalBytes)
        let node = try Self.snapshot("connected_node_reported")
        #expect(ConnectionUsage.baseline(before: connected, connected: node) == MeterSample())
        #expect(ConnectionUsage.of(before, baseline: nil) == nil)
    }

    @Test func noticeTextPrefersTheCoresOwnWords() {
        #expect(ErrorText.text(for: .core("upgrade_required", "请购买")) == "请购买")
        #expect(ErrorText.text(for: .local(LocalCodes.noPlan)).contains("套餐"))
        #expect(ErrorText.text(for: .local("something_new")) == "操作失败，请稍后重试")
        #expect(ErrorText.status(.waitingNetwork) == "等待网络" && ErrorText.status(.stopping) == "正在断开")
    }

    // handoff_app_standardization.md D9: the report a user pastes to support has exactly these lines and
    // nothing about what the app is built from or which server it talks to.
    @Test func diagnosticsReportHoldsOnlyTheFiveCustomerFacingFacts() throws {
        var state = SessionState()
        state.phase = .signedIn
        state.snapshot = try Self.snapshot("ready_with_selection").with { $0.lastError = ErrorView(code: "tunnel_unhealthy") }
        state.identity = try JSONDecoder().decode(CoreHello.self, from: Data(
            #"{"component":"asterlink-core","core_version":"9.9.9","protocol_version":1,"min_protocol_version":1,"max_protocol_version":1,"mihomo_revision":"abcdef0123456789abcdef"}"#.utf8))
        let report = Diagnostics.report(appVersion: "1.2.3", osVersion: "iOS 18.0", state: state, now: Date(timeIntervalSince1970: 0))
        let lines = report.split(separator: "\n").map(String.init)
        #expect(lines.count == 6 && lines[0] == "AsterLink 诊断信息")
        #expect(lines[2] == "应用版本: 1.2.3" && lines[3] == "系统版本: iOS 18.0")
        #expect(lines[4].hasPrefix("连接状态: ") && lines[5] == "错误码: tunnel_unhealthy")
        for forbidden in ["9.9.9", "abcdef", "Core", "Mihomo", "generation", "隧道", "代次"] { #expect(!report.contains(forbidden), "\(forbidden)") }
        state.snapshot.lastError = nil
        #expect(Diagnostics.report(appVersion: "1", osVersion: "iOS 18.0", state: state).hasSuffix("错误码: 无"))
    }

    @Test func accountKeysAreScopedAndNeverHoldTheAddress() {
        let a = CredentialAccount.key(apiBase: "https://x/api/v1/client/", email: " A@B.C ")
        #expect(a == CredentialAccount.key(apiBase: "https://x/api/v1/client", email: "a@b.c"))
        #expect(a != CredentialAccount.key(apiBase: "https://y/api/v1/client", email: "a@b.c"))
        #expect(a.count == 64 && !a.contains("@"))
    }
}

extension CoreSnapshot {
    func with(_ change: (inout CoreSnapshot) -> Void) -> CoreSnapshot {
        var copy = self
        change(&copy)
        return copy
    }
}
