package engine

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

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
