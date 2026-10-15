//go:build android

package engine

import (
	"context"
	"errors"
	"fmt"
	"syscall"

	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/listener"

	"asterlink/core/internal/managed"
	"asterlink/core/internal/network"
)

// Android TUN (handoff_app_android.md 6, 7.1, 7.2). The device is not created by sing-tun:
// the app's VpnService.Builder establishes it from a TunSpec (tunspec.go) and hands the
// Core the descriptor, which the template passes to Mihomo as tun.file-descriptor with
// auto-route and auto-detect-interface off. The VpnService routes everything (0.0.0.0/0,
// and ::/0 only when the node carries IPv6) into it; the app itself is not excluded,
// because Verify and the system resolver must take the same path as any app. What keeps
// the Core's own sockets out is protect():
//   - control-plane sockets: network.Egress while a tunnel is up (bindToInterface);
//   - every socket Mihomo opens (the node's outer connection, its UDP relay, the bootstrap
//     lookup of the node's name): dialer.DefaultSocketHook below. Setting the hook also
//     makes the pinned core ignore interface names and routing marks, the path CMFA uses.

var (
	errNoVPNService = errors.New("the VpnService is not running")
	errEstablish    = errors.New("the VpnService could not establish the interface")
)

func init() {
	dialer.DefaultSocketHook = func(_, _ string, c syscall.RawConn) error { return network.ProtectSocket(c) }
}

func platformSupported() bool { return true }

// preflightPlatform: the descriptor can only come from a running VpnService the user
// consented to, which registers itself as the TUN host while it runs.
func preflightPlatform(context.Context) error {
	if currentTunHost() == nil {
		return fmt.Errorf("%w: %w", managed.ErrTunUnavailable, errNoVPNService)
	}
	return nil
}

// attached is the running session's descriptor. Start and stopLocked use it under the
// engine lock; there is only one engine per process.
var attached ownedFD

// attachTun asks the host to establish the interface the section describes and adds the
// descriptor to it.
func attachTun(conf tunConfig) (tunConfig, error) {
	host := currentTunHost()
	if host == nil {
		return conf, errNoVPNService
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
	return conf, nil
}

func releaseTun() { attached.release() }

// tunnelDevices: the device is identified by its descriptor, not by a name.
func tunnelDevices() map[string]bool { return nil }

// tunReady: Mihomo's TUN listener is running on exactly the descriptor this session
// attached. ReCreateTun creates the listener synchronously, so this is true right after
// it unless the listener failed (watchTunErrors then reports why).
func tunReady(map[string]bool) bool {
	running := listener.GetTunConf()
	return attached.set && running.Enable && running.FileDescriptor == attached.fd
}
