package network

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"sync"
)

// Host is implemented by the mobile binding (native/core/mobile) on top of the Kotlin
// VpnService. Only the Android build consults it (network_android.go); it is declared
// here so the binding compiles and is tested on every host.
type Host interface {
	// Protect excludes the socket from the VPN; false means it could not be protected.
	Protect(fd int) bool
	// Snapshot reports the current default network (ParseHostSnapshot format).
	Snapshot() string
}

var (
	hostMu sync.RWMutex
	host   Host
)

// SetHost installs (or, with nil, removes) the mobile host.
func SetHost(h Host) {
	hostMu.Lock()
	host = h
	hostMu.Unlock()
}

func currentHost() Host {
	hostMu.RLock()
	defer hostMu.RUnlock()
	return host
}

// HostSnapshot is the physical network as a mobile host reports it. Android apps cannot
// read the routing table or enumerate interfaces through netlink (Android 11+), so the
// app's ConnectivityManager view of the default network is the only reliable source; the
// Kotlin side serialises it and the Core decodes it strictly here. It carries no node or
// account data. The decoder is platform-neutral so every host can test it.
type HostSnapshot struct {
	// Available is false when the system has no default network.
	Available bool `json:"available"`
	// Interface and Index identify the default network's link (LinkProperties and
	// java.net.NetworkInterface); both are required while Available.
	Interface string `json:"interface"`
	Index     int    `json:"index"`
	// Addresses are the link's own addresses; DNS the link's resolvers.
	Addresses []string `json:"addresses"`
	DNS       []string `json:"dns"`
	// Metered reports NET_CAPABILITY_NOT_METERED absent (cellular, hotspot).
	Metered bool `json:"metered"`
}

var (
	// ErrHostSnapshot means the host's network report was malformed; the caller treats
	// that as "no network" rather than guessing.
	ErrHostSnapshot = errors.New("malformed host network snapshot")
	interfaceName   = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,32}$`)
)

// maxHostSnapshot bounds the report; a real one is a few hundred bytes.
const maxHostSnapshot = 8 << 10

// ParseHostSnapshot strictly decodes a host report: unknown fields, trailing data,
// unparsable addresses and an available network without a link are all errors.
func ParseHostSnapshot(raw string) (HostSnapshot, error) {
	if len(raw) == 0 || len(raw) > maxHostSnapshot {
		return HostSnapshot{}, ErrHostSnapshot
	}
	var s HostSnapshot
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&s) != nil || decoder.More() {
		return HostSnapshot{}, ErrHostSnapshot
	}
	if !s.Available {
		return HostSnapshot{}, nil
	}
	if !interfaceName.MatchString(s.Interface) || s.Index <= 0 || len(s.Addresses) > 32 || len(s.DNS) > 16 {
		return HostSnapshot{}, ErrHostSnapshot
	}
	for _, list := range [][]string{s.Addresses, s.DNS} {
		for _, a := range list {
			if _, err := netip.ParseAddr(a); err != nil {
				return HostSnapshot{}, ErrHostSnapshot
			}
		}
	}
	return s, nil
}

// Egress returns the physical egress, or ErrNoNetwork when the host has none.
func (s HostSnapshot) Egress() (Interface, error) {
	if !s.Available {
		return Interface{}, ErrNoNetwork
	}
	return Interface{Name: s.Interface, Index: s.Index}, nil
}

// Signature is the same identity desktop hosts compute from the interface itself
// (signatureFrom): IPv4 addresses individually, IPv6 only by /64.
func (s HostSnapshot) Signature() string {
	if !s.Available {
		return "none"
	}
	var addrs []netip.Addr
	for _, a := range s.Addresses {
		if ip, err := netip.ParseAddr(a); err == nil {
			addrs = append(addrs, ip)
		}
	}
	return signatureFrom(s.Interface, addrs)
}

// Resolvers are the link's DNS servers the control plane may query directly, filtered
// by the same rules as a desktop adapter (BootstrapResolvers).
func (s HostSnapshot) Resolvers() []string {
	adapter := Adapter{Name: s.Interface}
	for _, a := range s.DNS {
		if ip, err := netip.ParseAddr(a); err == nil {
			adapter.DNS = append(adapter.DNS, ip)
		}
	}
	return BootstrapResolvers(adapter)
}
