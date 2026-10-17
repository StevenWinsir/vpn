import Darwin
import Testing
@testable import AsterLinkKit

/// The real scan against a real utun, where the host lets this process create one. macOS
/// requires root for that (measured: EPERM unprivileged), so the usual run records the refusal
/// and the selection rule is covered by utunSelection; `sudo swift test` exercises the scan
/// itself. The iOS extension gets its utun from setTunnelNetworkSettings. The interface exists
/// only while the socket is open and has no address or route, so the host's network is untouched.
struct UtunScanTests {
    @Test func duplicateFindsTheOneUtunAndDupsIt() throws {
        let controlID = try #require(UtunDescriptor.utunControlID())
        // No utun yet: nothing to hand over.
        #expect(throws: UtunDescriptor.Failure.notFound) { _ = try UtunDescriptor.duplicate() }
        let control = socket(UtunDescriptor.afSystem, SOCK_DGRAM, UtunDescriptor.sysprotoControl)
        try #require(control >= 0)
        defer { close(control) }
        var address = UtunDescriptor.SockaddrCtl()
        address.sc_len = UInt8(MemoryLayout<UtunDescriptor.SockaddrCtl>.size)
        address.sc_family = UInt8(UtunDescriptor.afSystem)
        address.ss_sysaddr = 2 // AF_SYS_CONTROL
        address.sc_id = controlID
        address.sc_unit = 0 // the kernel picks utunN
        let connected = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(control, $0, socklen_t(MemoryLayout<UtunDescriptor.SockaddrCtl>.size)) }
        }
        guard connected == 0 else {
            // Recorded, not assumed: some hosts refuse unprivileged utun creation.
            print("UTUN-SCAN: host refused a utun (errno \(errno)); selection is covered by utunSelection")
            return
        }
        let name = try #require(UtunDescriptor.interfaceName(control))
        #expect(name.hasPrefix("utun"))
        let copy = try UtunDescriptor.duplicate()
        defer { close(copy) }
        #expect(copy != control)
        #expect(UtunDescriptor.interfaceName(copy) == name)
        print("UTUN-SCAN: found \(name) on fd \(control), dup \(copy)")
    }
}
