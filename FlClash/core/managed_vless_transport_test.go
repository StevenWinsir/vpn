//go:build !cgo

package main

import (
	"bytes"
	"context"
	"core/managed"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener/inbound"
	"vpn/nodepolicy"
)

type protocolEchoTunnel struct {
	C.Tunnel
	payload  []byte
	accepted atomic.Int32
}

func (t *protocolEchoTunnel) HandleTCPConn(conn net.Conn, metadata *C.Metadata) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	// No forwarding is performed: only this synthetic loopback destination is allowed.
	if metadata.DstIP != netip.MustParseAddr("127.0.0.1") || metadata.DstPort != 12345 {
		return
	}
	data := make([]byte, len(t.payload))
	if _, err := io.ReadFull(conn, data); err != nil || !bytes.Equal(data, t.payload) {
		return
	}
	t.accepted.Add(1)
	_, _ = conn.Write(data)
}

func TestManagedProtocolVLESSRealLoopbackAuthentication(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(fmt.Sprintf("correct-uuid=%t", allowed), func(t *testing.T) {
			const fixtureUUID = "11111111-2222-4333-8444-555555555555"
			payload := bytes.Repeat([]byte("owned-vless-fixture\n"), 1024)
			target := &protocolEchoTunnel{payload: payload}
			server, err := inbound.NewVless(&inbound.VlessOption{
				// Test-only, loopback-only plain TCP to isolate UUID authentication.
				// Managed proxy validation never enables an inbound listener.
				AllowInsecure: true,
				BaseOption:    inbound.BaseOption{NameStr: "fixture", Listen: "127.0.0.1", Port: "0"},
				Users:         []inbound.VlessUser{{Username: "fixture", UUID: fixtureUUID}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := server.Listen(target); err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			host, portString, err := net.SplitHostPort(server.Address())
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(portString)
			if err != nil {
				t.Fatal(err)
			}
			id := fixtureUUID
			if !allowed {
				id = "22222222-3333-4444-8555-666666666666"
			}
			mapping := map[string]any{"name": "vless-fixture", "type": "vless", "server": host, "port": port, "uuid": id, "network": "tcp"}
			if err := nodepolicy.Validate(mapping); err != nil {
				t.Fatal(err)
			}
			proxy, err := adapter.ParseProxy(mapping)
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, err := proxy.DialContext(ctx, &C.Metadata{NetWork: C.TCP, DstIP: netip.MustParseAddr("127.0.0.1"), DstPort: 12345})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, err = conn.Write(payload)
			got := make([]byte, len(payload))
			if err == nil {
				_, err = io.ReadFull(conn, got)
			}
			if allowed {
				if err != nil || !bytes.Equal(got, payload) || target.accepted.Load() != 1 {
					t.Fatalf("VLESS round trip failed: %v", err)
				}
			} else if err == nil || target.accepted.Load() != 0 {
				t.Fatal("wrong UUID reached the echo handler")
			}
		})
	}
}

type managedCloseProbe struct {
	C.Proxy
	closed int
}

func (p *managedCloseProbe) Close() error { p.closed++; return nil }

func TestManagedClearClosesProtocolAdapters(t *testing.T) {
	engine := newManagedTestEngine(t)
	probe := &managedCloseProbe{}
	engine.view = &managed.ConfigurationView{}
	currentConfig = &config.Config{Proxies: map[string]C.Proxy{"fixture": probe}}
	if err := engine.Clear(); err != nil {
		t.Fatal(err)
	}
	if probe.closed != 1 || currentConfig != nil {
		t.Fatal("logout left protocol adapter alive")
	}
	if err := engine.Clear(); err != nil || probe.closed != 1 {
		t.Fatal("repeated clear closed retired configuration")
	}
}
