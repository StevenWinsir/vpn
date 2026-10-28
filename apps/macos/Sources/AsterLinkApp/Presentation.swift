import AsterLinkShared
import Foundation

/// How a state reads, colours and acts: one place for the main window, the menu bar panel and the
/// diagnostics report. Display only: every verdict comes from the Core's snapshot, and the enable
/// conditions are the ones the single-column window used (connect needs a line, an authorized
/// account and no command in flight).
enum Tone { case idle, busy, good, bad }

enum ConnectionStatus: Equatable {
    case disconnected, preparing, connecting, connected, stopping, unavailable

    init(_ state: CoreState) {
        switch state {
        case .connected: self = .connected
        case .connecting: self = .connecting
        case .preparing: self = .preparing
        case .stopping: self = .stopping
        case .blocked, .error: self = .unavailable
        default: self = .disconnected
        }
    }

    var title: String {
        switch self {
        case .disconnected: "未连接"
        case .preparing: "正在准备…"
        case .connecting: "正在连接…"
        case .connected: "已连接"
        case .stopping: "正在断开…"
        case .unavailable: "无法连接"
        }
    }

    var detail: String {
        switch self {
        case .disconnected: "选择线路后，点击按钮即可连接"
        case .preparing: "正在准备连接，请稍候"
        case .connecting: "正在为你建立安全连接"
        case .connected: "已为你建立安全连接"
        case .stopping: "正在断开当前连接"
        case .unavailable: "请查看上方提示，或稍后重试"
        }
    }

    var tone: Tone {
        switch self {
        case .disconnected: .idle
        case .preparing, .connecting, .stopping: .busy
        case .connected: .good
        case .unavailable: .bad
        }
    }

    /// While a command runs the button shows progress instead of an action.
    var inProgress: Bool { self == .preparing || self == .connecting || self == .stopping }
}

/// What the big button (and the menu bar switch) does right now.
enum PrimaryAction: Equatable {
    case connect(enabled: Bool)
    case disconnect(enabled: Bool)
    case progress

    @MainActor
    init(_ session: SessionController) {
        let snap = session.snapshot
        switch snap.state {
        case .connected: self = .disconnect(enabled: !session.busy)
        case .connecting, .preparing, .stopping: self = .progress
        default:
            self = .connect(enabled: !snap.selectedNodeID.isEmpty && snap.entitlement?.accountAuthorized == true && !session.busy)
        }
    }

    var enabled: Bool {
        switch self {
        case let .connect(enabled), let .disconnect(enabled): enabled
        case .progress: false
        }
    }

    var title: String {
        switch self {
        case .connect: "连接"
        case .disconnect: "断开"
        case .progress: "请稍候"
        }
    }
}

/// The one thing standing between the user and a connection, with the one next step that fixes it.
struct Blocker: Equatable {
    enum Step: Equatable { case purchase, chooseLine, refresh }
    let text: String
    let step: Step?
    let severe: Bool

    /// Nil when Connect is available (or a connection is already up).
    @MainActor
    static func of(_ session: SessionController) -> Blocker? {
        let snap = session.snapshot
        if snap.state == .connected || ConnectionStatus(snap.state).inProgress { return nil }
        if let entitlement = snap.entitlement, entitlement.accountAuthorized {
            if snap.nodes.isEmpty {
                return Blocker(text: snap.catalogRevision.isEmpty ? "正在同步线路…" : "暂无可用线路，请稍后刷新", step: .refresh, severe: false)
            }
            if snap.selectedNodeID.isEmpty { return Blocker(text: "请先选择一条线路", step: .chooseLine, severe: false) }
            return nil
        }
        let reason = snap.entitlement?.reasonMessage ?? ""
        return Blocker(text: reason.isEmpty ? "暂无可用套餐，请前往官网购买或续费" : reason, step: .purchase, severe: true)
    }
}

enum Plan {
    /// Whole days left until the subscription ends (0 on the last day); nil without a date.
    static func daysLeft(_ expires: Date?, now: Date = Date()) -> Int? {
        guard let expires else { return nil }
        return max(0, Int(ceil(expires.timeIntervalSince(now) / 86_400)))
    }

    static func usedFraction(_ e: CoreEntitlement) -> Double {
        guard e.totalBytes > 0 else { return 0 }
        return min(1, max(0, 1 - Double(e.remainingBytes) / Double(e.totalBytes)))
    }
}

/// Lines of one region, in the order the server listed them.
struct RegionGroup: Identifiable {
    let region: String
    let nodes: [CoreNode]
    var id: String { region }
}

enum LineFilter: String, CaseIterable, Identifiable {
    case all, dedicated, direct
    var id: String { rawValue }

    var title: String {
        switch self {
        case .all: "全部"
        case .dedicated: "专线"
        case .direct: "普通线路"
        }
    }
}

enum NodeList {
    /// Search over what the user can already see (name, region, line label). The server's order is
    /// kept inside a region, and regions appear in the order of their first line.
    static func groups(_ nodes: [CoreNode], query: String, filter: LineFilter) -> [RegionGroup] {
        let needle = query.trimmingCharacters(in: .whitespaces).lowercased()
        var order: [String] = []
        var groups: [String: [CoreNode]] = [:]
        for node in nodes {
            switch filter {
            case .dedicated where !node.isDedicated, .direct where node.isDedicated: continue
            default: break
            }
            if !needle.isEmpty, !(node.name.lowercased().contains(needle) || node.region.lowercased().contains(needle)
                                  || node.lineLabel.contains(needle)) { continue }
            let key = node.region.trimmingCharacters(in: .whitespaces)
            if groups[key] == nil { order.append(key) }
            groups[key, default: []].append(node)
        }
        return order.map { RegionGroup(region: $0.isEmpty ? "其他" : $0, nodes: groups[$0] ?? []) }
    }
}
