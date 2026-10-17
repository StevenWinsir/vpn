import CAsterResolv
import Darwin
import Foundation
import Network

/// The physical network as the Core needs it (handoff_app_ios.md 7; Go
/// native/core/internal/network/host.go HostSnapshot, decoded strictly there): the default
/// network's interface name and index, its own addresses, its resolvers and whether it is
/// metered. Never anything about a node or the account.
public struct HostNetworkReport: Equatable, Sendable {
    public let available: Bool
    public let interface: String
    public let index: Int
    public let addresses: [String]
    public let dns: [String]
    public let metered: Bool

    public static let unavailable = HostNetworkReport(available: false, interface: "", index: 0, addresses: [], dns: [], metered: false)

    public init(available: Bool, interface: String, index: Int, addresses: [String], dns: [String], metered: Bool) {
        self.available = available
        self.interface = interface
        self.index = index
        self.addresses = addresses
        self.dns = dns
        self.metered = metered
    }

    /// The document Platform.networkSnapshot returns. The Go decoder bounds the lists
    /// (32 addresses, 16 resolvers); they are cut here so a busy interface is not refused.
    public func json() -> String {
        guard available else { return #"{"available":false}"# }
        let object: [String: Any] = [
            "available": true, "interface": interface, "index": index,
            "addresses": Array(addresses.prefix(32)), "dns": Array(dns.prefix(16)), "metered": metered,
        ]
        let data = (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data()
        return String(decoding: data, as: UTF8.self)
    }

    /// Builds the report for a path. Tunnel interfaces (utun, ipsec) are never the physical
    /// network; the first other interface the path uses is.
    public static func from(path: NWPath, resolvers: [String] = systemResolvers()) -> HostNetworkReport {
        guard path.status == .satisfied,
              let link = path.availableInterfaces.first(where: { $0.type != .other && !isTunnelName($0.name) })
        else { return .unavailable }
        let index = Int(if_nametoindex(link.name))
        guard index > 0 else { return .unavailable }
        return HostNetworkReport(available: true, interface: link.name, index: index, addresses: addresses(of: link.name),
                                 dns: resolvers, metered: path.isExpensive || path.isConstrained)
    }

    static func isTunnelName(_ name: String) -> Bool {
        ["utun", "ipsec", "ppp", "tun", "tap"].contains { name.hasPrefix($0) }
    }

    /// The interface's numeric addresses (IPv4 and IPv6, scope ids dropped).
    public static func addresses(of name: String) -> [String] {
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0, let first = head else { return [] }
        defer { freeifaddrs(head) }
        var out: [String] = []
        for entry in sequence(first: first, next: { $0.pointee.ifa_next }) {
            guard String(cString: entry.pointee.ifa_name) == name, let address = entry.pointee.ifa_addr else { continue }
            let family = Int32(address.pointee.sa_family)
            guard family == AF_INET || family == AF_INET6 else { continue }
            var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            let length = socklen_t(family == AF_INET ? MemoryLayout<sockaddr_in>.size : MemoryLayout<sockaddr_in6>.size)
            guard getnameinfo(address, length, &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 else { continue }
            let text = String(cString: host)
            out.append(String(text.split(separator: "%", maxSplits: 1).first ?? Substring(text)))
        }
        return out
    }

    /// The system resolver's servers. Inside the running tunnel this names the tunnel's own
    /// resolver, which the Core filters out (it then falls back to its bootstrap list); read
    /// before the tunnel exists it is the physical network's.
    public static func systemResolvers() -> [String] {
        var buffer = [CChar](repeating: 0, count: 1024)
        guard aster_dns_servers(&buffer, buffer.count) > 0 else { return [] }
        return String(cString: buffer).split(separator: "\n").map(String.init)
    }
}

/// Watches the default path and keeps the latest report. The provider hands it to the Core
/// (Platform.networkSnapshot) and calls `onChange` so the Core re-checks its network signature
/// (MobileNetworkChanged). Resolvers are taken from the last report made while no tunnel of
/// ours was up, because inside the tunnel the system resolver is the tunnel's.
public final class PathMonitor: @unchecked Sendable {
    private let monitor = NWPathMonitor()
    private let queue = DispatchQueue(label: "asterlink.path-monitor")
    private let lock = NSLock()
    private var report = HostNetworkReport.unavailable
    private var physicalResolvers: [String] = []
    private var tunnelUp = false
    public var onChange: (@Sendable () -> Void)?

    public init() {}

    public func start() {
        monitor.pathUpdateHandler = { [weak self] path in self?.update(path) }
        monitor.start(queue: queue)
    }

    public func cancel() { monitor.cancel() }

    /// The provider calls this right before applying its own tunnel settings.
    public func freezeResolvers() {
        lock.lock()
        defer { lock.unlock() }
        if physicalResolvers.isEmpty { physicalResolvers = HostNetworkReport.systemResolvers() }
        tunnelUp = true
    }

    public var latestJSON: String {
        lock.lock()
        defer { lock.unlock() }
        return report.json()
    }

    private func update(_ path: NWPath) {
        lock.lock()
        if !tunnelUp { physicalResolvers = HostNetworkReport.systemResolvers() }
        let next = HostNetworkReport.from(path: path, resolvers: physicalResolvers)
        let changed = next != report
        report = next
        lock.unlock()
        if changed { onChange?() }
    }
}
