import Foundation

/// Display helpers. Traffic is shown in GB (1 GB = 2^30 bytes, like the website); accounting
/// integers never go through a Double (only the final display rounding does).
public enum Formatting {
    private static let units = ["B", "KB", "MB", "GB", "TB", "PB"]

    public static func bytes(_ value: Int64) -> String {
        let clamped = max(0, value)
        var amount = Double(clamped)
        var index = 0
        while amount >= 1024 && index < units.count - 1 {
            amount /= 1024
            index += 1
        }
        return index == 0 ? "\(clamped) B" : String(format: "%.2f %@", amount, units[index])
    }

    public static func speed(_ bytesPerSecond: Int64) -> String { bytes(bytesPerSecond) + "/s" }

    /// 500‰ → "0.5×", 1000‰ → "1×", 1250‰ → "1.25×". Pure integer arithmetic.
    public static func rate(_ permille: Int64) -> String {
        let whole = permille / 1000
        let fraction = permille % 1000
        if fraction == 0 { return "\(whole)×" }
        var digits = String(format: "%03d", Int(fraction))
        while digits.hasSuffix("0") { digits.removeLast() }
        return "\(whole).\(digits)×"
    }

    /// Physical bytes at a line's rate, in bytes (units / 1000, rounded down like the server's
    /// remaining_bytes). Split so the product cannot overflow for any realistic counter.
    public static func billedBytes(physical: Int64, ratePermille: Int64) -> Int64 {
        if physical <= 0 || ratePermille <= 0 { return 0 }
        return (physical / 1000) * ratePermille + (physical % 1000) * ratePermille / 1000
    }

    /// The server's deduction in units (1 unit = 1/1000 byte): physical bytes x permille, exact.
    /// Saturates instead of overflowing, so a corrupt counter can never wrap into a refund.
    public static func chargedUnits(physical: Int64, ratePermille: Int64) -> Int64 {
        if physical <= 0 || ratePermille <= 0 { return 0 }
        let (units, overflow) = physical.multipliedReportingOverflow(by: ratePermille)
        return overflow ? Int64.max : units
    }

    /// RFC 3339 as Go writes it (with or without fractional seconds); nil when absent or unreadable.
    public static func parseInstant(_ value: String?) -> Date? {
        guard let value, !value.isEmpty else { return nil }
        if let date = try? Date(value, strategy: .iso8601) { return date }
        let withFraction = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
        return try? Date(value, strategy: withFraction)
    }

    public static func date(_ value: Date?, timeZone: TimeZone = .current) -> String {
        guard let value else { return "—" }
        return stamp(value, "yyyy-MM-dd", timeZone)
    }

    public static func dateTime(_ value: Date?, timeZone: TimeZone = .current) -> String {
        guard let value else { return "—" }
        return stamp(value, "yyyy-MM-dd HH:mm", timeZone)
    }

    private static func stamp(_ value: Date, _ pattern: String, _ timeZone: TimeZone) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = timeZone
        f.dateFormat = pattern
        return f.string(from: value)
    }

    /// Whole days (rounded up) until `expires`; nil without a date; 0 when already past.
    public static func daysLeft(_ expires: Date?, now: Date) -> Int64? {
        guard let expires else { return nil }
        let seconds = Int64(expires.timeIntervalSince1970) - Int64(now.timeIntervalSince1970)
        return seconds <= 0 ? 0 : (seconds + 86_399) / 86_400
    }

    /// Fraction of the plan already used, 0...1.
    public static func usedFraction(totalBytes: Int64, remainingBytes: Int64) -> Double {
        if totalBytes <= 0 { return 0 }
        return min(1, max(0, 1 - Double(remainingBytes) / Double(totalBytes)))
    }

    /// 00:12:31, or 1:02:03:04 past a day.
    public static func elapsed(milliseconds: Int64) -> String {
        let total = max(0, milliseconds / 1000)
        let days = total / 86_400
        let clock = String(format: "%02d:%02d:%02d", Int(total % 86_400 / 3600), Int(total % 3600 / 60), Int(total % 60))
        return days > 0 ? "\(days):\(clock)" : clock
    }

    /// The avatar letter: the first letter or digit of the name or email.
    public static func initial(name: String, email: String) -> String {
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        let source = trimmed.isEmpty ? email.trimmingCharacters(in: .whitespaces) : trimmed
        guard let first = source.first(where: { $0.isLetter || $0.isNumber }) else { return "?" }
        return String(first).uppercased()
    }
}
