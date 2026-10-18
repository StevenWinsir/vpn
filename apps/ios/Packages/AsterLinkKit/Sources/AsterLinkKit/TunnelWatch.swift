import Foundation
import UserNotifications

/// The error domain the tunnel extension fails with; the app reads the code back from it
/// (NEVPNConnection.fetchLastDisconnectError).
public enum PacketTunnelErrors {
    public static let domain = "com.asterlink.tunnel"
}

/// A tunnel that ended without the user asking (quota used up, plan ended, line disabled or
/// changed, lease lost): the reason to show, in the Core's own words when it gave any.
public struct TunnelEnd: Sendable, Equatable {
    public let notice: Notice

    public var text: String { ErrorText.text(for: notice) }
}

/// Decides, from a Core snapshot, whether the data path ended by itself (handoff_app_ios.md 8.6:
/// "扩展在后台停连时发本地通知"). The extension runs this on a timer while the app may be suspended;
/// the app runs the same rules through SessionController, so both say the same thing.
public enum TunnelWatch {
    public enum Verdict: Sendable, Equatable {
        /// Connected, connecting, settling, or handed back: nothing to report.
        case running
        case ended(TunnelEnd)
        /// The response could not be read; keep watching.
        case unreadable
    }

    public static func evaluate(response: String) -> Verdict {
        guard let document = try? JSONDecoder().decode(CoreResponse.self, from: Data(response.utf8)),
              let snapshot = try? document.decodeResult(CoreSnapshot.self) else { return .unreadable }
        return evaluate(snapshot)
    }

    public static func evaluate(_ snapshot: CoreSnapshot) -> Verdict {
        if snapshot.tunnelBusy || snapshot.state == CoreSnapshot.State.released || snapshot.state == CoreSnapshot.State.authenticating {
            return .running
        }
        return .ended(TunnelEnd(notice: reason(snapshot)))
    }

    /// The Core's recorded error first, then the entitlement verdict, then a generic line.
    public static func reason(_ snapshot: CoreSnapshot) -> Notice {
        if let error = snapshot.lastError, !error.message.isEmpty { return .core(error.code, error.message) }
        if let verdict = snapshot.entitlement, !verdict.accountAuthorized, !verdict.reasonMessage.isEmpty {
            return .core(verdict.reason, verdict.reasonMessage)
        }
        if snapshot.state == CoreSnapshot.State.signedOut { return .local(LocalCodes.sessionEnded) }
        return .local(LocalCodes.tunnelEnded)
    }
}

/// Local notifications for a tunnel that stopped by itself. They carry only the reason text (a
/// Core message or a fixed sentence): no account, line or traffic figures. One fixed identifier,
/// so the extension's notification and the app's later one for the same stop are a single entry.
public enum TunnelNotifier {
    public static let identifier = "asterlink.tunnel.ended"

    public static func post(_ end: TunnelEnd, center: UNUserNotificationCenter = .current()) {
        let content = UNMutableNotificationContent()
        content.title = "VPN 已断开"
        content.body = end.text
        content.sound = .default
        content.interruptionLevel = .timeSensitive
        center.add(UNNotificationRequest(identifier: identifier, content: content, trigger: nil))
    }

    /// Withdraws a stale alert (a new connection, an explicit sign-out).
    public static func clear(center: UNUserNotificationCenter = .current()) {
        center.removeDeliveredNotifications(withIdentifiers: [identifier])
        center.removePendingNotificationRequests(withIdentifiers: [identifier])
    }

    /// Asks once. A refusal is fine: the VPN works without notifications and the reason is still
    /// shown on the next launch.
    @discardableResult
    public static func requestAuthorizationIfNeeded(center: UNUserNotificationCenter = .current()) async -> Bool {
        let settings = await center.notificationSettings()
        switch settings.authorizationStatus {
        case .authorized, .provisional, .ephemeral: return true
        case .denied: return false
        default: return (try? await center.requestAuthorization(options: [.alert, .sound])) ?? false
        }
    }
}

/// The SessionController's alert port, backed by local notifications.
public struct NotificationAlerts: SessionAlerts {
    public init() {}

    public func tunnelStopped(_ reason: Notice) { TunnelNotifier.post(TunnelEnd(notice: reason)) }

    public func clear() { TunnelNotifier.clear() }
}
