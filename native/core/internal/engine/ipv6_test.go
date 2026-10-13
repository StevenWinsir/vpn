package engine

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
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

// fakeTarget behaves like a node's far side for one IPv6 destination.
func fakeTarget(reply string) dialFunc {
	return func(context.Context, *C.Metadata) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			buf := make([]byte, 512)
			_, _ = server.Read(buf)
			if reply != "" {
				_, _ = server.Write([]byte(reply))
			}
		}()
		return client, nil
	}
}

func TestOnlyARealHTTPAnswerCountsAsIPv6(t *testing.T) {
	addr := netip.MustParseAddrPort("[2606:4700:4700::1111]:80")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !httpAnswers(ctx, fakeTarget("HTTP/1.1 301 Moved\r\n\r\n"), addr) {
		t.Fatal("an HTTP response must count")
	}
	// What an IPv4-only VLESS node does: the dial succeeds, then EOF.
	if httpAnswers(ctx, fakeTarget(""), addr) {
		t.Fatal("EOF after a successful dial must not count")
	}
	if httpAnswers(ctx, fakeTarget("SSH-2.0-x\r\n"), addr) {
		t.Fatal("non-HTTP bytes must not count")
	}
	refused := func(context.Context, *C.Metadata) (net.Conn, error) { return nil, errors.New("no route") }
	if httpAnswers(ctx, refused, addr) {
		t.Fatal("a failed dial must not count")
	}
}
