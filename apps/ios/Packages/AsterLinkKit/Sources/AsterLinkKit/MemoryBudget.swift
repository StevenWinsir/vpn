import Darwin

/// The extension's memory guard (handoff_app_ios.md 8.7). iOS ends a packet tunnel extension
/// whose whole-process footprint passes its jetsam limit (about 50 MiB, not guaranteed), and
/// the Go heap limit only covers part of that. So the provider samples phys_footprint every
/// few seconds: above the soft threshold it asks the Go runtime to return memory, above the
/// hard one it also records pressure. The thresholds are initial values; rounds 2 and 5 set
/// them from device measurements. The peak is kept for diagnostics.
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

    public let soft: UInt64
    public let hard: UInt64
    public private(set) var peak: UInt64 = 0

    public init(soft: UInt64 = softLimit, hard: UInt64 = hardLimit) {
        precondition(soft < hard)
        self.soft = soft
        self.hard = hard
    }

    public mutating func observe(_ footprint: UInt64) -> Action {
        peak = max(peak, footprint)
        if footprint >= hard { return .pressure }
        if footprint >= soft { return .release }
        return .none
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
