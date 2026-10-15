package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	C "github.com/metacubex/mihomo/constant"
)

// The node really forwards for its generated credentials, counts what it carried, refuses
// a wrong key, and relays UDP over Shadowsocks. REALITY needs a reachable TLS server and is
// covered by the emulator suite instead.
func TestNodeForwardsAndCountsForItsOwnCredentialsOnly(t *testing.T) {
	r, stop, err := start("127.0.0.1", "127.0.0.1:0", "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	body := strings.Repeat("x", 200<<10)
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = http.Serve(target, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }))
	}()
	defer target.Close()
	addr := netip.MustParseAddrPort(target.Addr().String())

	clients := map[string]map[string]any{
		"ss":    {"name": "ss", "type": "ss", "server": "127.0.0.1", "port": r.SSPort, "cipher": r.SSCipher, "password": r.SSPassword, "udp": true},
		"vless": {"name": "vless", "type": "vless", "server": "127.0.0.1", "port": r.VLESSPort, "uuid": r.VLESSUUID, "udp": true, "tls": false},
	}
	for name, mapping := range clients {
		t.Run(name, func(t *testing.T) {
			before := stats(t, r)
			got := fetch(t, mapping, addr)
			if got != len(body) {
				t.Fatalf("fetched %d of %d bytes", got, len(body))
			}
			after := stats(t, r)
			if after["download"]-before["download"] < int64(len(body)) {
				t.Fatalf("node counted %d down for a %d byte body", after["download"]-before["download"], len(body))
			}
		})
	}

	// By name: only the node knows TargetName (the emulator reaches the sink this way).
	byName, err := adapter.ParseProxy(clients["ss"])
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	named, err := byName.DialContext(ctx, &C.Metadata{NetWork: C.TCP, Host: TargetName, DstPort: addr.Port()})
	if err != nil {
		t.Fatalf("dial %s through the node: %v", TargetName, err)
	}
	fmt.Fprintf(named, "GET / HTTP/1.1\r\nHost: t\r\nConnection: close\r\n\r\n")
	if response, err := http.ReadResponse(bufio.NewReader(named), nil); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("by name: %v", err)
	}
	named.Close()

	wrong := map[string]any{"name": "bad", "type": "ss", "server": "127.0.0.1", "port": r.SSPort, "cipher": r.SSCipher, "password": "not-the-password", "udp": true}
	if got := fetch(t, wrong, addr); got != 0 {
		t.Fatalf("a wrong password fetched %d bytes", got)
	}

	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := echo.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = echo.WriteTo(buf[:n], from)
		}
	}()
	dst := netip.MustParseAddrPort(echo.LocalAddr().String())
	for name, mapping := range clients { // VLESS UDP is XUDP in the pinned client
		if err := udpEcho(mapping, dst); err != nil {
			t.Errorf("%s UDP relay: %v", name, err)
		}
	}
}

func udpEcho(mapping map[string]any, dst netip.AddrPort) error {
	proxy, err := adapter.ParseProxy(mapping)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pc, err := proxy.ListenPacketContext(ctx, &C.Metadata{NetWork: C.UDP, DstIP: dst.Addr(), DstPort: dst.Port()})
	if err != nil {
		return err
	}
	defer pc.Close()
	if _, err := pc.WriteTo([]byte("ping"), net.UDPAddrFromAddrPort(dst)); err != nil {
		return err
	}
	_ = pc.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	n, _, err := pc.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "ping" {
		return fmt.Errorf("%q %v", buf[:n], err)
	}
	return nil
}

func fetch(t *testing.T, mapping map[string]any, dst netip.AddrPort) int {
	t.Helper()
	proxy, err := adapter.ParseProxy(mapping)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := proxy.DialContext(ctx, &C.Metadata{NetWork: C.TCP, DstIP: dst.Addr(), DstPort: dst.Port()})
	if err != nil {
		return 0
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: t\r\nConnection: close\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0
	}
	defer response.Body.Close()
	n, _ := io.Copy(io.Discard, response.Body)
	return int(n)
}

func stats(t *testing.T, r ready) map[string]int64 {
	t.Helper()
	time.Sleep(300 * time.Millisecond) // trackers push their last chunk asynchronously
	response, err := http.Get("http://" + r.StatusAddr + "/stats?t=" + r.StatusToken)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var out map[string]int64
	if err := json.NewDecoder(response.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if denied, _ := http.Get("http://" + r.StatusAddr + "/stats?t=wrong"); denied == nil || denied.StatusCode != http.StatusForbidden {
		t.Fatal("the counter must refuse a wrong token")
	}
	return out
}
