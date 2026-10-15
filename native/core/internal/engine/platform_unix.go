//go:build !windows && !android

package engine

import (
	"context"
	"net"
	"runtime"
	"strings"

	"asterlink/core/internal/network"
)

// platformSupported: only macOS has a TUN implementation off Windows.
func platformSupported() bool { return runtime.GOOS == "darwin" }

// preflightPlatform has nothing to add on macOS: root is the Helper's concern.
func preflightPlatform(context.Context) error { return nil }

func tunnelDevices() map[string]bool {
	found := map[string]bool{}
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		if strings.HasPrefix(i.Name, "utun") {
			found[i.Name] = true
		}
	}
	return found
}

// tunReady: a new utun device exists and the routing table already sends public
// destinations into a tunnel.
func tunReady(before map[string]bool) bool {
	for name := range tunnelDevices() {
		if !before[name] && network.ViaTunnel() != "" {
			return true
		}
	}
	return false
}
