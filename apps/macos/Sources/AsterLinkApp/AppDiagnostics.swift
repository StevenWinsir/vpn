import AsterLinkShared
import Foundation

/// Build facts plus a redacted diagnostics report. The report is assembled from the
/// Core's already-redacted snapshot; it cannot contain a token, password or node detail.
struct AppDiagnostics {
    let appVersion: String
    let channel: String
    let mihomoRevision: String
    let apiHost: String

    init(environment: ClientEnvironment?) {
        let info = Bundle.main.infoDictionary ?? [:]
        appVersion = (info["CFBundleShortVersionString"] as? String) ?? "dev"
        channel = environment?.channel.rawValue ?? "unconfigured"
        mihomoRevision = String((info["AsterLinkMihomoRevision"] as? String ?? "70f0570405c3c2c47bb113b88db95006d239b346").prefix(10))
        apiHost = environment?.apiBase.host ?? "—"
    }

    func report(for snapshot: CoreSnapshot) -> String {
        var lines = [
            "AsterLink \(appVersion) (\(channel))",
            "core \(snapshot.coreVersion) mihomo \(mihomoRevision)",
            "api \(apiHost)",
            "state \(snapshot.state.rawValue) generation \(snapshot.generation)",
            "nodes \(snapshot.nodes.count) config_ready \(snapshot.configReady)",
        ]
        if let entitlement = snapshot.entitlement {
            lines.append("account_authorized \(entitlement.accountAuthorized) reason \(entitlement.reason.isEmpty ? "-" : entitlement.reason)")
        }
        if let error = snapshot.lastError { lines.append("last_error \(error.code)") }
        return lines.joined(separator: "\n")
    }
}
