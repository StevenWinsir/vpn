//go:build !(android && cgo)

package main

import (
	"context"
	"core/managed"
	"net"
	"net/http"
	"runtime"

	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/component/resolver"
)

func newManagedTransport() http.RoundTripper {
	transport := managed.NewTransport(nil)
	if runtime.GOOS == "darwin" {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address, dialer.WithResolver(resolver.ProxyServerHostResolver))
		}
	}
	return transport
}
func stopManagedPlatform() {}
