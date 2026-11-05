import Foundation

/// A privileged Helper that does not match the app that is talking to it must never be
/// used to start a Core: an upgrade replaces the app bundle, but launchd can keep running
/// the previous Helper until it is re-registered (handoff_app.md stage 5).
public enum HelperUpgradePolicy {
    public enum Verdict: Equatable, Sendable {
        case compatible
        case mismatch(helper: String, app: String)
        /// Same version, but macOS still runs the Helper (or its Core) under the old launchd
        /// registration's resource limits (handoff_fix.md F1): reinstalling applies the new one.
        case schedulingLimited
    }

    /// Exact string equality: Helper and app are built, signed and shipped from one version.
    /// A Helper that reports a scheduling clamp needs the same reinstall as a stale version.
    public static func verdict(helperVersion: String, appVersion: String, schedulingLimited: Bool? = nil) -> Verdict {
        guard helperVersion == appVersion else { return .mismatch(helper: helperVersion, app: appVersion) }
        return schedulingLimited == true ? .schedulingLimited : .compatible
    }

    public static func userMessage(for verdict: Verdict) -> String? {
        switch verdict {
        case .compatible: nil
        case .mismatch, .schedulingLimited: "后台服务需要更新，请在“账户”页点击“重新安装”后再连接"
        }
    }

    /// Whether a connect must stop at `verdict`. A stale version always stops. A scheduling clamp
    /// stops until the user has reinstalled once for this app version: if macOS still clamps the
    /// Helper after that, a refusal would only lock the user out, so the slow-but-working path
    /// is allowed (and the clamp shows up in the Helper's replies for diagnosis).
    public static func refusesConnect(_ verdict: Verdict, reinstalledForVersion: String?, appVersion: String) -> Bool {
        switch verdict {
        case .compatible: false
        case .mismatch: true
        case .schedulingLimited: reinstalledForVersion != appVersion
        }
    }

    /// UserDefaults key under which the app records the version it last reinstalled the Helper for.
    public static let reinstalledVersionKey = "AsterLinkHelperReinstalledForVersion"

    /// Removal and reinstall are refused while traffic may be flowing: the user disconnects first.
    public static func mayChangeService(whileState state: CoreState) -> Bool {
        switch state {
        case .signedOut, .ready, .blocked, .error, .authenticating: true
        case .preparing, .connecting, .connected, .stopping: false
        }
    }
}
