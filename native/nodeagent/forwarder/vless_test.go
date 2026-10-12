package forwarder

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

const (
	uuidA = "11111111-2222-4333-8444-555555555555"
	uuidB = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

func echoTCP(t *testing.T) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return ln.Addr()
}

func echoUDP(t *testing.T) net.Addr {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], from)
		}
	}()
	return pc.LocalAddr()
}

func newServer(t *testing.T, mutate func(*VLESSOptions)) *VLESS {
	t.Helper()
	opts := VLESSOptions{Listen: "127.0.0.1:0", AllowPrivateTargets: true, WatchInterval: 20 * time.Millisecond, IdleTimeout: 10 * time.Second}
	if mutate != nil {
		mutate(&opts)
	}
	v, err := NewVLESS(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

func install(t *testing.T, v *VLESS, id, uuid string, budget int64, ttl time.Duration) {
	t.Helper()
	if err := v.Install(Grant{ID: id, Protocol: "vless", Credential: map[string]string{"uuid": uuid}, Deadline: time.Now().Add(ttl), EpochBudget: budget}); err != nil {
		t.Fatal(err)
	}
}

func header(uuid string, command byte, target net.Addr) []byte {
	key, _ := parseUUID(uuid)
	host, port, _ := net.SplitHostPort(target.String())
	p, _ := strconv.Atoi(port)
	out := append([]byte{0}, key[:]...)
	out = append(out, 0, command)
	out = binary.BigEndian.AppendUint16(out, uint16(p))
	ip := net.ParseIP(host).To4()
	return append(append(out, 1), ip...)
}

// dialTCP performs the VLESS handshake. A refused user yields an error on first read.
func dialTCP(t *testing.T, v *VLESS, uuid string, target net.Addr) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", v.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err = c.Write(header(uuid, 1, target)); err != nil {
		t.Fatal(err)
	}
	return c
}

func expectHeader(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, 2)
	if _, err := io.ReadFull(c, got); err != nil || got[0] != 0 || got[1] != 0 {
		t.Fatalf("response header: %v %v", got, err)
	}
}

func roundTrip(t *testing.T, c net.Conn, payload []byte) {
	t.Helper()
	if _, err := c.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	back := make([]byte, len(payload))
	if _, err := io.ReadFull(c, back); err != nil || !bytes.Equal(back, payload) {
		t.Fatalf("echo failed: %v", err)
	}
}

func closedSoon(c net.Conn, within time.Duration) bool {
	_ = c.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 1)
	for {
		if _, err := c.Read(buf); err != nil {
			ne, timeout := err.(net.Error)
			return !(timeout && ne.Timeout())
		}
	}
}

func TestTCPRelayCountsPayloadPerGrant(t *testing.T) {
	v := newServer(t, nil)
	target := echoTCP(t)
	install(t, v, "g1", uuidA, 1<<20, time.Minute)
	install(t, v, "g2", uuidB, 1<<20, time.Minute)

	c1 := dialTCP(t, v, uuidA, target)
	expectHeader(t, c1)
	roundTrip(t, c1, bytes.Repeat([]byte("a"), 1000))
	c2 := dialTCP(t, v, uuidB, target)
	expectHeader(t, c2)
	roundTrip(t, c2, bytes.Repeat([]byte("b"), 300))

	usage := v.Usage()
	if usage["g1"] != (Counters{1000, 1000}) || usage["g2"] != (Counters{300, 300}) {
		t.Fatalf("each user must be counted separately: %+v", usage)
	}
}

func TestUnknownRevokedAndMalformedUsersGetNothing(t *testing.T) {
	v := newServer(t, nil)
	target := echoTCP(t)
	c := dialTCP(t, v, uuidB, target)
	if !closedSoon(c, 2*time.Second) {
		t.Fatal("unknown uuid was served")
	}
	// Bad version byte / truncated header.
	for _, raw := range [][]byte{{1, 2, 3}, append([]byte{9}, make([]byte, 40)...)} {
		bad, _ := net.Dial("tcp", v.Addr().String())
		_, _ = bad.Write(raw)
		if !closedSoon(bad, 2*time.Second) && len(raw) > 10 {
			t.Fatal("bad version served")
		}
		bad.Close()
	}
	if len(v.Usage()) != 0 {
		t.Fatal("nothing should be counted")
	}
}

func TestRemoveKillsEstablishedFlowsAndRefusesNewOnes(t *testing.T) {
	v := newServer(t, nil)
	target := echoTCP(t)
	install(t, v, "g", uuidA, 1<<20, time.Minute)
	var flows []net.Conn
	for range 5 {
		c := dialTCP(t, v, uuidA, target)
		expectHeader(t, c)
		roundTrip(t, c, []byte("ping-ping"))
		flows = append(flows, c)
	}
	udp := echoUDP(t)
	uc, _ := net.Dial("tcp", v.Addr().String())
	t.Cleanup(func() { uc.Close() })
	_, _ = uc.Write(header(uuidA, 2, udp))
	expectHeader(t, uc)

	final, ok := v.Remove("g")
	if !ok || final.Upload != 45 || final.Download != 45 {
		t.Fatalf("final counters %+v ok=%v", final, ok)
	}
	for i, c := range flows {
		if !closedSoon(c, 2*time.Second) {
			t.Fatalf("established TCP flow %d survived revocation", i)
		}
	}
	if !closedSoon(uc, 2*time.Second) {
		t.Fatal("established UDP flow survived revocation")
	}
	fresh := dialTCP(t, v, uuidA, target)
	if !closedSoon(fresh, 2*time.Second) {
		t.Fatal("new connection with a revoked credential was served")
	}
	if _, ok := v.Remove("g"); ok {
		t.Fatal("double remove must report not installed")
	}
	if len(v.Usage()) != 0 {
		t.Fatal("removed grant still reported")
	}
}

func TestDeadlineIsEnforcedWithoutTraffic(t *testing.T) {
	v := newServer(t, nil)
	target := echoTCP(t)
	install(t, v, "g", uuidA, 1<<20, 400*time.Millisecond)
	c := dialTCP(t, v, uuidA, target)
	expectHeader(t, c)
	roundTrip(t, c, []byte("hello"))
	if !closedSoon(c, 3*time.Second) {
		t.Fatal("idle flow outlived the absolute deadline")
	}
	if ids := v.Expired(time.Now()); len(ids) != 1 || ids[0] != "g" {
		t.Fatalf("expired list %v", ids)
	}
	late := dialTCP(t, v, uuidA, target)
	if !closedSoon(late, 2*time.Second) {
		t.Fatal("served after the deadline")
	}
	// Extending the lease revives the credential.
	if !v.Update("g", time.Now().Add(time.Minute), 1<<20) {
		t.Fatal("update refused")
	}
	again := dialTCP(t, v, uuidA, target)
	expectHeader(t, again)
	roundTrip(t, again, []byte("back"))
}

func TestByteCapIsNeverExceeded(t *testing.T) {
	v := newServer(t, nil)
	target := echoTCP(t)
	const budget = 10_000
	install(t, v, "g", uuidA, budget, time.Minute)
	c := dialTCP(t, v, uuidA, target)
	expectHeader(t, c)
	go func() { _, _ = c.Write(bytes.Repeat([]byte("x"), 200_000)) }()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	received, _ := io.Copy(io.Discard, c)
	got := v.Usage()["g"]
	if got.Upload+got.Download > budget {
		t.Fatalf("forwarded %d bytes past a %d byte cap", got.Upload+got.Download, budget)
	}
	// Upload alone may consume the whole allowance (it is one pool for both directions).
	if received > budget || got.Upload+got.Download != budget {
		t.Fatalf("client received %d bytes, node counted %+v", received, got)
	}
	if ids := v.Expired(time.Now()); len(ids) != 0 {
		t.Fatalf("a spent cap is not expiry: %v", ids)
	}
	// A top-up from the control plane lets the grant carry on.
	v.Update("g", time.Now().Add(time.Minute), 100_000)
	c2 := dialTCP(t, v, uuidA, target)
	expectHeader(t, c2)
	roundTrip(t, c2, []byte("more"))
}

func TestPrivateDestinationsAreRefusedByDefault(t *testing.T) {
	v := newServer(t, func(o *VLESSOptions) { o.AllowPrivateTargets = false })
	target := echoTCP(t) // on loopback
	install(t, v, "g", uuidA, 1<<20, time.Minute)
	c := dialTCP(t, v, uuidA, target)
	if !closedSoon(c, 2*time.Second) {
		t.Fatal("a public node must not relay to loopback")
	}
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.0.1", "172.16.0.1", "169.254.169.254", "100.64.0.1", "::1", "fe80::1", "224.0.0.1", "0.0.0.0", "255.255.255.255"} {
		if !isPrivate(net.ParseIP(ip)) {
			t.Fatalf("%s must be private", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if isPrivate(net.ParseIP(ip)) {
			t.Fatalf("%s must be public", ip)
		}
	}
	if v.Usage()["g"] != (Counters{}) {
		t.Fatal("refused traffic must not be counted")
	}
}

func TestUDPRelayCounts(t *testing.T) {
	v := newServer(t, nil)
	target := echoUDP(t)
	install(t, v, "g", uuidA, 1<<20, time.Minute)
	c, _ := net.Dial("tcp", v.Addr().String())
	t.Cleanup(func() { c.Close() })
	_, _ = c.Write(header(uuidA, 2, target))
	expectHeader(t, c)
	for _, size := range []int{10, 700} {
		frame := binary.BigEndian.AppendUint16(nil, uint16(size))
		frame = append(frame, bytes.Repeat([]byte("u"), size)...)
		if _, err := c.Write(frame); err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		back := make([]byte, 2+size)
		if _, err := io.ReadFull(c, back); err != nil || binary.BigEndian.Uint16(back[:2]) != uint16(size) {
			t.Fatalf("udp frame: %v", err)
		}
	}
	if got := v.Usage()["g"]; got != (Counters{710, 710}) {
		t.Fatalf("udp counters %+v", got)
	}
}

func TestInstallRules(t *testing.T) {
	v := newServer(t, nil)
	install(t, v, "g", uuidA, 100, time.Minute)
	if err := v.Install(Grant{ID: "other", Protocol: "vless", Credential: map[string]string{"uuid": uuidA}}); err != ErrDuplicate {
		t.Fatalf("shared credential must be refused: %v", err)
	}
	if err := v.Install(Grant{ID: "x", Protocol: "trojan", Credential: map[string]string{"password": "p"}}); err != ErrUnsupported {
		t.Fatalf("unsupported protocol: %v", err)
	}
	for _, bad := range []map[string]string{{"uuid": "nope"}, {"uuid": uuidB, "extra": "1"}, {}} {
		if err := v.Install(Grant{ID: "y", Protocol: "vless", Credential: bad}); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	// Re-installing the same id only refreshes its limits.
	if err := v.Install(Grant{ID: "g", Protocol: "vless", Credential: map[string]string{"uuid": uuidA}, Deadline: time.Now().Add(time.Hour), EpochBudget: 999}); err != nil {
		t.Fatal(err)
	}
	if len(v.Usage()) != 1 {
		t.Fatal("re-install duplicated the grant")
	}
}

func TestRemoveUnderLoadIsFinalAndRaceFree(t *testing.T) {
	v := newServer(t, nil)
	target := echoTCP(t)
	install(t, v, "g", uuidA, 1<<30, time.Minute)
	var wg sync.WaitGroup
	var sentMu sync.Mutex
	var echoed int64
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.Dial("tcp", v.Addr().String())
			if err != nil {
				return
			}
			defer c.Close()
			_, _ = c.Write(header(uuidA, 1, target))
			hdr := make([]byte, 2)
			if _, err = io.ReadFull(c, hdr); err != nil {
				return
			}
			buf := make([]byte, 1024)
			for {
				if _, err := c.Write(buf); err != nil {
					return
				}
				n, err := io.ReadFull(c, buf)
				sentMu.Lock()
				echoed += int64(n)
				sentMu.Unlock()
				if err != nil {
					return
				}
			}
		}()
	}
	time.Sleep(150 * time.Millisecond)
	final, ok := v.Remove("g")
	if !ok || final.Upload == 0 {
		t.Fatalf("no traffic observed before removal: %+v", final)
	}
	wg.Wait()
	// Nothing can be counted after Remove returned: the returned totals are final.
	if _, ok = v.Remove("g"); ok {
		t.Fatal("grant reappeared")
	}
	sentMu.Lock()
	defer sentMu.Unlock()
	if echoed > final.Download {
		t.Fatalf("client received %d echoed bytes but the node counted only %d downloaded", echoed, final.Download)
	}
}
