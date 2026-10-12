package managed

import (
	"context"
	"errors"

	"asterlink/core/internal/network"
)

// Engine failures with a stable meaning for the state machine.
var (
	// ErrTunUnavailable: this build or platform has no tunnel (stage 2 behaviour, and
	// every non-macOS host).
	ErrTunUnavailable = errors.New("tun unavailable in this build")
	// ErrVPNConflict: another VPN already owns the routes.
	ErrVPNConflict = errors.New("another vpn is active")
	// ErrNoNetwork: no physical default route.
	ErrNoNetwork = errors.New("no physical network")
	// ErrTunnelFailed: the device or its routes never appeared.
	ErrTunnelFailed = errors.New("tunnel did not start")
	// ErrTunnelUnhealthy: the tunnel exists but forwarding through the node failed.
	ErrTunnelUnhealthy = errors.New("tunnel does not forward")
)

// engineCode maps an engine failure to its stable IPC error code.
func engineCode(err error) string {
	switch {
	case errors.Is(err, ErrTunUnavailable):
		return "tun_unavailable"
	case errors.Is(err, ErrVPNConflict):
		return "vpn_conflict"
	case errors.Is(err, ErrNoNetwork):
		return "no_network"
	case errors.Is(err, ErrTunnelUnhealthy):
		return "tunnel_unhealthy"
	default:
		return "tunnel_failed"
	}
}

// Engine is the adapter between the managed state machine and the embedded Mihomo.
// The sequence is fixed: Preflight, Start (only after every authorization gate and the
// first metering acknowledgement), Verify, and later Stop. Start receives the single
// validated Profile and nothing else.
type Engine interface {
	// Preflight checks for conflicting VPNs and returns the physical egress that the
	// tunnel's outer connections and the Core's API calls must use.
	Preflight(ctx context.Context) (network.Interface, error)
	Start(ctx context.Context, profile Profile) error
	// Verify proves the tunnel forwards real traffic through the node.
	Verify(ctx context.Context) error
	// Totals are cumulative proxy-only bytes since the process started (never reset
	// while running, except by an engine restart the meter re-baselines on).
	Totals() (upload, download int64, err error)
	// Stop closes the data path. It is idempotent and must not block on the network.
	Stop() error
}

type unavailableEngine struct{}

func (unavailableEngine) Preflight(context.Context) (network.Interface, error) {
	return network.Interface{}, nil
}
func (unavailableEngine) Start(context.Context, Profile) error { return ErrTunUnavailable }
func (unavailableEngine) Verify(context.Context) error         { return ErrTunUnavailable }
func (unavailableEngine) Totals() (int64, int64, error)        { return 0, 0, nil }
func (unavailableEngine) Stop() error                          { return nil }

// UnavailableEngine refuses to start; it backs builds without a tunnel component.
func UnavailableEngine() Engine { return unavailableEngine{} }
