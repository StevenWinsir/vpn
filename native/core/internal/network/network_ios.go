//go:build ios

package network

import (
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// iOS (handoff_app_ios.md 7). The NEPacketTunnelProvider extension owns the tunnel and the
// sandbox cannot run /sbin/route, so the default network (interface, addresses, resolvers)
// comes from the host (NWPathMonitor, from round 2) through the same HostSnapshot format
// Android uses. iOS itself guarantees only one VPN at a time; another VPN taking over
// arrives as stopTunnel(.superceded).

// bindToInterface pins a socket to the physical interface, as on macOS. The extension's own
// sockets are expected to bypass its tunnel anyway (round 2 verifies this on a device);
// the pin makes the control plane independent of that assumption.
func bindToInterface(c syscall.RawConn, network string, index int) error {
	var inner error
	err := c.Control(func(fd uintptr) {
		if strings.HasSuffix(network, "6") {
			inner = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, index)
		} else {
			inner = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, index)
		}
	})
	if err != nil {
		return err
	}
	return inner
}

func hostSnapshot() (HostSnapshot, error) {
	h := currentHost()
	if h == nil {
		return HostSnapshot{}, ErrUnsupported
	}
	return ParseHostSnapshot(h.Snapshot())
}

// PhysicalDefault is the host's default network.
func PhysicalDefault() (Interface, error) {
	s, err := hostSnapshot()
	if err != nil {
		return Interface{}, err
	}
	return s.Egress()
}

// HostResolvers are the default network's own DNS servers for the control plane.
func HostResolvers() []string {
	s, err := hostSnapshot()
	if err != nil {
		return nil
	}
	return s.Resolvers()
}

func platformResolvers() []string { return HostResolvers() }

func platformSignature() (string, bool) {
	s, err := hostSnapshot()
	if err != nil {
		return "none", true
	}
	return s.Signature(), true
}

// ViaTunnel: the routing table is not readable and only one VPN can exist.
func ViaTunnel() string { return "" }

func otherTunnels() []string { return nil }
