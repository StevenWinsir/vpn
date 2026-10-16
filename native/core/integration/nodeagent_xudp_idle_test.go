package integration_test

import (
	"bytes"
	"net"
	"testing"
	"time"

	"asterlink/nodeagent/forwarder"
)

func TestMihomoXUDPIdleTargetKeepsActiveCarrier(t *testing.T) {
	const idle = 300 * time.Millisecond
	v, err := forwarder.NewVLESS(forwarder.VLESSOptions{
		Listen: "127.0.0.1:0", AllowPrivateTargets: true,
		IdleTimeout: idle, WatchInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	grant(t, v, "a", uuidA, 1<<20, time.Minute)
	quiet, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { quiet.Close() })
	dst := quiet.LocalAddr().(*net.UDPAddr)
	active := echoUDP(t)
	pc := mihomoUDP(t, v, uuidA, dst, true)
	_ = pc.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := pc.WriteTo([]byte("silent"), dst); err != nil {
		t.Fatal(err)
	}
	_ = quiet.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := quiet.ReadFromUDP(make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	want := forwarder.Counters{Upload: 6}
	// Use the actual pinned default XUDP PacketConn throughout; creating a new
	// connection on timeout would conceal the bug. Keep another destination
	// active for multiple idle periods before returning to the silent one.
	until := time.Now().Add(3 * idle)
	for time.Now().Before(until) {
		if err := exchange(pc, active, []byte("live")); err != nil {
			t.Fatalf("silent target interrupted an active real Mihomo carrier: %v", err)
		}
		want.Upload += 4
		want.Download += 4
		time.Sleep(10 * time.Millisecond)
	}
	body := []byte("reopened")
	_ = pc.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := pc.WriteTo(body, dst); err != nil {
		t.Fatal(err)
	}
	_ = quiet.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, 65535)
	n, from, err := quiet.ReadFromUDP(b)
	if err != nil || !bytes.Equal(b[:n], body) {
		t.Fatalf("target did not reopen: %v", err)
	}
	if _, err := quiet.WriteToUDP(b[:n], from); err != nil {
		t.Fatal(err)
	}
	n, source, err := pc.ReadFrom(b)
	if err != nil || !bytes.Equal(b[:n], body) || source.String() != dst.String() {
		t.Fatalf("reopened target payload/source: %v", err)
	}
	want.Upload += int64(len(body))
	want.Download += int64(len(body))
	final, ok := v.Remove("a")
	if !ok || final != want {
		t.Fatalf("payload-only counters changed: got %+v want %+v", final, want)
	}
	requireRejected(t, pc, active)
	requireRejected(t, mihomoUDP(t, v, uuidA, active, true), active)
}
