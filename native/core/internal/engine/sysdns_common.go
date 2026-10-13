package engine

import "errors"

// TunnelDNS is inside the TUN's 198.18.0.1/30 and below the fake-ip pool, which starts at
// 198.18.0.4, so it is never handed out as a fake address. On macOS the Core points the
// hardware network services at it (sysdns.go); on Windows the pinned sing-tun gives the
// TUN adapter exactly this DNS server (the next address after the TUN's own) and the
// Core verifies it (sysdns_windows.go).
const TunnelDNS = "198.18.0.2"

// System DNS modes reported for diagnostics and the privileged acceptance harness.
const (
	DNSCaptured = "captured" // system resolver points into the tunnel
)

// ErrDNSCapture means the system resolver could not be pointed into the tunnel;
// connecting anyway would leak every lookup, so Start fails instead.
var ErrDNSCapture = errors.New("system dns could not be captured")
