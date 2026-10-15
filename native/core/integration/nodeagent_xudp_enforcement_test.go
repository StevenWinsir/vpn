package integration_test

import (
	"bytes"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asterlink/nodeagent/forwarder"
)

func requireRejected(t *testing.T, pc net.PacketConn, dst *net.UDPAddr) {
	t.Helper()
	err := exchange(pc, dst, []byte("denied"))
	if err == nil {
		t.Fatal("unauthorized UDP traffic was served")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("expected a closed carrier, not a timeout: %v", err)
	}
}

func TestMihomoXUDPRemoveIsFinalAndGlobalIDIsNotAuthority(t *testing.T) {
	v := node(t, true)
	dst := echoUDP(t)
	grant(t, v, "a", uuidA, 1<<20, time.Minute)
	grant(t, v, "b", uuidB, 1<<20, time.Minute)
	// Identical source => identical optional global ID. It must not share the
	// grant, counter, remote socket or revocation scope.
	a1, a2, b := mihomoUDP(t, v, uuidA, dst, true), mihomoUDP(t, v, uuidA, dst, true), mihomoUDP(t, v, uuidB, dst, true)
	for _, pc := range []net.PacketConn{a1, a2, b} {
		if err := exchange(pc, dst, []byte("alive")); err != nil {
			t.Fatal(err)
		}
	}
	began := time.Now()
	final, ok := v.Remove("a")
	if !ok || final != (forwarder.Counters{Upload: 10, Download: 10}) {
		t.Fatalf("final counters %+v", final)
	}
	if time.Since(began) > time.Second {
		t.Fatal("Remove did not close established flows promptly")
	}
	requireRejected(t, a1, dst)
	requireRejected(t, a2, dst)
	requireRejected(t, mihomoUDP(t, v, uuidA, dst, true), dst)
	if err := exchange(b, dst, []byte("other grant still works")); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Usage()["a"]; ok {
		t.Fatal("removed grant reappeared")
	}
}

func TestMihomoXUDPDeadlineClosesIdleCarrierWithoutAPI(t *testing.T) {
	v := node(t, true)
	dst := echoUDP(t)
	grant(t, v, "a", uuidA, 1<<20, 300*time.Millisecond)
	pc := mihomoUDP(t, v, uuidA, dst, true)
	if err := exchange(pc, dst, []byte("alive")); err != nil {
		t.Fatal(err)
	}
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := pc.ReadFrom(make([]byte, 65535))
	var ne net.Error
	if err == nil || errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("idle flow survived deadline: %v", err)
	}
	requireRejected(t, mihomoUDP(t, v, uuidA, dst, false), dst)
	if ids := v.Expired(time.Now()); len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("expiry missing: %v", ids)
	}
	if !v.Update("a", time.Now().Add(time.Minute), 1<<20) {
		t.Fatal("lease refresh refused")
	}
	if err := exchange(mihomoUDP(t, v, uuidA, dst, false), dst, []byte("renewed")); err != nil {
		t.Fatal(err)
	}
}

func TestMihomoXUDPUnknownAndPrivateTargetsAreRejected(t *testing.T) {
	dst := echoUDP(t)
	for _, tc := range []struct {
		name    string
		install bool
		private bool
	}{{"unknown", false, true}, {"private", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			v := node(t, tc.private)
			if tc.install {
				grant(t, v, "a", uuidA, 1<<20, time.Minute)
			}
			requireRejected(t, mihomoUDP(t, v, uuidA, dst, false), dst)
			if got := v.Usage()["a"]; got != (forwarder.Counters{}) {
				t.Fatalf("denied traffic billed: %+v", got)
			}
		})
	}
}

func TestMihomoXUDPWholeDatagramBudgetAndTopUp(t *testing.T) {
	v := node(t, true)
	dst := echoUDP(t)
	grant(t, v, "a", uuidA, 10, time.Minute)
	pc := mihomoUDP(t, v, uuidA, dst, false)
	if err := exchange(pc, dst, bytes.Repeat([]byte("x"), 11)); err == nil {
		t.Fatal("over-budget packet served")
	}
	if got := v.Usage()["a"]; got != (forwarder.Counters{}) {
		t.Fatalf("rejected packet was billed: %+v", got)
	}
	// The rejected 11-byte packet must not burn the 10-byte allowance.
	next := mihomoUDP(t, v, uuidA, dst, false)
	if err := exchange(next, dst, []byte("abc")); err != nil {
		t.Fatalf("residual budget lost: %v", err)
	}
	if !v.Update("a", time.Now().Add(time.Minute), 20) {
		t.Fatal("top-up refused")
	}
	if err := exchange(next, dst, []byte("more")); err != nil {
		t.Fatal(err)
	}
	final, _ := v.Remove("a")
	if final != (forwarder.Counters{Upload: 7, Download: 7}) {
		t.Fatalf("payload-only allowance: %+v", final)
	}
}

func TestMihomoXUDPConcurrentCarriersShareEpochCap(t *testing.T) {
	v := node(t, true)
	dst := echoUDP(t)
	const cap = 2048
	grant(t, v, "a", uuidA, cap, time.Minute)
	var conns []net.PacketConn
	for range 8 {
		conns = append(conns, mihomoUDP(t, v, uuidA, dst, true))
	}
	var wg sync.WaitGroup
	var echoed atomic.Int64
	for _, pc := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := bytes.Repeat([]byte("b"), 100)
			for range 30 {
				if exchange(pc, dst, body) != nil {
					return
				}
				echoed.Add(int64(len(body)))
			}
		}()
	}
	wg.Wait()
	final, _ := v.Remove("a")
	if final.Upload+final.Download > cap || final.Upload == 0 || final.Download < echoed.Load() {
		t.Fatalf("cap=%d final=%+v echoed=%d", cap, final, echoed.Load())
	}
}

func TestMihomoXUDPRemoveUnderLoad(t *testing.T) {
	v := node(t, true)
	dst := echoUDP(t)
	grant(t, v, "a", uuidA, 1<<30, time.Minute)
	var conns []net.PacketConn
	for range 6 {
		pc := mihomoUDP(t, v, uuidA, dst, true)
		if err := exchange(pc, dst, []byte("ready")); err != nil {
			t.Fatal(err)
		}
		conns = append(conns, pc)
	}
	var wg sync.WaitGroup
	var echoed atomic.Int64
	echoed.Store(30)
	for _, pc := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := bytes.Repeat([]byte("b"), 512)
			for {
				if exchange(pc, dst, body) != nil {
					return
				}
				echoed.Add(512)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	began := time.Now()
	final, ok := v.Remove("a")
	if !ok || time.Since(began) > time.Second {
		t.Fatal("Remove did not finish established XUDP carriers promptly")
	}
	wg.Wait()
	if final.Download < echoed.Load() {
		t.Fatalf("final download=%d but client received=%d", final.Download, echoed.Load())
	}
	requireRejected(t, mihomoUDP(t, v, uuidA, dst, true), dst)
	if len(v.Usage()) != 0 {
		t.Fatal("usage changed after final removal")
	}
}
