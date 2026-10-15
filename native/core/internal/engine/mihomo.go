package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/metacubex/mihomo/component/iface"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/listener"
	LC "github.com/metacubex/mihomo/listener/config"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"

	"asterlink/core/internal/managed"
	"asterlink/core/internal/network"
)

// DefaultProbes are plain-HTTP reachability endpoints. They are fetched through the
// tunnel (the Core's probe client has no proxy and no interface binding), so a 2xx
// answer proves the whole chain: utun -> Mihomo -> paid node -> internet.
var DefaultProbes = []string{
	"http://cp.cloudflare.com/generate_204",
	"http://www.gstatic.com/generate_204",
	"http://captive.apple.com/hotspot-detect.html",
}

// tunConfig is Mihomo's parsed TUN section; platform files adjust it before the device is
// created.
type tunConfig = LC.Tun

// Mihomo is the production Engine.
type Mihomo struct {
	// Probes and the timeouts are variables only so the acceptance harness can aim
	// them at controlled targets.
	Probes       []string
	StartTimeout time.Duration
	ProbeTimeout time.Duration
	IPv6Timeout  time.Duration

	mu         sync.Mutex
	running    bool
	home       string
	configData []byte
	tunConf    LC.Tun
	configured bool
	// ipv6Capable is probed once per applied profile; ipv6Mode/ipv6Gateway describe the
	// running session so Stop removes exactly the routes this session added.
	ipv6Capable bool
	ipv6Mode    string
	ipv6Gateway netip.Addr
	// dnsMode is DNSCaptured while the system resolver points into the tunnel.
	dnsMode string
}

func New() *Mihomo {
	return &Mihomo{Probes: DefaultProbes, StartTimeout: 15 * time.Second, ProbeTimeout: 8 * time.Second, IPv6Timeout: 5 * time.Second}
}

var _ managed.Engine = (*Mihomo)(nil)

// Preflight refuses to stack on another VPN and finds the physical egress that the
// outer node connection and the Core's own API calls must keep using.
func (e *Mihomo) Preflight(ctx context.Context) (network.Interface, error) {
	if !platformSupported() {
		return network.Interface{}, managed.ErrTunUnavailable
	}
	if err := preflightPlatform(ctx); err != nil {
		return network.Interface{}, err
	}
	if len(network.Conflicts()) > 0 {
		return network.Interface{}, managed.ErrVPNConflict
	}
	iface, err := network.PhysicalDefault()
	if err != nil {
		return network.Interface{}, managed.ErrNoNetwork
	}
	return iface, nil
}

// Start brings the TUN up and returns once the device and its routes exist. It does
// not claim the tunnel forwards: Verify does.
func (e *Mihomo) Start(ctx context.Context, profile managed.Profile) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return errors.New("engine already running")
	}
	if !platformSupported() {
		return managed.ErrTunUnavailable
	}
	data, err := BuildConfig(profile)
	if err != nil {
		return err
	}
	// Reusing the reviewed in-memory Mihomo configuration for an identical
	// profile avoids repeatedly starting resolver/reset goroutines and keeps
	// same-process reconnects deterministic. A changed profile still performs a
	// complete ApplyConfig so node changes cannot inherit old runtime state.
	home := ""
	tunConf := e.tunConf
	if !e.configured || !bytes.Equal(e.configData, data) {
		home, err = os.MkdirTemp("", "asterlink-engine-")
		if err != nil {
			return err
		}
		constant.SetHomeDir(home)
		cfg, parseErr := executor.ParseWithBytes(data)
		if parseErr != nil {
			_ = os.RemoveAll(home)
			return parseErr
		}
		tunConf = cfg.General.Tun
		executor.ApplyConfig(cfg, true)
		e.configData = append(e.configData[:0], data...)
		e.tunConf = tunConf
		e.configured = true
		// Before the TUN exists, so the probe reaches the node over the physical path.
		e.ipv6Capable = len(tunConf.Inet6Address) > 0 && nodeCarriesIPv6(ctx, e.IPv6Timeout)
	} else {
		// ApplyConfig marks the tunnel running. Reconnects that reuse the same
		// profile need to restore that lifecycle state explicitly after Stop.
		tunnel.OnRunning()
	}
	before := tunnelDevices()
	e.home, e.running = home, true
	// The pinned fork's ApplyConfig leaves listeners alone (docs/macos-tun-poc.md), so
	// the TUN is created explicitly, and torn down explicitly in Stop. ReCreateTun only
	// logs its failure, so the log is watched to turn it into a precise error.
	failures := watchTunErrors()
	defer failures.Close()
	// Android: the VpnService establishes the interface now and its descriptor joins the
	// section; the desktops pass it through (sing-tun creates the device itself).
	running, err := attachTun(e.prepareTunConf(tunConf))
	if err != nil {
		e.stopLocked()
		return fmt.Errorf("%w: %w", managed.ErrTunnelFailed, err)
	}
	listener.ReCreateTun(running, tunnel.Tunnel)

	deadline := time.Now().Add(e.StartTimeout)
	for {
		if tunReady(before) {
			e.applyIPv6Policy(tunConf)
			// Only once the TUN answers on TunnelDNS: pointing the resolver there
			// earlier would black-hole every lookup on the machine.
			physical, err := network.PhysicalDefault()
			if err != nil || captureSystemDNS(physical.Name) != nil {
				e.stopLocked()
				return fmt.Errorf("%w: %w", managed.ErrTunnelFailed, ErrDNSCapture)
			}
			e.dnsMode = DNSCaptured
			return nil
		}
		if reason := failures.Err(); reason != "" {
			e.stopLocked()
			return fmt.Errorf("%w: %s", managed.ErrTunnelFailed, reason)
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			e.stopLocked()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return managed.ErrTunnelFailed
		}
		select {
		case <-ctx.Done():
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Verify proves real forwarding with a bounded probe through the tunnel.
func (e *Mihomo) Verify(ctx context.Context) error {
	client := &http.Client{
		Timeout: e.ProbeTimeout,
		// No proxy setting and no interface binding: the request must take the same
		// path as any application, i.e. the TUN.
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: e.ProbeTimeout},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	// The last probe's failure travels with the stable error (errors.Is still matches), so a
	// report can say why; it names only the public probe URL.
	var last error
	for _, url := range e.Probes {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			last = err
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			last = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 400 {
			return nil
		}
		last = fmt.Errorf("probe answered %d", response.StatusCode)
	}
	if last != nil {
		return fmt.Errorf("%w: %w", managed.ErrTunnelUnhealthy, last)
	}
	return managed.ErrTunnelUnhealthy
}

// Totals are Mihomo's proxy-only cumulative bytes (chain != DIRECT): payload carried
// to and from the paid node, excluding the Core's bound control-plane traffic, which
// never enters the tunnel. They persist across Stop/Start inside one process.
func (e *Mihomo) Totals() (int64, int64, error) {
	up, down := statistic.DefaultManager.TotalTraffic(true)
	return up, down, nil
}

// ActiveConnections reports tracked proxy flows that are still draining. The
// privileged acceptance harness records this alongside its idle-window counter
// so host background traffic is distinguished from an internal routing loop; it
// does not expose a control-plane listener or alter the production session API.
func (e *Mihomo) ActiveConnections() int {
	return len(statistic.DefaultManager.Snapshot().Connections)
}

// Stop closes the data path: no new flows, every tracked flow closed, device removed.
func (e *Mihomo) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopLocked()
	return nil
}

// NetworkChanged is the host's hint that the default network moved (Android:
// ConnectivityManager). Mihomo's own default-interface monitor is off on Android
// (auto-detect-interface needs netlink), so this does its work: cached interface data is
// dropped and the resolvers reconnect over the new network. Whether the session may
// continue is not decided here; the managed watchdog compares network signatures.
func NetworkChanged() {
	iface.FlushCache()
	resolver.ResetConnection()
}

// DNSMode reports whether the running session captured the system resolver.
func (e *Mihomo) DNSMode() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dnsMode
}

// IPv6Mode reports how the running session handles IPv6 (one of the IPv6* constants).
func (e *Mihomo) IPv6Mode() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ipv6Mode
}

func (e *Mihomo) stopLocked() {
	if !e.running {
		return
	}
	// The resolver goes back first, while the tunnel still answers on TunnelDNS, so no
	// lookup in between is sent to an address that no longer exists. A failed restore
	// keeps its record for the next Core start or the Helper.
	if err := RestoreSystemDNS(); err == nil {
		e.dnsMode = ""
	}
	e.releaseIPv6Policy()
	tunnel.OnSuspend()
	listener.ReCreateTun(LC.Tun{}, tunnel.Tunnel)
	// Android: the listener closed the VPN descriptor it owned; one it never took is closed
	// here, so the interface disappears either way.
	releaseTun()
	statistic.DefaultManager.Range(func(c statistic.Tracker) bool {
		_ = c.Close()
		return true
	})
	// Closing a tracker wakes its socket, but the handler goroutine may need a
	// scheduling turn to leave the manager. Wait briefly so repeated in-process
	// Start/Stop cycles do not accumulate old flow workers.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(statistic.DefaultManager.Snapshot().Connections) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	executor.Shutdown()
	if e.home != "" {
		_ = os.RemoveAll(e.home)
	}
	e.running, e.home = false, ""
	// Give the kernel a moment to retire the device and its routes.
	for range 20 {
		if network.ViaTunnel() == "" {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
}
