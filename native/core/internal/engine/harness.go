//go:build asterlink_harness

package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub/executor"
	LC "github.com/metacubex/mihomo/listener/config"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"

	"asterlink/core/internal/managed"
	"asterlink/core/internal/network"
)

// Harness is the in-process injection engine of handoff_app_ios.md 8.8: a test-only stand-in for
// the iOS TUN, compiled solely under the `asterlink_harness` tag (no shipped build uses it).
// The iOS simulator cannot run a packet tunnel, so the billing acceptance runs the real Core and
// the real pinned Mihomo with the TUN removed: the controlled client's TCP connections are
// handed straight to Mihomo's tunnel (the call a utun listener makes for every flow it
// terminates) and leave through the production proxy chain to the production node. Metering
// reads the same proxy-only counters (statistic.DefaultManager) as with a TUN, so the bytes the
// meter charges are the bytes that crossed the node.
//
// What it does not cover is exactly what only a device can: the utun, the system resolver, IPv6
// and the extension. Reports built on it are marked "harness", never "device".
type Harness struct {
	// Probes are the plain-HTTP URLs Verify fetches through the node (the acceptance aims
	// them at the controlled sink; they are counted like any flow).
	Probes       []string
	ProbeTimeout time.Duration

	mu         sync.Mutex
	running    bool
	configured bool
	configData []byte
	home       string
}

func NewHarness() *Harness { return &Harness{ProbeTimeout: 8 * time.Second} }

var (
	_ managed.Engine = (*Harness)(nil)
	_ managed.Gate   = (*Harness)(nil)
)

// Preflight: there is no VPN to conflict with and no physical egress to protect.
func (h *Harness) Preflight(context.Context) (network.Interface, error) {
	return network.Interface{}, nil
}

// Start applies the production iOS profile document to the pinned Mihomo and leaves the TUN
// section off: there is no device, only the tunnel's dispatcher.
func (h *Harness) Start(ctx context.Context, profile managed.Profile) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running {
		return errors.New("engine already running")
	}
	data, err := buildConfig(profile, "ios")
	if err != nil {
		return err
	}
	if !h.configured || !bytes.Equal(h.configData, data) {
		home, err := os.MkdirTemp("", "asterlink-harness-")
		if err != nil {
			return err
		}
		C.SetHomeDir(home)
		cfg, parseErr := executor.ParseWithBytes(data)
		if parseErr != nil {
			_ = os.RemoveAll(home)
			return parseErr
		}
		cfg.General.Tun = LC.Tun{}
		executor.ApplyConfig(cfg, true)
		h.home, h.configData, h.configured = home, append(h.configData[:0], data...), true
	} else {
		tunnel.OnRunning()
	}
	h.running = true
	return nil
}

// Dial is the injection point: a loopback TCP pair whose server side goes to Mihomo's
// dispatcher with the requested destination, exactly as a TUN listener would deliver an
// application's flow. The caller owns the returned (client) side.
func (h *Harness) Dial(ctx context.Context, _, address string) (net.Conn, error) {
	h.mu.Lock()
	running := h.running
	h.mu.Unlock()
	if !running {
		return nil, errors.New("the harness engine is not running")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- conn
	}()
	var dialer net.Dialer
	client, err := dialer.DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		return nil, err
	}
	inbound := <-accepted
	if inbound == nil {
		_ = client.Close()
		return nil, errors.New("harness inbound connection was not accepted")
	}
	metadata := &C.Metadata{NetWork: C.TCP, Type: C.TUN, Host: host, DstPort: uint16(port)}
	if ip, err := netip.ParseAddr(host); err == nil {
		metadata.Host, metadata.DstIP = "", ip
	}
	if local, ok := client.LocalAddr().(*net.TCPAddr); ok {
		if src, ok := netip.AddrFromSlice(local.IP); ok {
			metadata.SrcIP, metadata.SrcPort = src.Unmap(), uint16(local.Port)
		}
	}
	go tunnel.Tunnel.HandleTCPConn(inbound, metadata)
	return client, nil
}

// Verify fetches the probe URLs through the node, injected like any application flow.
func (h *Harness) Verify(ctx context.Context) error {
	client := &http.Client{
		Timeout:       h.ProbeTimeout,
		Transport:     &http.Transport{Proxy: nil, DialContext: h.Dial, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	var last error
	for _, url := range h.Probes {
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

// Totals are Mihomo's proxy-only cumulative bytes, as with the TUN.
func (h *Harness) Totals() (int64, int64, error) {
	up, down := statistic.DefaultManager.TotalTraffic(true)
	return up, down, nil
}

func (h *Harness) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.running {
		return nil
	}
	tunnel.OnSuspend()
	closeFlows()
	executor.Shutdown()
	if h.home != "" {
		_ = os.RemoveAll(h.home)
	}
	// The applied configuration stays, as in Mihomo.stopLocked: an identical profile on the
	// next Start only restores the running state, the reconnect path C01 exercises.
	h.running, h.home = false, ""
	return nil
}

// Suspend and Resume are the authorization gate's hold (managed.Gate), as on the device.
func (h *Harness) Suspend() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.running {
		return errNotRunning
	}
	tunnel.OnSuspend()
	closeFlows()
	return nil
}

func (h *Harness) Resume() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.running {
		return errNotRunning
	}
	tunnel.OnRunning()
	return nil
}

// ActiveConnections counts the proxy flows still tracked.
func (h *Harness) ActiveConnections() int { return len(statistic.DefaultManager.Snapshot().Connections) }
