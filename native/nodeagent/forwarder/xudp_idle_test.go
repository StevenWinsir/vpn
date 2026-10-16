package forwarder

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Keep a socket open but never reply: this exercises read-idle timeout,
// rather than an ICMP connection error from a closed UDP port.
func silentUDP(t *testing.T) net.PacketConn {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	return pc
}

func muxCarrier(t *testing.T, v *VLESS) *xudpCarrier {
	t.Helper()
	key, _ := parseUUID(uuidA)
	e := v.lookup(key)
	e.mu.Lock()
	defer e.mu.Unlock()
	for c := range e.conns {
		if x, ok := c.(*xudpCarrier); ok {
			return x
		}
	}
	t.Fatal("authenticated carrier was not tracked")
	return nil
}

func peerCount(x *xudpCarrier) int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return len(x.peers)
}

// Force only target read deadlines, not the carrier's deadline. Repeating this
// during the bounded wait also covers a reader not yet scheduled after its dial.
func retireQuietTargets(t *testing.T, x *xudpCarrier, c net.Conn, active string, want *Counters) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for peerCount(x) != 1 && time.Now().Before(until) {
		x.mu.Lock()
		for pc := range x.peers {
			if pc.RemoteAddr().String() != active {
				_ = pc.SetReadDeadline(time.Now().Add(-time.Second))
			}
		}
		x.mu.Unlock()
		time.Sleep(time.Millisecond)
		muxEcho(t, c, 1, xudpKeep, active, []byte("x"))
		want.Upload++
		want.Download++
	}
	if peerCount(x) != 1 {
		t.Fatal("retired targets still occupy the carrier limit")
	}
}

func TestXUDPIdleTargetsReleaseCapacityAndEndCanReuseSession(t *testing.T) {
	v := newServer(t, func(o *VLESSOptions) { o.IdleTimeout = time.Hour })
	install(t, v, "g", uuidA, 1<<20, time.Minute)
	active := echoUDP(t).String()
	c := dialMux(t, v)
	muxEcho(t, c, 1, xudpNew, active, []byte("x"))
	x := muxCarrier(t, v)
	want := Counters{Upload: 1, Download: 1}
	// More than 64 distinct destinations over the lifetime of one session,
	// never more than 64 live sockets. Merely ignoring timeout would fail this.
	for range 2 {
		for range maxXUDPTargets - 1 {
			quiet := silentUDP(t)
			if _, err := c.Write(muxFrame(1, xudpKeep, xudpData, quiet.LocalAddr().String(), []byte("q"), false)); err != nil {
				t.Fatal(err)
			}
			want.Upload++
		}
		muxEcho(t, c, 1, xudpKeep, active, []byte("x")) // parser barrier
		want.Upload++
		want.Download++
		if peerCount(x) != maxXUDPTargets {
			t.Fatalf("live target count: %d", peerCount(x))
		}
		retireQuietTargets(t, x, c, active, &want)
	}
	if _, err := c.Write(muxFrame(1, xudpEnd, 0, "", nil, false)); err != nil {
		t.Fatal(err)
	}
	muxEcho(t, c, 1, xudpNew, active, []byte("reuse"))
	want.Upload += 5
	want.Download += 5
	final, ok := v.Remove("g")
	if !ok || final != want || peerCount(x) != 0 {
		t.Fatalf("retirement/End/reuse final: got %+v, want %+v; peers=%d", final, want, peerCount(x))
	}
}

// A successful UDP write is never retried. Only retirement observed before
// admission returns errXUDPPeerStopped; serialize that decision with close.
type peerWriteConn struct {
	net.Conn
	writes atomic.Int64
}

func (c *peerWriteConn) Write(b []byte) (int, error)      { c.writes.Add(1); return len(b), nil }
func (c *peerWriteConn) Close() error                     { return nil }
func (c *peerWriteConn) SetWriteDeadline(time.Time) error { return nil }

func TestXUDPPeerRetirementSerializesWithAdmission(t *testing.T) {
	for range 100 {
		c := &peerWriteConn{}
		p := &xudpPeer{conn: c}
		e := newEntry(Grant{Deadline: time.Now().Add(time.Minute), EpochBudget: 100})
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var err error
		go func() { defer wg.Done(); <-start; err = p.write(e, []byte("data"), time.Second) }()
		go func() { defer wg.Done(); <-start; p.close() }()
		close(start)
		wg.Wait()
		w := c.writes.Load()
		if !(w == 0 && err == errXUDPPeerStopped || w == 1 && err == nil) || e.up.Load() != w*4 || e.spent.Load() != w*4 {
			t.Fatalf("retirement admitted/retried a packet: writes=%d err=%v counters=%+v", w, err, e.counters())
		}
		if err := p.write(e, []byte("late"), time.Second); err != errXUDPPeerStopped || c.writes.Load() != w || e.up.Load() != w*4 {
			t.Fatal("stopped peer wrote or billed a late packet")
		}
	}
}

func TestXUDPCarrierIdleTimeoutStillCloses(t *testing.T) {
	v := newServer(t, func(o *VLESSOptions) { o.IdleTimeout = 100 * time.Millisecond })
	install(t, v, "g", uuidA, 100, time.Minute)
	c := dialMux(t, v)
	muxEcho(t, c, 1, xudpNew, echoUDP(t).String(), []byte("idle"))
	x := muxCarrier(t, v)
	if !closedSoon(c, time.Second) {
		t.Fatal("target timeout isolation disabled the carrier idle timeout")
	}
	final, _ := v.Remove("g")
	if final != (Counters{Upload: 4, Download: 4}) || peerCount(x) != 0 {
		t.Fatalf("idle carrier cleanup: counters=%+v peers=%d", final, peerCount(x))
	}
}

func TestXUDPIdleTargetDoesNotCloseActiveCarrier(t *testing.T) {
	for _, sameSession := range []bool{true, false} {
		name := "different-sessions"
		if sameSession {
			name = "same-session"
		}
		t.Run(name, func(t *testing.T) {
			const idle = 300 * time.Millisecond
			v := newServer(t, func(o *VLESSOptions) { o.IdleTimeout = idle })
			install(t, v, "g", uuidA, 1<<20, time.Minute)
			quiet, active := silentUDP(t), echoUDP(t).String()
			c := dialMux(t, v)
			x := muxCarrier(t, v)
			if _, err := c.Write(muxFrame(1, xudpNew, xudpData, quiet.LocalAddr().String(), []byte("silent"), false)); err != nil {
				t.Fatal(err)
			}
			// Observe the upload at the real target before counting idle time.
			_ = quiet.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, _, err := quiet.ReadFrom(make([]byte, 32)); err != nil {
				t.Fatal(err)
			}
			id, status := uint16(1), xudpKeep
			if !sameSession {
				id, status = 2, xudpNew
			}
			muxEcho(t, c, id, status, active, []byte("live"))
			want := Counters{Upload: 10, Download: 4}
			// Keep using the SAME carrier while the silent target expires. No
			// reconnect can hide the original bug. Require socket reclamation too.
			until := time.Now().Add(3 * time.Second)
			for peerCount(x) != 1 && time.Now().Before(until) {
				time.Sleep(10 * time.Millisecond)
				muxEcho(t, c, id, xudpKeep, active, []byte("live"))
				want.Upload += 4
				want.Download += 4
			}
			if peerCount(x) != 1 {
				t.Fatal("idle target socket was not reclaimed")
			}
			// Keep without an address must redial the original destination and
			// apply normal accounting, without a New or a replacement carrier.
			if _, err := c.Write(muxFrame(1, xudpKeep, xudpData, "", []byte("again"), false)); err != nil {
				t.Fatal(err)
			}
			_ = quiet.SetReadDeadline(time.Now().Add(2 * time.Second))
			b := make([]byte, 32)
			n, from, err := quiet.ReadFrom(b)
			if err != nil || string(b[:n]) != "again" {
				t.Fatalf("retired target was not reopened: n=%d err=%v", n, err)
			}
			if _, err = quiet.WriteTo(b[:n], from); err != nil {
				t.Fatal(err)
			}
			f, err := readXUDPFrame(c, make([]byte, 65535))
			if err != nil || f.id != 1 || string(f.payload) != "again" {
				t.Fatalf("reopened target reply: %+v %v", f, err)
			}
			want.Upload += 5
			want.Download += 5
			final, ok := v.Remove("g")
			if !ok || final != want {
				t.Fatalf("final payload counters: got %+v, want %+v", final, want)
			}
			if !closedSoon(c, time.Second) || peerCount(x) != 0 {
				t.Fatal("Remove left a carrier or UDP socket alive")
			}
		})
	}
}
