package engine

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

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

// Mihomo is the production Engine.
type Mihomo struct {
	// Probes and the timeouts are variables only so the acceptance harness can aim
	// them at controlled targets.
	Probes       []string
	StartTimeout time.Duration
	ProbeTimeout time.Duration

	mu      sync.Mutex
	running bool
	home    string
}

func New() *Mihomo {
	return &Mihomo{Probes: DefaultProbes, StartTimeout: 15 * time.Second, ProbeTimeout: 8 * time.Second}
}

var _ managed.Engine = (*Mihomo)(nil)

// Preflight refuses to stack on another VPN and finds the physical egress that the
// outer node connection and the Core's own API calls must keep using.
func (e *Mihomo) Preflight(context.Context) (network.Interface, error) {
	if runtime.GOOS != "darwin" {
		return network.Interface{}, managed.ErrTunUnavailable
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

func tunnelDevices() map[string]bool {
	found := map[string]bool{}
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		if strings.HasPrefix(i.Name, "utun") {
			found[i.Name] = true
		}
	}
	return found
}

// Start brings the TUN up and returns once the device and its routes exist. It does
// not claim the tunnel forwards: Verify does.
func (e *Mihomo) Start(ctx context.Context, profile managed.Profile) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return errors.New("engine already running")
	}
	if runtime.GOOS != "darwin" {
		return managed.ErrTunUnavailable
	}
	data, err := BuildConfig(profile)
	if err != nil {
		return err
	}
	home, err := os.MkdirTemp("", "asterlink-engine-")
	if err != nil {
		return err
	}
	constant.SetHomeDir(home)
	cfg, err := executor.ParseWithBytes(data)
	if err != nil {
		_ = os.RemoveAll(home)
		return err
	}
	before := tunnelDevices()
	e.home, e.running = home, true
	executor.ApplyConfig(cfg, true)
	// The pinned fork's ApplyConfig leaves listeners alone (docs/macos-tun-poc.md), so
	// the TUN is created explicitly, and torn down explicitly in Stop.
	listener.ReCreateTun(cfg.General.Tun, tunnel.Tunnel)

	deadline := time.Now().Add(e.StartTimeout)
	for {
		added := false
		for name := range tunnelDevices() {
			if !before[name] {
				added = true
			}
		}
		if added && network.ViaTunnel() != "" {
			return nil
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
	for _, url := range e.Probes {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 400 {
			return nil
		}
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

// Stop closes the data path: no new flows, every tracked flow closed, device removed.
func (e *Mihomo) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopLocked()
	return nil
}

func (e *Mihomo) stopLocked() {
	if !e.running {
		return
	}
	listener.ReCreateTun(LC.Tun{}, tunnel.Tunnel)
	statistic.DefaultManager.Range(func(c statistic.Tracker) bool {
		_ = c.Close()
		return true
	})
	executor.Shutdown()
	_ = os.RemoveAll(e.home)
	e.running, e.home = false, ""
	// Give the kernel a moment to retire the device and its routes.
	for range 20 {
		if network.ViaTunnel() == "" {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
}
