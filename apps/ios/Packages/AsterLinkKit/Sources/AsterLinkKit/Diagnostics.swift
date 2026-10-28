import Foundation

/// The text behind "复制诊断信息" (handoff_app_standardization.md D9). It is pasted to customer
/// support, so it is user-visible copy: only the app version, the system version, the connection
/// state and the last error code, with the time of the report. The same five lines on every
/// platform. No token, password, line address or credential can reach it: none of them exist in
/// the types it reads.
public enum Diagnostics {
    public static func report(appVersion: String, osVersion: String, state: SessionState, now: Date = Date()) -> String {
        let snapshot = state.snapshot
        let view = ConnectionView.from(snapshot, busy: state.busy, activity: state.activity)
        let code = snapshot.lastError?.code ?? ""
        return [
            "AsterLink 诊断信息",
            "时间: \(Formatting.dateTime(now))",
            "应用版本: \(appVersion)",
            "系统版本: \(osVersion)",
            "连接状态: \(ErrorText.status(view.status))",
            "错误码: \(code.isEmpty ? "无" : code)",
        ].joined(separator: "\n")
    }
}
