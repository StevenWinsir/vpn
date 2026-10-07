//go:build !cgo

package main

import (
	"bytes"
	"context"
	"core/managed"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/net/packet"
	SS "github.com/metacubex/mihomo/transport/shadowsocks/core"
	"github.com/metacubex/mihomo/transport/socks5"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

// A local encrypted SS peer answers STUN itself, without contacting a public
// service. This proves the fix preserves UDP-capable nodes and proxy accounting;
// it is not a claim about a commercial node's public exit IP.
func TestManagedWebRTCSTUNUsesEncryptedUDPAndMetering(t *testing.T) {
	for _, algorithm := range []string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"} {
		for _, destination := range []string{"198.19.254.2:32123", "[2001:db8:ffff::2]:32123"} {
			t.Run(algorithm+"/"+destination, func(t *testing.T) {
				raw, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer raw.Close()
				cipher, err := SS.PickCipher(algorithm, nil, "isolated-stun-fixture")
				if err != nil {
					t.Fatal(err)
				}
				peer := cipher.PacketConn(packet.NewEnhancePacketConn(raw))
				if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				engine := newManagedTestEngine(t)
				yaml := fmt.Sprintf("ipv6: true\nproxies: [{name: Selected, type: ss, server: 127.0.0.1, port: %d, cipher: %s, password: isolated-stun-fixture, udp: true}]\nproxy-groups: [{name: VPN, type: select, proxies: [Selected]}]\nrules: ['MATCH,VPN']\n", raw.LocalAddr().(*net.UDPAddr).Port, algorithm)
				prepared, err := engine.Prepare(context.Background(), managedProfileFixture(yaml))
				if err != nil {
					t.Fatal(err)
				}
				view, err := prepared.Apply(context.Background(), managed.ConfigurationOwner{Generation: 1, UserID: "stun-test", SessionID: "session-a"})
				if err != nil {
					t.Fatal(err)
				}
				port := managedTestPort(t)
				if err := engine.Start(context.Background(), view.Owner, view.ID, port); err != nil {
					t.Fatal(err)
				}
				client, err := net.Dial("udp4", fmt.Sprintf("127.0.0.1:%d", port))
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				if err := client.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				request := []byte{0, 1, 0, 0, 0x21, 0x12, 0xa4, 0x42, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
				address := socks5.ParseAddr(destination)
				outbound := append(append([]byte{0, 0, 0}, address...), request...)
				beforeUp, beforeDown := statistic.DefaultManager.TotalTraffic(true)
				if _, err := client.Write(outbound); err != nil {
					t.Fatal(err)
				}
				buffer := make([]byte, 1024)
				n, source, err := peer.ReadFrom(buffer)
				if err != nil || !bytes.Equal(buffer[:n], append(append([]byte{}, address...), request...)) {
					t.Fatalf("STUN did not reach the selected encrypted SS peer: %v", err)
				}
				// Binding Success with a synthetic documentation-only mapped address.
				response := append([]byte{}, request...)
				response[0], response[3] = 1, 12
				response = append(response, 0, 0x20, 0, 8, 0, 1, 0x7d, 0x29, 203^0x21, 0^0x12, 113^0xa4, 9^0x42)
				if _, err := peer.WriteTo(append(append([]byte{}, address...), response...), source); err != nil {
					t.Fatal(err)
				}
				n, err = client.Read(buffer)
				expected := append(append([]byte{0, 0, 0}, address...), response...)
				if err != nil || !bytes.Equal(buffer[:n], expected) {
					t.Fatalf("STUN response did not return through the managed proxy: %v", err)
				}
				deadline := time.Now().Add(time.Second)
				for {
					up, down := statistic.DefaultManager.TotalTraffic(true)
					if up-beforeUp >= int64(len(request)) && down-beforeDown >= int64(len(response)) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("proxied STUN escaped metering: upload=%d download=%d", up-beforeUp, down-beforeDown)
					}
					time.Sleep(time.Millisecond)
				}
			})
		}
	}
}
