package forwarder

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

func muxHeader(uuid string) []byte {
	key, _ := parseUUID(uuid)
	return append(append([]byte{0}, key[:]...), 0, 3)
}
func muxFrame(id uint16, status, options byte, target string, body []byte, global bool) []byte {
	h := binary.BigEndian.AppendUint16(nil, id)
	h = append(h, status, options)
	if target != "" {
		host, port, _ := net.SplitHostPort(target)
		p, _ := strconv.Atoi(port)
		h = append(h, 2)
		h = binary.BigEndian.AppendUint16(h, uint16(p))
		ip := net.ParseIP(host)
		if ip == nil {
			h = append(h, 2, byte(len(host)))
			h = append(h, host...)
		} else if v4 := ip.To4(); v4 != nil {
			h = append(append(h, 1), v4...)
		} else {
			h = append(append(h, 3), ip.To16()...)
		}
		if global {
			h = append(h, 1, 2, 3, 4, 5, 6, 7, 8)
		}
	}
	out := binary.BigEndian.AppendUint16(nil, uint16(len(h)))
	out = append(out, h...)
	if options&1 != 0 {
		out = binary.BigEndian.AppendUint16(out, uint16(len(body)))
		out = append(out, body...)
	}
	return out
}
func dialMux(t *testing.T, v *VLESS) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", v.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err = c.Write(muxHeader(uuidA)); err != nil {
		t.Fatal(err)
	}
	expectHeader(t, c)
	return c
}
func muxEcho(t *testing.T, c net.Conn, id uint16, status byte, target string, body []byte) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write(muxFrame(id, status, 1, target, body, false)); err != nil {
		t.Fatal(err)
	}
	f, err := readXUDPFrame(c, make([]byte, 65535))
	if err != nil || f.id != id || f.status != xudpKeep || !bytes.Equal(f.payload, body) {
		t.Fatalf("mux echo: %+v err=%v", f, err)
	}
}

func TestXUDPMultipleSessionsEndKeepAndZeroDatagram(t *testing.T) {
	v := newServer(t, nil)
	install(t, v, "g", uuidA, 1<<20, time.Minute)
	dst := echoUDP(t).String()
	c := dialMux(t, v)
	muxEcho(t, c, 7, xudpNew, dst, []byte("one"))
	muxEcho(t, c, 9, xudpNew, dst, []byte("two"))
	// Keep without address falls back to the session's New target.
	muxEcho(t, c, 7, xudpKeep, "", []byte("keep"))
	muxEcho(t, c, 9, xudpKeep, "", nil)
	_, _ = c.Write(muxFrame(7, xudpEnd, 0, "", nil, false))
	_, _ = c.Write(muxFrame(0, xudpAlive, 0, "", nil, false))
	muxEcho(t, c, 9, xudpKeep, "", []byte("alive"))
	muxEcho(t, c, 7, xudpNew, dst, []byte("reuse"))
	final, _ := v.Remove("g")
	if final != (Counters{20, 20}) {
		t.Fatalf("control/header bytes billed: %+v", final)
	}
	if !closedSoon(c, time.Second) {
		t.Fatal("carrier survived remove")
	}
}

func TestXUDPDomainAndIPv6(t *testing.T) {
	for _, network := range []string{"udp4", "udp6"} {
		t.Run(network, func(t *testing.T) {
			host := "127.0.0.1"
			if network == "udp6" {
				host = "::1"
			}
			pc, err := net.ListenPacket(network, net.JoinHostPort(host, "0"))
			if err != nil {
				t.Skipf("loopback family unavailable: %v", err)
			}
			t.Cleanup(func() { pc.Close() })
			go func() {
				b := make([]byte, 1024)
				for {
					n, from, err := pc.ReadFrom(b)
					if err != nil {
						return
					}
					_, _ = pc.WriteTo(b[:n], from)
				}
			}()
			v := newServer(t, nil)
			install(t, v, "g", uuidA, 1<<20, time.Minute)
			c := dialMux(t, v)
			_, port, _ := net.SplitHostPort(pc.LocalAddr().String())
			target := pc.LocalAddr().String()
			if network == "udp4" {
				target = net.JoinHostPort("localhost", port)
				// localhost may prefer IPv6 for UDP even when the v4 listener
				// exists. Serve both families on the same port, without public DNS.
				if extra, err := net.ListenPacket("udp6", net.JoinHostPort("::1", port)); err == nil {
					t.Cleanup(func() { extra.Close() })
					go func() {
						b := make([]byte, 1024)
						for {
							n, from, err := extra.ReadFrom(b)
							if err != nil {
								return
							}
							_, _ = extra.WriteTo(b[:n], from)
						}
					}()
				}
			}
			muxEcho(t, c, 0, xudpNew, target, []byte("data"))
		})
	}
}

func TestXUDPPrivateNewAndKeepTargetsFailClosed(t *testing.T) {
	for _, target := range []string{"127.0.0.1:9", "localhost:9", "[::1]:9", "[::ffff:127.0.0.1]:9", "10.0.0.1:9", "169.254.169.254:80"} {
		for _, status := range []byte{xudpNew, xudpKeep} {
			t.Run(target+strconv.Itoa(int(status)), func(t *testing.T) {
				v := newServer(t, func(o *VLESSOptions) { o.AllowPrivateTargets = false })
				install(t, v, "g", uuidA, 1<<20, time.Minute)
				c := dialMux(t, v)
				if status == xudpKeep {
					_, _ = c.Write(muxFrame(0, xudpNew, 0, "192.0.2.1:9", nil, false))
				} // no packet/dial to public network
				_, _ = c.Write(muxFrame(0, status, 1, target, []byte("blocked"), false))
				if !closedSoon(c, 2*time.Second) {
					t.Fatal("private destination was not rejected")
				}
				if final, _ := v.Remove("g"); final != (Counters{}) {
					t.Fatalf("private packet billed: %+v", final)
				}
			})
		}
	}
}

func TestXUDPIdleAndPartialFrameRemove(t *testing.T) {
	for _, partial := range [][]byte{nil, {0}, {0, 12, 0, 0, 1, 1, 2}} {
		t.Run(strconv.Itoa(len(partial)), func(t *testing.T) {
			v := newServer(t, nil)
			install(t, v, "g", uuidA, 100, time.Minute)
			c := dialMux(t, v)
			_, _ = c.Write(partial)
			done := make(chan struct{})
			go func() { v.Remove("g"); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("Remove waited on an incomplete mux frame")
			}
			if !closedSoon(c, time.Second) {
				t.Fatal("idle carrier survived Remove")
			}
		})
	}
}

func TestXUDPRejectsMalformedFramesAndSessionMisuse(t *testing.T) {
	dst := echoUDP(t).String()
	cases := map[string][]byte{
		"too-short": {0, 3}, "oversize-header": {0xff, 0xff}, "unknown-status": muxFrame(0, 99, 0, "", nil, false),
		"invalid-flags": muxFrame(0, xudpNew, 4, dst, nil, false), "new-no-address": muxFrame(0, xudpNew, 0, "", nil, false),
		"unknown-keep": muxFrame(0, xudpKeep, 1, dst, []byte("bad"), false), "unknown-end": muxFrame(0, xudpEnd, 0, "", nil, false),
		"keep-global-id": muxFrame(0, xudpKeep, 1, dst, []byte("bad"), true), "alive-with-data": muxFrame(0, xudpAlive, 1, "", nil, false),
		"end-with-data": muxFrame(0, xudpEnd, 1, "", nil, false), "zero-port": muxFrame(0, xudpNew, 1, "127.0.0.1:0", nil, false),
	}
	tcp := muxFrame(0, xudpNew, 1, dst, []byte("bad"), false)
	tcp[6] = 1
	cases["tcp-mux"] = tcp
	badAddr := muxFrame(0, xudpNew, 1, dst, []byte("bad"), false)
	badAddr[9] = 99
	cases["bad-address"] = badAddr
	cases["duplicate-new"] = append(muxFrame(0, xudpNew, 0, dst, nil, false), muxFrame(0, xudpNew, 0, dst, nil, false)...)
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			v := newServer(t, nil)
			install(t, v, "g", uuidA, 1<<20, time.Minute)
			c := dialMux(t, v)
			_, _ = c.Write(wire)
			if !closedSoon(c, time.Second) {
				t.Fatal("invalid frame did not close carrier")
			}
			if final, _ := v.Remove("g"); final != (Counters{}) {
				t.Fatalf("invalid traffic billed: %+v", final)
			}
		})
	}
}

func TestXUDPBoundsSessionsAndTargets(t *testing.T) {
	t.Run("sessions", func(t *testing.T) {
		v := newServer(t, nil)
		install(t, v, "g", uuidA, 1<<20, time.Minute)
		c := dialMux(t, v)
		for i := 0; i <= maxXUDPSessions; i++ {
			_, _ = c.Write(muxFrame(uint16(i), xudpNew, 0, "127.0.0.1:9", nil, false))
		}
		if !closedSoon(c, time.Second) {
			t.Fatal("session bound not enforced")
		}
	})
	t.Run("targets", func(t *testing.T) {
		v := newServer(t, nil)
		install(t, v, "g", uuidA, 1<<20, time.Minute)
		c := dialMux(t, v)
		for i := 0; i < maxXUDPTargets; i++ {
			status := xudpKeep
			if i == 0 {
				status = xudpNew
			}
			muxEcho(t, c, 0, status, echoUDP(t).String(), []byte("x"))
		}
		_, _ = c.Write(muxFrame(0, xudpKeep, 1, echoUDP(t).String(), []byte("denied"), false))
		if !closedSoon(c, time.Second) {
			t.Fatal("target bound not enforced")
		}
		if final, _ := v.Remove("g"); final != (Counters{64, 64}) {
			t.Fatalf("target limit counters: %+v", final)
		}
	})
}

type shortDatagramWriter struct {
	n   int
	err error
}

func (w shortDatagramWriter) Write(p []byte) (int, error) { return min(w.n, len(p)), w.err }
func TestDatagramAccountingRefundsShortWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		err  error
		want int64
	}{{"header-only", 3, io.ErrClosedPipe, 0}, {"partial-payload", 8, io.ErrClosedPipe, 3}, {"short-nil", 8, nil, 3}, {"full-error", 15, io.ErrClosedPipe, 10}, {"full", 15, nil, 10}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEntry(Grant{Deadline: time.Now().Add(time.Minute), EpochBudget: 100})
			err := writeDatagram(e, shortDatagramWriter{tc.n, tc.err}, make([]byte, 15), 5, &e.down)
			if e.down.Load() != tc.want || e.spent.Load() != tc.want {
				t.Fatalf("counter=%d spent=%d", e.down.Load(), e.spent.Load())
			}
			if tc.name == "short-nil" && !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("missing short-write error: %v", err)
			}
		})
	}
}
func TestDatagramBudgetReservationIsAtomicAndAllOrNothing(t *testing.T) {
	e := newEntry(Grant{Deadline: time.Now().Add(time.Minute), EpochBudget: 1000})
	if e.admitDatagram(1001) || e.spent.Load() != 0 {
		t.Fatal("oversize datagram consumed budget")
	}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = writeDatagram(e, io.Discard, make([]byte, 30), 0, &e.up) }()
	}
	wg.Wait()
	if e.spent.Load() != 990 || e.up.Load() != 990 {
		t.Fatalf("spent=%d up=%d", e.spent.Load(), e.up.Load())
	}
	if err := writeDatagram(e, io.Discard, make([]byte, 10), 0, &e.up); err != nil {
		t.Fatal(err)
	}
	if e.spent.Load() != 1000 {
		t.Fatal("residual budget lost")
	}
}

func FuzzReadXUDPFrame(f *testing.F) {
	for _, seed := range [][]byte{muxFrame(0, xudpNew, 1, "127.0.0.1:53", []byte("dns"), true), muxFrame(3, xudpKeep, 1, "[::1]:53", nil, false), muxFrame(0, xudpNew, 0, "localhost:53", nil, false), muxFrame(1, xudpEnd, 0, "", nil, false), {0xff, 0xff}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, wire []byte) { _, _ = readXUDPFrame(bytes.NewReader(wire), make([]byte, 65535)) })
}
func TestMuxRequestHasNoOuterAddress(t *testing.T) {
	first := muxFrame(0, xudpNew, 1, "127.0.0.1:53", []byte("dns"), true)
	br := bufio.NewReader(bytes.NewReader(append(muxHeader(uuidA), first...)))
	key, req, err := readRequest(br)
	want, _ := parseUUID(uuidA)
	if err != nil || key != want || req.command != 3 || req.host != "" {
		t.Fatalf("request: %+v %v", req, err)
	}
	rest, _ := io.ReadAll(br)
	if !bytes.Equal(rest, first) {
		t.Fatal("mux frame consumed as an outer address")
	}
}
