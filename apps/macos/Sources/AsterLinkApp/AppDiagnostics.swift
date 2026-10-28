import AsterLinkShared
import Foundation

/// The app version and the text behind "复制诊断信息" (handoff_app_standardization.md D9). The report
/// is pasted to customer support, so it is user-visible copy: the app version, the system version,
/// the connection state and the last error code, with the time of the report. The same six lines
/// as iOS and Android. No token, password, line or server detail can reach it.
struct AppDiagnostics {
    let appVersion: String
    /// Development builds carry a neutral marker next to the version (D8); release builds never do.
    let isDevelopment: Bool

    init(environment: ClientEnvironment?) {
        let info = Bundle.main.infoDictionary ?? [:]
        // Also the version the Core and the Helper are told; unchanged.
        appVersion = (info["CFBundleShortVersionString"] as? String) ?? "dev"
        isDevelopment = environment.map { $0.channel == .development } ?? true
    }

    init(appVersion: String, isDevelopment: Bool) {
        self.appVersion = appVersion
        self.isDevelopment = isDevelopment
    }

    static var osVersion: String {
        let v = ProcessInfo.processInfo.operatingSystemVersion
        return "macOS \(v.majorVersion).\(v.minorVersion).\(v.patchVersion)"
    }

    func report(for snapshot: CoreSnapshot, now: Date = Date()) -> String {
        let code = snapshot.lastError?.code ?? ""
        return [
            "AsterLink 诊断信息",
            "时间: \(Formatting.dateTime(now))",
            "应用版本: \(appVersion)",
            "系统版本: \(Self.osVersion)",
            "连接状态: \(ConnectionStatus(snapshot.state).title.replacingOccurrences(of: "…", with: ""))",
            "错误码: \(code.isEmpty ? "无" : code)",
        ].joined(separator: "\n")
    }
}
