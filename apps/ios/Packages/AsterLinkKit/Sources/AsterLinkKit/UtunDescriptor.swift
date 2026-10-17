import Darwin

/// Finds the packet tunnel's utun descriptor in this process (handoff_app_ios.md 8.2).
///
/// NEPacketTunnelProvider does not expose the descriptor. After setTunnelNetworkSettings the
/// extension's process holds exactly one kernel-control socket connected to the utun control,
/// so every open descriptor is examined: getpeername must report an AF_SYSTEM address whose
/// control id is com.apple.net.utun_control's, and UTUN_OPT_IFNAME must name a utunN. This is
/// the approach WireGuard for iOS takes; no private API or KVC is involved. The Core receives
/// a dup() so the provider keeps the original and the interface outlives the stack's copy.
public enum UtunDescriptor {
    public enum Failure: Error, Equatable {
        case notFound
        /// More than one utun control socket: refuse rather than guess.
        case ambiguous([Int32])
        case controlUnavailable
        case dupFailed(Int32)
    }

    /// One open descriptor as seen by the scan.
    public struct Candidate: Equatable, Sendable {
        public let fd: Int32
        public let controlID: UInt32?
        public let interfaceName: String?

        public init(fd: Int32, controlID: UInt32?, interfaceName: String?) {
            self.fd = fd
            self.controlID = controlID
            self.interfaceName = interfaceName
        }
    }

    /// The selection rule, separated from the system calls so it is tested on any host.
    public static func select(_ candidates: [Candidate], utunControlID: UInt32) throws -> Int32 {
        let matches = candidates.filter { candidate in
            guard candidate.controlID == utunControlID, let name = candidate.interfaceName,
                  name.hasPrefix("utun"), let unit = Int(name.dropFirst(4)), unit >= 0 else { return false }
            return true
        }
        switch matches.count {
        case 0: throw Failure.notFound
        case 1: return matches[0].fd
        default: throw Failure.ambiguous(matches.map(\.fd))
        }
    }

    /// Scans this process and returns a dup() of the utun descriptor (close-on-exec). The caller
    /// owns the copy; the Go Core closes it with its listener.
    public static func duplicate(maxDescriptor: Int32 = 1024) throws -> Int32 {
        guard let controlID = utunControlID() else { throw Failure.controlUnavailable }
        let fd = try select(scan(maxDescriptor: maxDescriptor), utunControlID: controlID)
        let copy = fcntl(fd, F_DUPFD_CLOEXEC, 0)
        guard copy >= 0 else { throw Failure.dupFailed(errno) }
        return copy
    }

    /// Every open descriptor up to maxDescriptor that is a connected kernel-control socket.
    public static func scan(maxDescriptor: Int32 = 1024) -> [Candidate] {
        var found: [Candidate] = []
        for fd in 0...maxDescriptor {
            var address = SockaddrCtl()
            var length = socklen_t(MemoryLayout<SockaddrCtl>.size)
            let peer = withUnsafeMutablePointer(to: &address) {
                $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { getpeername(fd, $0, &length) }
            }
            guard peer == 0, address.sc_family == UInt8(afSystem) else { continue }
            found.append(Candidate(fd: fd, controlID: address.sc_id, interfaceName: interfaceName(fd)))
        }
        return found
    }

    /// The kernel's id for the utun control, from a throwaway control socket.
    public static func utunControlID() -> UInt32? {
        let socket = Darwin.socket(afSystem, SOCK_DGRAM, sysprotoControl)
        guard socket >= 0 else { return nil }
        defer { close(socket) }
        var info = CtlInfo()
        withUnsafeMutableBytes(of: &info.ctl_name) { name in
            let control = Array(utunControlName.utf8)
            name.copyBytes(from: control.prefix(name.count - 1))
        }
        guard ioctl(socket, ctliocginfo, &info) == 0 else { return nil }
        return info.ctl_id
    }

    static func interfaceName(_ fd: Int32) -> String? {
        var buffer = [CChar](repeating: 0, count: Int(IFNAMSIZ))
        var length = socklen_t(buffer.count)
        guard getsockopt(fd, sysprotoControl, utunOptIfname, &buffer, &length) == 0 else { return nil }
        return String(cString: buffer)
    }

    // <sys/kern_control.h> and <net/if_utun.h> are not in the iOS SDK's Swift view; these are
    // their stable ABI values.
    static let afSystem: Int32 = 32
    static let sysprotoControl: Int32 = 2
    static let utunOptIfname: Int32 = 2
    static let utunControlName = "com.apple.net.utun_control"
    /// _IOWR('N', 3, struct ctl_info)
    static let ctliocginfo: UInt = 0xC064_4E03

    /// struct ctl_info: 4 + 96 bytes. The name is 24 UInt32 so no padding follows ctl_id.
    struct CtlInfo {
        var ctl_id: UInt32 = 0
        var ctl_name: (UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32,
                       UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32, UInt32) =
            (0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
    }

    struct SockaddrCtl {
        var sc_len: UInt8 = 0
        var sc_family: UInt8 = 0
        var ss_sysaddr: UInt16 = 0
        var sc_id: UInt32 = 0
        var sc_unit: UInt32 = 0
        var sc_reserved: (UInt32, UInt32, UInt32, UInt32, UInt32) = (0, 0, 0, 0, 0)
    }
}
