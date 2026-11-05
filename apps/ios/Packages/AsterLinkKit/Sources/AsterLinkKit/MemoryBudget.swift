import Darwin

/// The extension's memory guard (handoff_app_ios.md 8.7). iOS ends a packet tunnel extension
/// whose whole-process footprint passes its jetsam limit (about 50 MiB, not guaranteed), and
/// the Go heap limit only covers part of that. So the provider samples phys_footprint every
/// few seconds: above the soft threshold it asks the Go runtime to return memory, above the
/// hard one it also records pressure. The thresholds are initial values; rounds 2 and 5 set
/// them from device measurements. The peak is kept for diagnostics.
///
/// A release is a full GC plus returning every free page, and the pages come straight back
/// under sustained traffic. Repeating it every sample while the footprint sits above the
/// threshold only takes CPU from forwarding (handoff_fix.md F5, D9). So a release happens on
/// the first crossing after the footprint was below the soft threshold; while it stays above,
/// again only after `releaseInterval` and only if it grew by `releaseGrowth` since the last
/// release. Above the hard threshold growth alone is enough (no wait): that is the way to the
/// jetsam limit. Pressure is reported with the same rate limit.
public struct MemoryBudget: Sendable {
    public enum Action: Equatable, Sendable {
        case none
        /// Collect and return memory to the system (MobileFreeMemory).
        case release
        /// As release, and report pressure: the hard threshold was crossed.
        case pressure
    }

    public static let softLimit: UInt64 = 38 << 20
    public static let hardLimit: UInt64 = 44 << 20
    /// The Go heap's soft limit and GC target applied at start (MobileTuneMemory).
    public static let goHeapLimit: Int64 = 22 << 20
    public static let goGCPercent = 60
    /// Minimum seconds between releases while the footprint stays above the soft threshold.
    public static let releaseInterval: Double = 30
    /// Growth since the last release that justifies another one.
    public static let releaseGrowth: UInt64 = 2 << 20

    public let soft: UInt64
    public let hard: UInt64
    public private(set) var peak: UInt64 = 0
    /// Releases performed so far (logged in the resource sample).
    public private(set) var releases = 0
    private var lastRelease: (at: Double, footprint: UInt64)?
    /// True while the footprint has been below the soft threshold since the last release.
    private var armed = true

    public init(soft: UInt64 = softLimit, hard: UInt64 = hardLimit) {
        precondition(soft < hard)
        self.soft = soft
        self.hard = hard
    }

    /// `uptime` is a monotonic clock in seconds (the provider passes the system uptime).
    public mutating func observe(_ footprint: UInt64, at uptime: Double) -> Action {
        peak = max(peak, footprint)
        guard footprint >= soft else {
            armed = true
            return .none
        }
        let grew = lastRelease.map { footprint >= $0.footprint + Self.releaseGrowth } ?? true
        let waited = lastRelease.map { uptime - $0.at >= Self.releaseInterval } ?? true
        let due = footprint >= hard ? (armed || grew) : (armed || (grew && waited))
        guard due else { return .none }
        armed = false
        lastRelease = (uptime, footprint)
        releases += 1
        return footprint >= hard ? .pressure : .release
    }

    /// This process's phys_footprint (what jetsam compares), or nil if unavailable.
    public static func footprint() -> UInt64? {
        var info = task_vm_info_data_t()
        var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<natural_t>.size)
        let result = withUnsafeMutablePointer(to: &info) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
            }
        }
        return result == KERN_SUCCESS ? info.phys_footprint : nil
    }
}
