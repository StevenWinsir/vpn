import Foundation

/// Display helpers. Traffic uses binary units (1 GiB = 2^30 bytes) and says so.
enum Formatting {
    static func bytes(_ value: Int64) -> String {
        let units = ["B", "KiB", "MiB", "GiB", "TiB"]
        var amount = Double(max(0, value))
        var index = 0
        while amount >= 1024, index < units.count - 1 { amount /= 1024; index += 1 }
        return index == 0 ? "\(Int(amount)) B" : String(format: "%.2f %@", amount, units[index])
    }

    static func date(_ value: Date?) -> String {
        guard let value else { return "—" }
        return value.formatted(date: .abbreviated, time: .shortened)
    }
}
