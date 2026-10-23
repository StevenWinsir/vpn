package api

import (
	"math/rand/v2"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Binding 127.0.0.1:0 and closing the listener hands back a number from the kernel's ephemeral
// range, and the kernel may give that same number to the next bind(0) or outgoing connection on
// the host before whoever was meant to have it binds it. Main CI hit that on 2026-10-23
// ("listen tcp 127.0.0.1:42625: bind: address already in use", Android billing world). The
// kernel never hands out a port below the ephemeral range by itself, so once such a port is
// checked free only an explicit bind of that same number can take it (scripts/loopback_port.py
// is the harness side of the same rule).

const loopbackPortFloor = 20000

var (
	loopbackPortsMu     sync.Mutex
	loopbackPortsIssued = map[int]bool{}
)

// ephemeralPortStart is the first port the kernel hands out by itself (Linux says; macOS and
// Windows default to 49152).
func ephemeralPortStart() int {
	if runtime.GOOS == "linux" {
		if raw, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range"); err == nil {
			if fields := strings.Fields(string(raw)); len(fields) > 0 {
				if n, err := strconv.Atoi(fields[0]); err == nil {
					return n
				}
			}
		}
		return 32768
	}
	return 49152
}

func loopbackPortBindable(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	defer ln.Close()
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return false
	}
	return pc.Close() == nil
}

// freeAddr picks a loopback address for a listener that something else binds later (the node
// agent's forwarder, or the control plane again after a restart): free for TCP and UDP, below
// the ephemeral range, and never the same one twice in this process.
func freeAddr(t *testing.T) string {
	t.Helper()
	loopbackPortsMu.Lock()
	defer loopbackPortsMu.Unlock()
	if top := ephemeralPortStart(); top-loopbackPortFloor >= 1000 {
		for range 500 {
			port := loopbackPortFloor + rand.IntN(top-loopbackPortFloor)
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
			if !loopbackPortsIssued[port] && loopbackPortBindable(addr) {
				loopbackPortsIssued[port] = true
				return addr
			}
		}
	}
	// No room below the ephemeral range on this host: the kernel's choice, as before.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// listenWorld binds a native acceptance world's listener on 127.0.0.1 before the world starts
// anything else, so none of its own helpers (node agent, echo server, control plane) can take
// the port first. An empty or "0" port lets the kernel choose without a gap; the harness then
// reads the address back from the world's facts file.
func listenWorld(t *testing.T, port string) net.Listener {
	t.Helper()
	if port == "" {
		port = "0"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}
