//go:build ios

package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"syscall"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener"
	"github.com/metacubex/mihomo/tunnel"

	"asterlink/core/internal/managed"
)

// iOS TUN (handoff_app_ios.md 7, 8.2). The utun belongs to the NEPacketTunnelProvider
// extension: Platform.OpenTun applies the TunSpec through setTunnelNetworkSettings, finds the
// utun descriptor and returns a dup() of it. The extension keeps the original, so the
// interface survives the listener closing its copy (a changed line or a reopened gate rebuilds
// the listener on a new dup). The Core owns the copy: Mihomo's listener closes it, or, when a
// start fails earlier, releaseTun does (ownedFD's identity check prevents a double close).
//
// Outbound sockets need no protect(): an extension's own traffic does not enter its own
// tunnel (to be confirmed on a device, round 2), and the control plane additionally pins its
// sockets to the physical interface (network_ios.go). A node without IPv6 gets the ingress
// relay (ipv6relay_unix.go) between the utun and the stack.

var (
	errNoTunnelProvider = errors.New("the packet tunnel provider is not running")
	errEstablish        = errors.New("the packet tunnel provider could not apply the tunnel settings")
)

func init() {
	// Verify's first stage (8.5): the extension's own sockets bypass its tunnel, so the probe
	// is carried by the node in process. The app proves the system route separately.
	verifyDial = dialThroughNode
}

func platformSupported() bool { return true }

// preflightPlatform: the descriptor can only come from a running provider, which registers
// itself as the TUN host while it runs. The app process never has one.
func preflightPlatform(context.Context) error {
	if currentTunHost() == nil {
		return fmt.Errorf("%w: %w", managed.ErrTunUnavailable, errNoTunnelProvider)
	}
	return nil
}

var (
	// attached is the session's utun copy; inner the relay's stack-side descriptor when the
	// relay runs. Start and stopLocked use them under the engine lock (one engine per process).
	attached, inner ownedFD
	relay           *ipv6Relay
	// rejectIPv6 is set by prepareTunConf before attachTun: the node cannot carry IPv6.
	rejectIPv6 bool
)

func attachTun(conf tunConfig) (tunConfig, error) {
	host := currentTunHost()
	if host == nil {
		return conf, errNoTunnelProvider
	}
	spec, err := tunSpecFor(conf)
	if err != nil {
		return conf, err
	}
	doc, err := spec.encode()
	if err != nil {
		return conf, err
	}
	fd := host.OpenTun(doc)
	if fd <= 0 {
		return conf, errEstablish
	}
	owned, err := adoptFD(fd)
	if err != nil {
		return conf, fmt.Errorf("%w: %w", errEstablish, err)
	}
	attached = owned
	conf.FileDescriptor = fd
	if !rejectIPv6 {
		return conf, nil
	}
	r, stackFD, err := startIPv6Relay(fd, int(conf.MTU))
	if err != nil {
		return conf, fmt.Errorf("%w: ipv6 relay: %w", errEstablish, err)
	}
	if inner, err = adoptFD(stackFD); err != nil {
		r.stop()
		_ = syscall.Close(stackFD)
		return conf, fmt.Errorf("%w: ipv6 relay: %w", errEstablish, err)
	}
	relay = r
	// recvmsg_x/sendmsg_x and the utun name lookup only work on a utun.
	conf.FileDescriptor, conf.RecvMsgX, conf.SendMsgX = stackFD, false, false
	return conf, nil
}

// releaseTun runs after the listener closed: the relay closes the utun copy it owned, and
// whatever the listener never took is closed here.
func releaseTun() {
	relay.stop()
	relay = nil
	inner.release()
	attached.release()
}

func tunnelDevices() map[string]bool { return nil }

// tunReady: Mihomo's TUN listener runs on exactly the descriptor this session handed it.
func tunReady(map[string]bool) bool {
	running := listener.GetTunConf()
	want := attached
	if relay != nil {
		want = inner
	}
	return want.set && running.Enable && running.FileDescriptor == want.fd
}

// dialThroughNode opens a TCP connection to address through the configured node, in process.
func dialThroughNode(ctx context.Context, network, address string) (net.Conn, error) {
	proxy, ok := tunnel.Proxies()[ProxyName]
	if !ok {
		return nil, errors.New("no node configured")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return nil, err
	}
	metadata := &C.Metadata{NetWork: C.TCP, Host: host, DstPort: uint16(port)}
	if ip, err := netip.ParseAddr(host); err == nil {
		metadata.Host, metadata.DstIP = "", ip
	}
	return proxy.DialContext(ctx, metadata)
}
