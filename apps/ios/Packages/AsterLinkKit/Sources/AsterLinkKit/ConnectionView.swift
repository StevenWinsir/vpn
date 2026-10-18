import Foundation

/// The fixed status words of handoff_app_ios.md 10.3 (the UI owns the text).
public enum ConnectionStatus: Sendable, Equatable {
    case disconnected, preparing, connecting, connected, stopping, unavailable
    /// Connected, gate closed: re-confirming the authorization online (正在重新验证).
    case reconfirming
    /// Connected, gate closed, and the server cannot be reached yet (等待网络).
    case waitingNetwork
}

public enum Tone: Sendable, Equatable { case idle, busy, good, bad }

/// What the connect page shows for one snapshot. Derived only from the Core's verdict plus the
/// command in flight; nothing here decides authorization.
public struct ConnectionView: Sendable, Equatable {
    public let status: ConnectionStatus
    public let tone: Tone
    public let showConnect: Bool
    public let showDisconnect: Bool
    public let showProgress: Bool
    public let canConnect: Bool
    public let cancellable: Bool
    /// Why Connect is unavailable (nil when it is available).
    public let blocker: Notice?
    /// The last failure the Core recorded, shown under the status.
    public let failure: Notice?
    /// The account has no usable plan.
    public let needsPurchase: Bool
    /// The server refuses this app version (client_upgrade_required).
    public let needsUpdate: Bool

    /// Entitlement reasons the website fixes (buy, renew, top up).
    static let purchaseReasons: Set<String> = ["upgrade_required", "paid_vip_required", "subscription_expired", "quota_exhausted"]
    /// The server's answer when this app version is below its CLIENT_MIN_APP_VERSION.
    public static let updateRequired = "client_upgrade_required"

    public static func from(_ snapshot: CoreSnapshot, busy: Bool, activity: SessionActivity) -> ConnectionView {
        let entitlement = snapshot.entitlement
        let authorized = entitlement?.accountAuthorized == true
        let needsPurchase = !authorized && (entitlement == nil || entitlement!.reason.isEmpty || purchaseReasons.contains(entitlement!.reason))
        let blocker: Notice?
        if !authorized {
            if let entitlement, !entitlement.reasonMessage.isEmpty { blocker = .core(entitlement.reason, entitlement.reasonMessage) } else { blocker = .local(LocalCodes.noPlan) }
        } else if snapshot.nodes.isEmpty {
            blocker = .local(snapshot.catalogRevision.isEmpty ? LocalCodes.syncing : LocalCodes.noLines)
        } else if snapshot.selectedNode == nil {
            blocker = .local(LocalCodes.selectLine)
        } else {
            blocker = nil
        }

        // While the connect command runs the Core answers nothing else (commands are serial), so
        // the snapshot still says "ready": the activity is the truth until the reply.
        if activity == .connecting && snapshot.state != CoreSnapshot.State.connected {
            return busyView(snapshot.state == CoreSnapshot.State.connecting ? .connecting : .preparing, cancellable: true)
        }
        if activity == .disconnecting { return busyView(.stopping, cancellable: false) }

        switch snapshot.state {
        case CoreSnapshot.State.connected:
            // The tunnel holds traffic but forwards nothing until the Core re-confirms; it can
            // still be disconnected (which settles like any other disconnect).
            guard let gate = snapshot.gate else {
                return ConnectionView(status: .connected, tone: .good, showConnect: false, showDisconnect: true, showProgress: false,
                                      canConnect: false, cancellable: false, blocker: nil, failure: nil, needsPurchase: false, needsUpdate: false)
            }
            return ConnectionView(status: gate.waitingNetwork ? .waitingNetwork : .reconfirming, tone: .busy, showConnect: false,
                                  showDisconnect: true, showProgress: true, canConnect: false, cancellable: false, blocker: nil,
                                  failure: nil, needsPurchase: false, needsUpdate: false)
        case CoreSnapshot.State.connecting: return busyView(.connecting, cancellable: false)
        case CoreSnapshot.State.preparing: return busyView(.preparing, cancellable: false)
        case CoreSnapshot.State.stopping: return busyView(.stopping, cancellable: false)
        default: break
        }
        let bad = snapshot.state == CoreSnapshot.State.blocked || snapshot.state == CoreSnapshot.State.error
        let failure = snapshot.lastError.flatMap { $0.message.isEmpty ? nil : Notice.core($0.code, $0.message) }
        return ConnectionView(
            status: bad ? .unavailable : .disconnected,
            tone: bad || (blocker != nil && !authorized) ? .bad : .idle,
            showConnect: true, showDisconnect: false, showProgress: false,
            canConnect: blocker == nil && !busy, cancellable: false,
            blocker: blocker, failure: failure, needsPurchase: needsPurchase,
            needsUpdate: failure?.code == updateRequired || blocker?.code == updateRequired)
    }

    private static func busyView(_ status: ConnectionStatus, cancellable: Bool) -> ConnectionView {
        ConnectionView(status: status, tone: .busy, showConnect: false, showDisconnect: false, showProgress: true, canConnect: false,
                       cancellable: cancellable, blocker: nil, failure: nil, needsPurchase: false, needsUpdate: false)
    }
}

public enum LineFilter: Sendable, Equatable, CaseIterable { case all, dedicated, direct }

public struct NodeRow: Sendable, Equatable, Identifiable {
    public let id: String
    public let name: String
    public let region: String
    public let dedicated: Bool
    public let ratePermille: Int64
    public let nodeMetered: Bool
    public let selected: Bool
    public let configReady: Bool

    public init(_ node: NodeView, snapshot: CoreSnapshot) {
        id = node.id; name = node.name; region = node.region; dedicated = node.isDedicated
        ratePermille = node.ratePermille; nodeMetered = node.meteringSource == "node_reported"
        selected = node.id == snapshot.selectedNodeID
        configReady = node.id == snapshot.selectedNodeID && snapshot.configReady
    }
}

/// Lines of one region, in the order the server listed them.
public struct RegionGroup: Sendable, Equatable, Identifiable {
    public let region: String
    public let rows: [NodeRow]
    public var id: String { region }
}

public enum NodeList {
    /// Search over the metadata the user can already see (name, region, line label); the
    /// server's order is kept and regions appear in the order of their first line.
    public static func build(_ snapshot: CoreSnapshot, query: String, filter: LineFilter, lineLabel: (Bool) -> String) -> [RegionGroup] {
        let needle = query.trimmingCharacters(in: .whitespaces).lowercased()
        let rows = snapshot.nodes
            .filter {
                switch filter {
                case .dedicated: return $0.isDedicated
                case .direct: return !$0.isDedicated
                case .all: return true
                }
            }
            .filter {
                needle.isEmpty || $0.name.lowercased().contains(needle) || $0.region.lowercased().contains(needle)
                    || lineLabel($0.isDedicated).lowercased().contains(needle)
            }
            .map { NodeRow($0, snapshot: snapshot) }
        var order: [String] = []
        var groups: [String: [NodeRow]] = [:]
        for row in rows {
            let key = row.region.trimmingCharacters(in: .whitespaces)
            if groups[key] == nil { order.append(key) }
            groups[key, default: []].append(row)
        }
        return order.map { RegionGroup(region: $0, rows: groups[$0] ?? []) }
    }

    /// So "no plan", "no lines" and "nothing matches" are never confused. Nil when rows exist.
    public static func emptyReason(_ snapshot: CoreSnapshot, filtered: Bool) -> String? {
        if snapshot.entitlement?.accountAuthorized != true { return LocalCodes.noPlan }
        // No catalog yet (just signed in): not the same as an administrator offering none.
        if snapshot.nodes.isEmpty && snapshot.catalogRevision.isEmpty { return LocalCodes.syncing }
        if snapshot.nodes.isEmpty { return LocalCodes.noLines }
        if filtered { return LocalCodes.noMatch }
        return nil
    }
}
