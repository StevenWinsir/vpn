import Darwin
import Foundation

// Round 5 (handoff_app_ios.md 8.6, 8.7, 11): the pure rules of the mobile lifecycle, kept out of
// the extension and the app so they run in the Kit's tests. The system itself (sleep, wake, a
// competing VPN, Settings) is device-only; these types decide what the answer to each is.

/// The foreground hint as a lease. The app renews it while a screen is visible; the extension
/// keeps the catalog long-poll running only while the lease is valid. An app that is killed or
/// suspended cannot say "background", so the lease simply runs out and the extension falls back
/// to the background rhythm on its own (handoff_app_ios.md 8.6).
public struct ForegroundLease: Sendable, Equatable {
    /// How long one hint stays valid, and how often the app repeats it (a third of the lease).
    public static let ttl: TimeInterval = 30
    public static let heartbeat: Duration = .seconds(10)

    public private(set) var expiresAt: TimeInterval?
    /// What the Core was last told, so a transition is reported once.
    public private(set) var applied: Bool

    /// The Core starts in the foreground (the user just tapped Connect).
    public init(startedAt now: TimeInterval) {
        expiresAt = now + Self.ttl
        applied = true
    }

    /// The app says a screen is visible (or not). Returns the value to give the Core when it changed.
    public mutating func hint(visible: Bool, at now: TimeInterval) -> Bool? {
        expiresAt = visible ? now + Self.ttl : nil
        return transition(to: visible)
    }

    /// Called on the extension's timer: the lease ran out without a renewal.
    public mutating func tick(at now: TimeInterval) -> Bool? {
        guard let expiresAt, now >= expiresAt else { return nil }
        self.expiresAt = nil
        return transition(to: false)
    }

    private mutating func transition(to visible: Bool) -> Bool? {
        guard visible != applied else { return nil }
        applied = visible
        return visible
    }
}

/// Why the system stopped the tunnel (NEProviderStopReason raw values; the app and extension do
/// not both link the enum, so the numbers are mapped here and checked by a test).
public struct TunnelStopReason: Sendable, Equatable {
    public let raw: Int
    public init(raw: Int) { self.raw = raw }

    /// What to tell the user, or nil for a stop that needs no news: none, or the user's own
    /// toggle (the app learns of that by itself and says so once it is open).
    public var notice: Notice? {
        switch raw {
        case 0, 1: return nil
        case 11: return .local(LocalCodes.vpnSuperseded)
        case 5, 9, 10: return .local(LocalCodes.vpnRevoked)
        case 12, 13: return .local(LocalCodes.vpnUserSwitch)
        case 16: return .local(LocalCodes.vpnAppUpdate)
        case 3, 4: return .local(LocalCodes.networkUnreachable)
        case 15, 8: return .local(LocalCodes.tunnelEnded)
        default: return .local(LocalCodes.vpnSystemStopped)
        }
    }

    /// A stable name for the log (a number alone says little when reading a device log).
    public var name: String {
        let names = ["none", "userInitiated", "providerFailed", "noNetworkAvailable", "unrecoverableNetworkChange", "providerDisabled",
                     "authenticationCanceled", "configurationFailed", "idleTimeout", "configurationDisabled", "configurationRemoved",
                     "superceded", "userLogout", "userSwitch", "connectionFailed", "sleep", "appUpdate"]
        return raw >= 0 && raw < names.count ? names[raw] : "unknown\(raw)"
    }
}

/// Turns the Core's own gate history into log lines (D-L01/D-L02: the gate closure's length and
/// reason are recorded). The Core keeps `last_gate` and a closure counter; a new count is a new
/// closure that has been reopened.
public struct GateLog: Sendable {
    private var seenClosures: Int64
    private var wasClosed = false

    public init(seenClosures: Int64 = 0) { self.seenClosures = seenClosures }

    /// One log line per event, in order; empty when nothing changed. Carries reason codes and
    /// durations only.
    public mutating func observe(_ snapshot: CoreSnapshot) -> [String] {
        var lines: [String] = []
        if let gate = snapshot.gate, !wasClosed {
            wasClosed = true
            lines.append("gate closed reason=\(gate.reason)")
        } else if snapshot.gate == nil, wasClosed {
            wasClosed = false
        }
        if let record = snapshot.lastGate, record.totalClosures > seenClosures {
            seenClosures = record.totalClosures
            lines.append("gate reopened reason=\(record.reason) outcome=\(record.outcome) closed_ms=\(record.closedMillis) attempts=\(record.attempts) total=\(record.totalClosures)")
        }
        return lines
    }
}

/// One resource reading of the extension, written to the log every minute so a device run leaves
/// a trace (D-MEM, D-L07; `scripts/ios/summarize-samples.py` reads it back). Numbers only.
public struct ResourceSample: Sendable, Equatable {
    public let uptimeSeconds: Int
    public let footprint: UInt64
    public let peak: UInt64
    public let openFiles: Int
    public let threads: Int
    public let goroutines: Int
    public let goHeap: UInt64
    public let goSys: UInt64
    public let gcCount: Int

    public static let prefix = "sample "

    /// `stats` is the Core's RuntimeStats document; an unreadable one reads as zeros.
    public static func capture(uptime: Int, footprint: UInt64, peak: UInt64, stats: String) -> ResourceSample {
        let object = (try? JSONSerialization.jsonObject(with: Data(stats.utf8))) as? [String: Any] ?? [:]
        func number(_ key: String) -> UInt64 { (object[key] as? NSNumber)?.uint64Value ?? 0 }
        return ResourceSample(uptimeSeconds: uptime, footprint: footprint, peak: peak, openFiles: openFileCount(), threads: threadCount(),
                              goroutines: Int(number("goroutines")), goHeap: number("heap_in_use"), goSys: number("sys"),
                              gcCount: Int(number("num_gc")))
    }

    public var logLine: String {
        Self.prefix + "t=\(uptimeSeconds) footprint=\(footprint) peak=\(peak) fds=\(openFiles) threads=\(threads) goroutines=\(goroutines) heap=\(goHeap) sys=\(goSys) gc=\(gcCount)"
    }

    /// The reading back of `logLine` (the tests prove the two agree with the script's format).
    public static func parse(_ line: String) -> ResourceSample? {
        guard let start = line.range(of: prefix) else { return nil }
        var fields: [String: UInt64] = [:]
        for part in line[start.upperBound...].split(separator: " ") {
            let pair = part.split(separator: "=", maxSplits: 1)
            if pair.count == 2, let value = UInt64(pair[1]) { fields[String(pair[0])] = value }
        }
        guard let t = fields["t"], let footprint = fields["footprint"], let peak = fields["peak"], let fds = fields["fds"],
              let threads = fields["threads"], let goroutines = fields["goroutines"], let heap = fields["heap"],
              let sys = fields["sys"], let gc = fields["gc"] else { return nil }
        return ResourceSample(uptimeSeconds: Int(t), footprint: footprint, peak: peak, openFiles: Int(fds), threads: Int(threads),
                              goroutines: Int(goroutines), goHeap: heap, goSys: sys, gcCount: Int(gc))
    }

    /// Descriptors open in this process (the directory listing's own descriptor is not counted).
    static func openFileCount() -> Int {
        let count = (try? FileManager.default.contentsOfDirectory(atPath: "/dev/fd").count) ?? 1
        return max(0, count - 1)
    }

    static func threadCount() -> Int {
        var list: thread_act_array_t?
        var count = mach_msg_type_number_t(0)
        guard task_threads(mach_task_self_, &list, &count) == KERN_SUCCESS, let list else { return 0 }
        vm_deallocate(mach_task_self_, vm_address_t(UInt(bitPattern: list)), vm_size_t(Int(count) * MemoryLayout<thread_t>.size))
        return Int(count)
    }
}
