//go:build !windows

package engine

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestRejectPrefixesBeatAndCoverTheTunRoutes(t *testing.T) {
	prefixes := rejectPrefixes()
	if len(prefixes) != 2*len(singTunIPv6Ranges) {
		t.Fatalf("got %d prefixes", len(prefixes))
	}
	for i, tun := range singTunIPv6Ranges {
		low, high := prefixes[2*i], prefixes[2*i+1]
		for _, half := range []netip.Prefix{low, high} {
			if half.Bits() != tun.Bits()+1 || !tun.Contains(half.Addr()) || half.Masked() != half {
				t.Fatalf("%v is not a proper half of %v", half, tun)
			}
		}
		if low == high || low.Overlaps(high) {
			t.Fatalf("halves of %v overlap: %v %v", tun, low, high)
		}
	}
	// Global unicast, where real AAAA answers live, must be refused.
	for _, addr := range []string{"2606:4700:4700::1111", "2001:4860:4860::8888", "240e:35e::1"} {
		covered := false
		for _, p := range prefixes {
			covered = covered || p.Contains(netip.MustParseAddr(addr))
		}
		if !covered {
			t.Fatalf("%s not covered", addr)
		}
	}
	if got := prefixes[10].String() + " " + prefixes[11].String(); got != "2000::/4 3000::/4" {
		t.Fatalf("2000::/3 halves = %s", got)
	}
}

func TestIPv6RejectIsAllOrNothing(t *testing.T) {
	saved := route
	defer func() { route = saved }()
	gateway := netip.MustParseAddr("fdfe:dcba:9876::1")
	var calls []string
	route = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		if len(calls) == 5 {
			return errors.New("refused")
		}
		return nil
	}
	if err := addIPv6Reject(gateway); err == nil {
		t.Fatal("expected failure")
	}
	if calls[0] != "add -inet6 -net 100::/9 fdfe:dcba:9876::1 -reject" {
		t.Fatalf("unexpected argv %q", calls[0])
	}
	// four successful adds, the failing fifth, then the four removed again
	if len(calls) != 9 || calls[5] != "delete -inet6 -net 100::/9 fdfe:dcba:9876::1" {
		t.Fatalf("rollback calls: %q", calls)
	}

	calls = nil
	route = func(args ...string) error { calls = append(calls, strings.Join(args, " ")); return nil }
	if err := addIPv6Reject(gateway); err != nil || len(calls) != 2*len(singTunIPv6Ranges) {
		t.Fatalf("err=%v calls=%d", err, len(calls))
	}
}
