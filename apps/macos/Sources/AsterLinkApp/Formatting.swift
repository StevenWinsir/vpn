import Foundation

/// Display helpers. Traffic is shown in GB like the website (1 GB = 2^30 bytes); the counters
/// themselves are never rounded, only their display.
enum Formatting {
    private static let units = ["B", "KB", "MB", "GB", "TB"]

    static func bytes(_ value: Int64) -> String {
        var amount = Double(max(0, value))
        var index = 0
        while amount >= 1024, index < units.count - 1 { amount /= 1024; index += 1 }
        return index == 0 ? "\(Int(amount)) B" : String(format: "%.2f %@", amount, units[index])
    }

    static func speed(_ bytesPerSecond: Int64) -> String { bytes(bytesPerSecond) + "/s" }

    /// Physical bytes at a line's rate, rounded down like the server's balance.
    static func billedBytes(physical: Int64, ratePermille: Int64) -> Int64 {
        if physical <= 0 || ratePermille <= 0 { return 0 }
        return (physical / 1000) * ratePermille + (physical % 1000) * ratePermille / 1000
    }

    static func date(_ value: Date?) -> String { stamp(value, "yyyy-MM-dd") }
    static func dateTime(_ value: Date?) -> String { stamp(value, "yyyy-MM-dd HH:mm") }

    private static func stamp(_ value: Date?, _ pattern: String) -> String {
        guard let value else { return "—" }
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = pattern
        return formatter.string(from: value)
    }
}
