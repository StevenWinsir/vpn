//go:build asterlink_tuntest

package mobile

// Debug-only acceptance hooks for the real Android TUN (handoff_app_android.md 10, round 2:
// TunAcceptanceTest). They drive the production engine package, not a copy, against ONE
// controlled test node, exactly like cmd/asterlink-tuntest does on the desktops, so the
// template and the VpnService path that ship are the ones under test. There is no backend
// session here: no login, lease or meter.
//
// They exist only in an AAR bound with -tags asterlink_tuntest
// (scripts/build-native-android.py --tun-test-hooks). The build record says so, and the app
// build refuses such an AAR unless the tun acceptance build was asked for explicitly
// (apps/android/app/build.gradle.kts), so a normal or release APK can never carry them.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/metacubex/mihomo/tunnel/statistic"

	"asterlink/core/internal/engine"
	"asterlink/core/internal/managed"
	"asterlink/core/internal/network"
)

var tunTest struct {
	mu  sync.Mutex
	eng *engine.Mihomo
}

type tunTestOptions struct {
	// Proxy is one proxy object (the shape a node profile carries), e.g. ss or vless.
	Proxy map[string]any `json:"proxy"`
	// Probes optionally replaces Verify's targets (plain-HTTP URLs reached through the TUN).
	Probes []string `json:"probes,omitempty"`
}

type tunTestResult struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Millis   int64  `json:"ms"`
	IPv6Mode string `json:"ipv6_mode,omitempty"`
	DNSMode  string `json:"dns_mode,omitempty"`
}

func tunTestReply(started time.Time, err error, eng *engine.Mihomo) string {
	r := tunTestResult{OK: err == nil, Millis: time.Since(started).Milliseconds()}
	if err != nil {
		r.Error = err.Error()
	}
	if eng != nil {
		r.IPv6Mode, r.DNSMode = eng.IPv6Mode(), eng.DNSMode()
	}
	out, _ := json.Marshal(r)
	return string(out)
}

// TunTestStart brings the TUN up on the node in optionsJSON: Preflight (a running
// VpnService), the control plane bound to the physical network, then Start, which asks the
// VpnService to establish the interface. Start must have been called (the platform).
func TunTestStart(optionsJSON string) string {
	tunTest.mu.Lock()
	defer tunTest.mu.Unlock()
	started := time.Now()
	var opts tunTestOptions
	decoder := json.NewDecoder(bytes.NewReader([]byte(optionsJSON)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&opts) != nil || len(opts.Proxy) == 0 {
		return tunTestReply(started, errors.New("options must be {\"proxy\":{...}}"), nil)
	}
	if tunTest.eng == nil {
		tunTest.eng = engine.New()
	}
	eng := tunTest.eng
	if len(opts.Probes) > 0 {
		eng.Probes = opts.Probes
	} else {
		eng.Probes = engine.DefaultProbes
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	iface, err := eng.Preflight(ctx)
	if err != nil {
		return tunTestReply(started, err, eng)
	}
	network.Default.Set(iface)
	profile := managed.Profile{NodeID: "tuntest", Version: "tuntest", Proxy: opts.Proxy}
	if err := eng.Start(ctx, profile); err != nil {
		network.Default.Clear()
		return tunTestReply(started, err, eng)
	}
	return tunTestReply(started, nil, eng)
}

// TunTestVerify runs the production Verify: an unbound HTTP probe that must take the same
// path as any app (system resolver -> tunnel DNS -> fake-ip -> TUN -> node).
func TunTestVerify() string {
	tunTest.mu.Lock()
	defer tunTest.mu.Unlock()
	started := time.Now()
	if tunTest.eng == nil {
		return tunTestReply(started, errors.New("not started"), nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return tunTestReply(started, tunTest.eng.Verify(ctx), tunTest.eng)
}

// TunTestDial connects to host:port the way the Core's API client does (network.Default:
// protected socket, name resolved through the default network's own resolvers, never the
// tunnel). It proves the control plane does not depend on the paid node (T05).
func TunTestDial(address string) string {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := network.Default.DialContext(ctx, "tcp", address)
	if err == nil {
		conn.Close()
	}
	return tunTestReply(started, err, nil)
}

type tunTestFlow struct {
	// Destination is what the app asked for (host or IP, and port), as the TUN saw it.
	Destination string `json:"dst"`
	Network     string `json:"network"`
	// Inbound is where the flow came from: "Tun" for app traffic, "Inner" for the core's own
	// connections through the node (the template's DNS-over-TCP nameservers).
	Inbound  string `json:"inbound"`
	Upload   int64  `json:"up"`
	Download int64  `json:"down"`
}

type tunTestStatus struct {
	Upload     int64 `json:"upload"`
	Download   int64 `json:"download"`
	Active     int   `json:"active"`
	Goroutines int   `json:"goroutines"`
	FDs        int   `json:"fds"`
	// Flows are the tracked TUN flows, largest first (at most 64). A routing loop shows up
	// as a flow whose destination is the node itself or the bootstrap resolver: the Core's
	// own outer connection re-entering the tunnel it carries (T05-loop).
	Flows []tunTestFlow `json:"flows"`
}

// TunTestStatus reports the proxy-only counters (what metering bills), the tracked flows and
// the process's goroutines and open descriptors (the 20-cycle resource check).
func TunTestStatus() string {
	tunTest.mu.Lock()
	defer tunTest.mu.Unlock()
	s := tunTestStatus{Flows: []tunTestFlow{}}
	if tunTest.eng != nil {
		s.Upload, s.Download, _ = tunTest.eng.Totals()
		s.Active = tunTest.eng.ActiveConnections()
	}
	for _, c := range statistic.DefaultManager.Snapshot().Connections {
		if c.Metadata == nil {
			continue
		}
		dst := c.Metadata.Host
		if dst == "" {
			dst = c.Metadata.DstIP.String()
		}
		s.Flows = append(s.Flows, tunTestFlow{Destination: net.JoinHostPort(dst, strconv.Itoa(int(c.Metadata.DstPort))),
			Network: c.Metadata.NetWork.String(), Inbound: c.Metadata.Type.String(), Upload: c.UploadTotal.Load(), Download: c.DownloadTotal.Load()})
	}
	slices.SortFunc(s.Flows, func(a, b tunTestFlow) int { return int((b.Upload + b.Download) - (a.Upload + a.Download)) })
	if len(s.Flows) > 64 {
		s.Flows = s.Flows[:64]
	}
	s.Goroutines = runtime.NumGoroutine()
	if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
		s.FDs = len(entries)
	}
	out, _ := json.Marshal(s)
	return string(out)
}

// TunTestGoroutines is the process's goroutine profile grouped by stack (pprof debug=1), so a
// cycle-to-cycle growth can be attributed to the code that leaves goroutines behind.
func TunTestGoroutines() string {
	var out bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&out, 1)
	return out.String()
}

// TunTestStop closes the data path; the VPN interface goes with the descriptor.
func TunTestStop() string {
	tunTest.mu.Lock()
	defer tunTest.mu.Unlock()
	started := time.Now()
	if tunTest.eng != nil {
		_ = tunTest.eng.Stop()
	}
	network.Default.Clear()
	return tunTestReply(started, nil, nil)
}
