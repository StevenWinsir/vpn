//go:build !windows

package engine

import (
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"syscall"
	"time"
)

// ipv6Relay applies refuseIPv6 to a utun (handoff_app_ios.md 8.4). The pinned stack reads
// its descriptor directly, so a filter in front of it needs a descriptor of its own: a
// datagram socket pair whose inner end the stack gets as tun.file-descriptor. Each utun read
// is one packet behind a 4-byte address-family header, and each socket-pair datagram carries
// exactly the same bytes, so the stack cannot tell the difference (recvmsg_x/sendmsg_x, which
// are utun-only, are switched off for it). IPv4 passes through untouched in both directions;
// IPv6 from the system is answered by the relay and never reaches the stack. The relay owns
// the utun descriptor it was given and its own end of the pair; the stack owns the inner end.
// It runs only for a node without IPv6, so the common path keeps the stack on the utun itself.
type ipv6Relay struct {
	utun, local *os.File
	done        sync.WaitGroup
	// refusals bounds the error rate (RFC 4443 2.4(f)): at most refusalBurst per second.
	mu      sync.Mutex
	window  time.Time
	answers int
	// Refused counts the IPv6 packets answered or dropped (diagnostics and tests).
	refused uint64
}

const (
	// utunHeader is the 4-byte protocol-family prefix of every utun packet.
	utunHeader = 4
	// darwinAFInet6 is AF_INET6 on Darwin, the value the utun header carries for IPv6.
	darwinAFInet6 = 30
	// relayBuffer is each pair socket's buffer: a burst of full-size packets, and far above
	// the platform's 2 KiB default datagram limit for local sockets.
	relayBuffer  = 1 << 20
	refusalBurst = 200
)

// startIPv6Relay takes ownership of utun and returns the descriptor the stack must read.
func startIPv6Relay(utun, mtu int) (*ipv6Relay, int, error) {
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return nil, -1, err
	}
	for _, fd := range pair {
		syscall.CloseOnExec(fd)
		for _, opt := range []int{syscall.SO_SNDBUF, syscall.SO_RCVBUF} {
			if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, opt, relayBuffer); err != nil {
				syscall.Close(pair[0])
				syscall.Close(pair[1])
				return nil, -1, err
			}
		}
	}
	for _, fd := range []int{utun, pair[0]} {
		if err := syscall.SetNonblock(fd, true); err != nil {
			syscall.Close(pair[0])
			syscall.Close(pair[1])
			return nil, -1, err
		}
	}
	r := &ipv6Relay{utun: os.NewFile(uintptr(utun), "utun"), local: os.NewFile(uintptr(pair[0]), "tun-relay")}
	size := mtu + utunHeader + 64
	r.done.Add(2)
	go r.inbound(size)
	go r.outbound(size)
	return r, pair[1], nil
}

// inbound: system -> stack. IPv6 is answered here and never forwarded.
func (r *ipv6Relay) inbound(size int) {
	defer r.done.Done()
	buf := make([]byte, size)
	for {
		n, err := r.utun.Read(buf)
		if err != nil {
			if closed(err) {
				return
			}
			time.Sleep(10 * time.Millisecond) // e.g. the interface is going away: no busy loop
			continue
		}
		if n <= utunHeader {
			continue
		}
		packet := buf[utunHeader:n]
		if packet[0]>>4 == 6 {
			r.refuse(packet)
			continue
		}
		_, _ = r.local.Write(buf[:n])
	}
}

// outbound: stack -> system, unchanged.
func (r *ipv6Relay) outbound(size int) {
	defer r.done.Done()
	buf := make([]byte, size)
	for {
		n, err := r.local.Read(buf)
		if err != nil {
			if closed(err) {
				return
			}
			time.Sleep(10 * time.Millisecond) // e.g. the interface is going away: no busy loop
			continue
		}
		_, _ = r.utun.Write(buf[:n])
	}
}

func (r *ipv6Relay) refuse(packet []byte) {
	r.mu.Lock()
	r.refused++
	now := time.Now()
	if now.Sub(r.window) >= time.Second {
		r.window, r.answers = now, 0
	}
	allowed := r.answers < refusalBurst
	if allowed {
		r.answers++
	}
	r.mu.Unlock()
	if !allowed {
		return
	}
	reply := refuseIPv6(packet)
	if reply == nil {
		return
	}
	framed := make([]byte, utunHeader+len(reply))
	binary.BigEndian.PutUint32(framed, darwinAFInet6)
	copy(framed[utunHeader:], reply)
	_, _ = r.utun.Write(framed)
}

// Refused reports how many IPv6 packets the relay has turned away.
func (r *ipv6Relay) Refused() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refused
}

// stop closes the relay's descriptors (the utun it owned and its end of the pair) and waits
// for both loops to leave. Idempotent.
func (r *ipv6Relay) stop() {
	if r == nil {
		return
	}
	_ = r.utun.Close()
	_ = r.local.Close()
	r.done.Wait()
}

func closed(err error) bool {
	return errors.Is(err, os.ErrClosed)
}
