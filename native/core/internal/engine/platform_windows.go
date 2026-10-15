//go:build windows

package engine

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows"

	"asterlink/core/internal/managed"
	"asterlink/core/internal/network"
)

func platformSupported() bool { return true }

// preflightPlatform: creating a Wintun adapter and the WFP session needs an elevated
// token. SYSTEM (the Service) and an administrator's elevated process both qualify; an
// ordinary user process does not, and failing here is clearer than a creation error
// after the timeout.
func preflightPlatform(context.Context) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("%w: administrator rights are required to create the TUN adapter", managed.ErrTunUnavailable)
	}
	return nil
}

// attachTun: sing-tun creates the Wintun adapter itself; the section is used as it is.
func attachTun(conf tunConfig) (tunConfig, error) { return conf, nil }

// releaseTun: the adapter goes with the listener.
func releaseTun() {}

// tunnelDevices lists the Core's own adapter if it exists. Windows identifies it by name
// (a stale one from a killed Core keeps the name), unlike macOS where utunN is unique.
func tunnelDevices() map[string]bool {
	found := map[string]bool{}
	if network.OwnTunnelUp() {
		found[network.TunName] = true
	}
	return found
}

// tunReady: the adapter is up AND the routing table already sends public destinations
// through it. That second condition is the real readiness signal: an adapter without its
// split routes would let traffic out of the physical interface.
func tunReady(map[string]bool) bool {
	return network.OwnTunnelUp() && network.ViaTunnel() == network.TunName
}
