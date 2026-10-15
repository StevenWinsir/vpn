//go:build android

package network

import (
	"errors"
	"syscall"
)

// Android (handoff_app_android.md 2.3, 6). The app process owns the VpnService, so the
// two things desktop builds read from the kernel come from the host instead:
//   - "binding" a socket to the physical network is VpnService.protect(fd): a protected
//     socket bypasses the VPN's routes, which is what keeps API calls and the node's
//     outer connection out of the tunnel;
//   - the default network (link, addresses, resolvers) comes from ConnectivityManager,
//     because an ordinary app cannot read netlink routes on Android 11+.
// Android itself guarantees that only one VPN is active at a time, so there is no
// competing tunnel to detect; another VPN taking over arrives as VpnService.onRevoke.

// ErrNotProtected means the host refused or was unable to protect a socket. Such a
// socket would route into the VPN, so the dial fails instead.
var ErrNotProtected = errors.New("socket could not be protected from the vpn")

func bindToInterface(c syscall.RawConn, _ string, _ int) error { return ProtectSocket(c) }

// ProtectSocket excludes one socket from the VPN through the host. It is also Mihomo's
// dialer.DefaultSocketHook on Android (engine/platform_android.go), so the node's outer
// connection, its UDP relay and the bootstrap lookup of the node's name never enter the
// TUN they carry: the pinned core disables its loopback detector on Android, so this is
// the only thing preventing a routing loop (T05-loop).
func ProtectSocket(c syscall.RawConn) error {
	h := currentHost()
	if h == nil {
		return ErrNotProtected
	}
	protected := false
	if err := c.Control(func(fd uintptr) { protected = h.Protect(int(fd)) }); err != nil {
		return err
	}
	if !protected {
		return ErrNotProtected
	}
	return nil
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

// platformResolvers: there is no /etc/resolv.conf, so control-plane lookups always use the
// default network's servers (over protected sockets while the VPN is up).
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
