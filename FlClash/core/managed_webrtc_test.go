//go:build !cgo

package main

import (
	"bytes"
	"context"
	"core/managed"
	"errors"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"
)

// Use real UDP sockets and a STUN binding request on an arbitrary destination
// port. Blocking only 3478/19302 cannot satisfy this regression. The explicit
// DIRECT rule is a test-only positive control, never a catalog configuration.
func TestManagedWebRTCSTUNCannotFallBackToDirect(t *testing.T) {
	for _, family := range []struct{ network, host, cidr string }{
		{"udp4", "127.0.0.1", "IP-CIDR,127.0.0.1/32"},
		{"udp6", "::1", "IP-CIDR6,::1/128"},
	} {
		for _, fixture := range []struct {
			name, proxy string
			direct      bool
		}{
			{"positive-control", "type: http, server: 127.0.0.1, port: 9", true},
			{"http-no-udp", "type: http, server: 127.0.0.1, port: 9", false},
			{"ss-udp-disabled", "type: ss, server: 127.0.0.1, port: 9, cipher: aes-128-gcm, password: test-only, udp: false", false},
		} {
			t.Run(family.network+"/"+fixture.name, func(t *testing.T) {
				sink, err := net.ListenPacket(family.network, net.JoinHostPort(family.host, "0"))
				if err != nil {
					t.Fatal(err)
				}
				defer sink.Close()
				target := sink.LocalAddr().(*net.UDPAddr)
				rules := "'MATCH,VPN'"
				if fixture.direct {
					rules = fmt.Sprintf("'%s,DIRECT', %s", family.cidr, rules)
				}
				yaml := fmt.Sprintf("ipv6: true\nproxies: [{name: Selected, %s}]\nproxy-groups: [{name: VPN, type: select, proxies: [Selected]}]\nrules: [%s]\n", fixture.proxy, rules)
				engine := newManagedTestEngine(t)
				prepared, err := engine.Prepare(context.Background(), managedProfileFixture(yaml))
				if err != nil {
					t.Fatal(err)
				}
				view, err := prepared.Apply(context.Background(), managed.ConfigurationOwner{Generation: 1, UserID: "stun-test", SessionID: "session-a"})
				if err != nil {
					t.Fatal(err)
				}
				reservation, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				port := reservation.Addr().(*net.TCPAddr).Port
				if err := reservation.Close(); err != nil {
					t.Fatal(err)
				}
				if err := engine.Start(context.Background(), view.Owner, view.ID, port); err != nil {
					t.Fatal(err)
				}
				client, err := net.Dial("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				// RFC 5389 header: Binding Request, no attributes, magic cookie,
				// and a 96-bit transaction ID. No external STUN service is contacted.
				stun := []byte{0, 1, 0, 0, 0x21, 0x12, 0xa4, 0x42, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
				packet := []byte{0, 0, 0}
				if family.network == "udp4" {
					packet = append(packet, 1)
					packet = append(packet, target.IP.To4()...)
				} else {
					packet = append(packet, 4)
					packet = append(packet, target.IP.To16()...)
				}
				packet = append(packet, byte(target.Port>>8), byte(target.Port))
				packet = append(packet, stun...)
				if _, err := client.Write(packet); err != nil {
					t.Fatal(err)
				}
				if err := sink.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				buffer := make([]byte, 256)
				n, _, err := sink.ReadFrom(buffer)
				if fixture.direct {
					if err != nil || !bytes.Equal(buffer[:n], stun) {
						t.Fatalf("positive control did not deliver the actual STUN request: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatal("selected node cannot carry UDP, but STUN escaped over DIRECT")
				}
				var netErr net.Error
				if !errors.As(err, &netErr) || !netErr.Timeout() {
					t.Fatalf("unexpected UDP observation: %v", err)
				}
			})
		}
	}
}
