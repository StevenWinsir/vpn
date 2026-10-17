//go:build darwin && !ios

package network

import (
	"bufio"
	"bytes"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// bindToInterface pins the socket to one interface, bypassing the routing table
// (and therefore the TUN's catch-all routes).
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

func routeGet(args ...string) string {
	out, err := exec.Command("/sbin/route", append([]string{"-n", "get"}, args...)...).Output()
	if err != nil {
		return ""
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		if f := strings.Fields(scanner.Text()); len(f) == 2 && f[0] == "interface:" {
			return f[1]
		}
	}
	return ""
}

// PhysicalDefault is the interface the default route uses, as long as it is not a
// tunnel. `route get default` addresses the 0/0 entry itself, so it keeps answering
// with the physical interface while a TUN's 0/1 + 128/1 split routes are installed.
func PhysicalDefault() (Interface, error) {
	name := routeGet("default")
	if name == "" || strings.HasPrefix(name, "utun") {
		return Interface{}, ErrNoNetwork
	}
	i, err := net.InterfaceByName(name)
	if err != nil || i.Flags&net.FlagUp == 0 {
		return Interface{}, ErrNoNetwork
	}
	return Interface{Name: i.Name, Index: i.Index}, nil
}

// otherTunnels lists up utun devices that carry an IPv4 address (VPN clients; the
// address-less utuns the system keeps for iCloud Private Relay and similar do not count).
func otherTunnels() []string {
	var found []string
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		if !strings.HasPrefix(i.Name, "utun") || i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() {
				found = append(found, i.Name)
				break
			}
		}
	}
	return found
}

// ViaTunnel catches VPN clients that split the default route (0/1 + 128/1)
// and therefore hide behind an untouched `default` entry.
func ViaTunnel() string {
	for _, probe := range []string{"1.1.1.1", "8.8.8.8"} {
		if name := routeGet(probe); strings.HasPrefix(name, "utun") {
			return name
		}
	}
	return ""
}

// platformSignature: the signature is read from the interface itself (network.go).
func platformSignature() (string, bool) { return "", false }

// platformResolvers: until the tunnel records bootstrap servers, /etc/resolv.conf applies.
func platformResolvers() []string { return nil }
