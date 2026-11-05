import Darwin

/// Whether macOS is holding a process below the normal scheduling band (handoff_fix.md F1).
///
/// A launchd job without `ProcessType` gets "light resource limits": its task base priority is
/// clamped to 20 (utility), and every process it spawns inherits the clamp. `Adaptive` and
/// `Background` jobs sit at 4 while idle. An interactive job, and anything a user starts, runs
/// at the default 31. The Helper checks itself and the Core it spawned, so a registration that
/// still carries the old launchd type after an upgrade is visible instead of silently slow.
public enum SchedulingProbe {
    /// The default task base priority (BASEPRI_DEFAULT). Anything lower is a clamp.
    public static let normalBasePriority: Int32 = 31

    /// Pure classification, kept separate so it can be tested without a clamped process.
    public static func isLimited(basePriority: Int32) -> Bool { basePriority < normalBasePriority }

    /// The task base priority of `pid` (the caller's own process by default), or nil if it
    /// cannot be read (the process is gone, or belongs to another user).
    public static func basePriority(of pid: pid_t = getpid()) -> Int32? {
        var info = proc_taskinfo()
        let size = Int32(MemoryLayout<proc_taskinfo>.size)
        guard proc_pidinfo(pid, PROC_PIDTASKINFO, 0, &info, size) == size else { return nil }
        return info.pti_priority
    }

    /// True if `pid` is clamped; nil if it could not be read (never treated as limited).
    public static func isLimited(pid: pid_t = getpid()) -> Bool? {
        basePriority(of: pid).map(isLimited(basePriority:))
    }
}
