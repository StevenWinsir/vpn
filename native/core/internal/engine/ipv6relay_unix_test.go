//go:build !windows

package engine

import (
	"encoding/binary"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// fakeUtun is a datagram socket pair standing in for a utun: the test plays the system on
// one end and the relay owns the other, exactly as it owns the provider's dup().
type fakeUtun struct{ system, relayEnd int }

func newFakeUtun(t *testing.T) fakeUtun {
	t.Helper()
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range pair {
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, relayBuffer)
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, relayBuffer)
	}
	t.Cleanup(func() { syscall.Close(pair[0]) })
	return fakeUtun{system: pair[0], relayEnd: pair[1]}
}

func framed(family uint32, packet []byte) []byte {
	out := make([]byte, utunHeader+len(packet))
	binary.BigEndian.PutUint32(out, family)
	copy(out[utunHeader:], packet)
	return out
}

// receive reads one datagram from fd within a deadline, or returns nil.
func receive(t *testing.T, fd int, wait time.Duration) []byte {
	t.Helper()
	tv := syscall.NsecToTimeval(wait.Nanoseconds())
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16<<10)
	n, _, err := syscall.Recvfrom(fd, buf, 0)
	if err != nil {
		return nil
	}
	return buf[:n]
}

// T04 at packet level: IPv4 crosses the relay unchanged both ways, IPv6 never reaches the
// stack and is answered on the utun, full-size packets fit, and stop leaves no goroutine.
func TestIPv6RelayRefusesIPv6AndPassesIPv4(t *testing.T) {
	before := runtime.NumGoroutine()
	utun := newFakeUtun(t)
	relay, stackFD, err := startIPv6Relay(utun.relayEnd, 9000)
	if err != nil {
		t.Fatal(err)
	}
	stack := os.NewFile(uintptr(stackFD), "stack") // what the listener would own
	defer stack.Close()

	v4 := framed(2, append([]byte{0x45}, make([]byte, 8999)...)) // a full 9000-byte IPv4 packet
	if _, err := syscall.Write(utun.system, v4); err != nil {
		t.Fatal(err)
	}
	if got := receive(t, stackFD, 2*time.Second); string(got) != string(v4) {
		t.Fatalf("IPv4 to the stack: %d bytes, want %d", len(got), len(v4))
	}

	syn := framed(darwinAFInet6, ipv6Packet(protoTCP, appAddr, siteAddr, tcpSegment(tcpFlagSYN, 9, 0, 0)))
	if _, err := syscall.Write(utun.system, syn); err != nil {
		t.Fatal(err)
	}
	reply := receive(t, utun.system, 2*time.Second)
	if len(reply) != utunHeader+ipv6HeaderLen+20 || binary.BigEndian.Uint32(reply) != darwinAFInet6 ||
		reply[utunHeader+ipv6HeaderLen+13] != tcpFlagRST|tcpFlagACK {
		t.Fatalf("SYN answer %x", reply)
	}
	if leaked := receive(t, stackFD, 200*time.Millisecond); leaked != nil {
		t.Fatalf("IPv6 reached the stack: %x", leaked)
	}

	out := framed(2, append([]byte{0x45}, make([]byte, 99)...))
	if _, err := stack.Write(out); err != nil {
		t.Fatal(err)
	}
	if got := receive(t, utun.system, 2*time.Second); string(got) != string(out) {
		t.Fatal("stack output did not reach the utun unchanged")
	}
	if relay.Refused() != 1 {
		t.Fatalf("refused %d", relay.Refused())
	}

	relay.stop()
	relay.stop() // idempotent
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines %d -> %d after stop", before, after)
	}
	// The relay closed the utun copy it owned.
	var st syscall.Stat_t
	if syscall.Fstat(utun.relayEnd, &st) == nil {
		t.Fatal("the relay left its utun descriptor open")
	}
}

// The error rate is bounded: a flood of IPv6 is counted but answered at most refusalBurst
// times per second.
func TestIPv6RelayRateLimitsAnswers(t *testing.T) {
	utun := newFakeUtun(t)
	relay, stackFD, err := startIPv6Relay(utun.relayEnd, 1500)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(stackFD)
	defer relay.stop()
	syn := framed(darwinAFInet6, ipv6Packet(protoTCP, appAddr, siteAddr, tcpSegment(tcpFlagSYN, 9, 0, 0)))
	const flood = refusalBurst + 50
	for range flood {
		if _, err := syscall.Write(utun.system, syn); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for relay.Refused() < flood && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	answers := 0
	for receive(t, utun.system, 100*time.Millisecond) != nil {
		answers++
	}
	if relay.Refused() != flood || answers == 0 || answers > refusalBurst {
		t.Fatalf("refused %d, answered %d (limit %d)", relay.Refused(), answers, refusalBurst)
	}
}
