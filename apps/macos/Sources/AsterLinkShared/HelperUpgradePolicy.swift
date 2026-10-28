import Foundation

/// A privileged Helper that does not match the app that is talking to it must never be
/// used to start a Core: an upgrade replaces the app bundle, but launchd can keep running
/// the previous Helper until it is re-registered (handoff_app.md stage 5).
public enum HelperUpgradePolicy {
    public enum Verdict: Equatable, Sendable {
        case compatible
        case mismatch(helper: String, app: String)
    }

    /// Exact string equality: Helper and app are built, signed and shipped from one version.
    public static func verdict(helperVersion: String, appVersion: String) -> Verdict {
        helperVersion == appVersion ? .compatible : .mismatch(helper: helperVersion, app: appVersion)
    }

    public static func userMessage(for verdict: Verdict) -> String? {
        guard case .mismatch = verdict else { return nil }
        return "后台服务需要更新，请在“账户”页点击“重新安装”后再连接"
    }

    /// Removal and reinstall are refused while traffic may be flowing: the user disconnects first.
    public static func mayChangeService(whileState state: CoreState) -> Bool {
        switch state {
        case .signedOut, .ready, .blocked, .error, .authenticating: true
        case .preparing, .connecting, .connected, .stopping: false
        }
    }
}
