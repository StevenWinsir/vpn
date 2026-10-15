// Package integration_test exercises the node with the actual pinned client, not
// billtest's independent wire implementation. No TUN, API or public network is used.
package integration_test

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"asterlink/nodeagent/forwarder"
	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
)

const uuidA = "11111111-2222-4333-8444-555555555555"
const uuidB = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

func echoUDP(t *testing.T) *net.UDPAddr {
	t.Helper()
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFromUDP(b)
			if err != nil {
				return
			}
			if _, err = pc.WriteToUDP(b[:n], from); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { pc.Close(); <-done })
	return pc.LocalAddr().(*net.UDPAddr)
}

func node(t *testing.T, allowPrivate bool) *forwarder.VLESS {
	t.Helper()
	v, err := forwarder.NewVLESS(forwarder.VLESSOptions{Listen: "127.0.0.1:0", AllowPrivateTargets: allowPrivate, WatchInterval: 10 * time.Millisecond, IdleTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

func grant(t *testing.T, v *forwarder.VLESS, id, uuid string, cap int64, ttl time.Duration) {
	t.Helper()
	if err := v.Install(forwarder.Grant{ID: id, Protocol: "vless", Credential: map[string]string{"uuid": uuid}, EpochBudget: cap, Deadline: time.Now().Add(ttl)}); err != nil {
		t.Fatal(err)
	}
}

func mihomoUDP(t *testing.T, v *forwarder.VLESS, uuid string, dst *net.UDPAddr, source bool) C.PacketConn {
	t.Helper()
	host, port, _ := net.SplitHostPort(v.Addr().String())
	p, _ := strconv.Atoi(port)
	// Deliberately omit PacketEncoding/XUDP: this is the production default that
	// previously failed while the independent command-2 client passed.
	a, err := outbound.NewVless(outbound.VlessOption{Name: "managed", Server: host, Port: p, UUID: uuid, UDP: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	m := &C.Metadata{NetWork: C.UDP, DstIP: dst.AddrPort().Addr(), DstPort: uint16(dst.Port)}
	if source {
		m.SrcIP = netip.MustParseAddr("192.0.2.1")
		m.SrcPort = 12345
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pc, err := a.ListenPacketContext(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	return pc
}

func exchange(pc net.PacketConn, dst *net.UDPAddr, body []byte) error {
	if err := pc.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	if _, err := pc.WriteTo(body, dst); err != nil {
		return err
	}
	b := make([]byte, 65535)
	n, from, err := pc.ReadFrom(b)
	if err != nil {
		return err
	}
	if !bytes.Equal(body, b[:n]) || from.String() != dst.String() {
		return &echoError{}
	}
	return nil
}

type echoError struct{}

func (*echoError) Error() string { return "UDP payload or source address mismatch" }

func TestMihomoXUDPRoundTripAndPerGrantCounters(t *testing.T) {
	v := node(t, true)
	a, b := echoUDP(t), echoUDP(t)
	grant(t, v, "a", uuidA, 1<<20, time.Minute)
	grant(t, v, "b", uuidB, 1<<20, time.Minute)
	ca, cb := mihomoUDP(t, v, uuidA, a, true), mihomoUDP(t, v, uuidB, b, false)
	// New with global ID, New without one, Keep, destination change, then back.
	for _, tc := range []struct {
		pc   net.PacketConn
		dst  *net.UDPAddr
		size int
	}{{ca, a, 17}, {cb, b, 31}, {ca, b, 700}, {ca, a, 4096}} {
		if err := exchange(tc.pc, tc.dst, bytes.Repeat([]byte("x"), tc.size)); err != nil {
			t.Fatalf("real pinned Mihomo XUDP round trip (%d bytes): %v", tc.size, err)
		}
	}
	wantA, wantB := (forwarder.Counters{Upload: 4813, Download: 4813}), (forwarder.Counters{Upload: 31, Download: 31})
	if got := v.Usage(); got["a"] != wantA || got["b"] != wantB {
		t.Fatalf("payload-only grant counters: %+v", got)
	}
	final, ok := v.Remove("a")
	if !ok || final != wantA {
		t.Fatalf("final counters: %+v, %v", final, ok)
	}
}
