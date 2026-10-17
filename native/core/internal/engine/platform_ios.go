//go:build ios

package engine

import (
	"context"
	"errors"
	"fmt"

	"asterlink/core/internal/managed"
)

// iOS TUN (handoff_app_ios.md 7, 8.2). The device belongs to the NEPacketTunnelProvider
// extension: it applies the TunSpec through setTunnelNetworkSettings and hands the Core a
// dup() of the utun descriptor. Round 1 has no provider yet, so the platform reports itself
// unsupported and connect ends in tun_unavailable. Nothing here pretends to have a tunnel.

var errNoTunnelProvider = errors.New("the iOS tunnel provider is not implemented yet")

func platformSupported() bool { return false }

func preflightPlatform(context.Context) error {
	return fmt.Errorf("%w: %w", managed.ErrTunUnavailable, errNoTunnelProvider)
}

func attachTun(conf tunConfig) (tunConfig, error) { return conf, errNoTunnelProvider }

func releaseTun() {}

func tunnelDevices() map[string]bool { return nil }

func tunReady(map[string]bool) bool { return false }
