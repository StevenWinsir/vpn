package managed

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"
)

type SocketControl func(network, address string, connection syscall.RawConn) error

func NewTransport(control SocketControl) *http.Transport {
	return NewTransportWithDNS(control, nil)
}

func NewTransportWithDNS(control SocketControl, servers func() []string) *http.Transport {
	dnsDialer := &net.Dialer{Timeout: 5 * time.Second, Control: control}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if servers == nil {
				return dnsDialer.DialContext(ctx, network, address)
			}
			for _, server := range servers() {
				host, port, err := net.SplitHostPort(server)
				if err != nil {
					host, port = server, "53"
				}
				if net.ParseIP(host) == nil {
					continue
				}
				connection, err := dnsDialer.DialContext(ctx, network, net.JoinHostPort(host, port))
				if err == nil {
					return connection, nil
				}
			}
			return nil, errors.New("managed DNS unavailable")
		},
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second, Resolver: resolver, Control: control}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		IdleConnTimeout:       15 * time.Second,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		MaxConnsPerHost:       4,
		ForceAttemptHTTP2:     true,
	}
}
