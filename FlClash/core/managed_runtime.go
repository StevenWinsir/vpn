package main

import (
	"context"
	"core/managed"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/adapter/inbound"
	"github.com/metacubex/mihomo/component/resolver"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
	"github.com/metacubex/mihomo/listener"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

var managedDataPlane atomic.Pointer[managedTrafficTunnel]
var managedPlanes = struct {
	sync.Mutex
	items map[*managedTrafficTunnel]struct{}
}{items: map[*managedTrafficTunnel]struct{}{}}

type managedProviderAccess interface{ P.Tunnel }

type managedTrafficTunnel struct {
	C.Tunnel
	managedProviderAccess
	mu      sync.Mutex
	running bool
	tcp     map[net.Conn]struct{}
	packets int
}

func newManagedTrafficTunnel(base C.Tunnel) *managedTrafficTunnel {
	t := &managedTrafficTunnel{Tunnel: base, running: true, tcp: map[net.Conn]struct{}{}}
	managedPlanes.Lock()
	managedPlanes.items[t] = struct{}{}
	managedPlanes.Unlock()
	return t
}

func (t *managedTrafficTunnel) HandleTCPConn(conn net.Conn, metadata *C.Metadata) {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		_ = conn.Close()
		return
	}
	t.tcp[conn] = struct{}{}
	t.mu.Unlock()
	defer func() { t.mu.Lock(); delete(t.tcp, conn); t.mu.Unlock() }()
	t.Tunnel.HandleTCPConn(conn, metadata)
}

func (t *managedTrafficTunnel) HandleUDPPacket(packet C.UDPPacket, metadata *C.Metadata) {
	wrapped := &managedUDPPacket{UDPPacket: packet, owner: t}
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		packet.Drop()
		return
	}
	t.packets++
	t.mu.Unlock()
	t.Tunnel.HandleUDPPacket(wrapped, metadata)
}

type managedUDPPacket struct {
	C.UDPPacket
	owner *managedTrafficTunnel
	once  sync.Once
}

func (p *managedUDPPacket) Drop() {
	p.once.Do(func() {
		p.UDPPacket.Drop()
		p.owner.mu.Lock()
		p.owner.packets--
		p.owner.mu.Unlock()
	})
}

func (p *managedUDPPacket) WriteBack(data []byte, addr net.Addr) (int, error) {
	p.owner.mu.Lock()
	running := p.owner.running
	p.owner.mu.Unlock()
	if !running {
		return 0, net.ErrClosed
	}
	return p.UDPPacket.WriteBack(data, addr)
}

func (p *managedUDPPacket) InAddr() net.Addr {
	if packet, ok := p.UDPPacket.(C.UDPPacketInAddr); ok {
		return packet.InAddr()
	}
	return nil
}

func (t *managedTrafficTunnel) stop() {
	t.mu.Lock()
	t.running = false
	connections := make([]net.Conn, 0, len(t.tcp))
	for conn := range t.tcp {
		connections = append(connections, conn)
	}
	t.mu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
}

func stopManagedDataPlane() {
	if plane := managedDataPlane.Load(); plane != nil {
		plane.stop()
	}
}

func managedTotals(ctx context.Context) (int64, int64, error) {
	if ctx.Err() != nil || !isInit.Load() {
		return 0, 0, managedConfigError("invalid_core_counters")
	}
	up, down := statistic.DefaultManager.TotalTraffic(true)
	return up, down, nil
}

func (e *managedProfileEngine) Start(ctx context.Context, owner managed.ConfigurationOwner, id string, port int) error {
	configMu.Lock()
	defer configMu.Unlock()
	if ctx.Err() != nil || e.view == nil || e.view.ID != id || e.view.Owner != owner || currentConfig == nil || !isInit.Load() {
		return managedConfigError("managed_configuration_required")
	}
	if port < 1024 || port > 65535 {
		return managedConfigError("invalid_runtime_options")
	}
	if managedNetworkCloser != nil {
		return managedConfigError("managed_tun_cleanup_failed")
	}
	plane := newManagedTrafficTunnel(tunnel.Tunnel)
	plane.managedProviderAccess = tunnel.Tunnel
	managedDataPlane.Store(plane)
	listener.SetAllowLan(false)
	listener.SetBindAddress("127.0.0.1")
	loopback := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	inbound.SetAllowedIPs(loopback)
	inbound.SetDisAllowedIPs(nil)
	inbound.SetSkipAuthPrefixes(loopback)
	tunnel.OnRunning()
	network, err := e.networkPolicy().Open(ctx, currentConfig, plane)
	if err != nil {
		plane.stop()
		tunnel.OnSuspend()
		listener.StopListener()
		return err
	}
	managedNetworkCloser = network
	if ctx.Err() != nil {
		plane.stop()
		tunnel.OnSuspend()
		if err := closeManagedNetworkLocked(); err != nil {
			return err
		}
		return managedConfigError("operation_superseded")
	}
	listener.ReCreateMixed(port, plane)
	if listener.GetPorts().MixedPort != port {
		plane.stop()
		tunnel.OnSuspend()
		listener.StopListener()
		if err := closeManagedNetworkLocked(); err != nil {
			return err
		}
		return managedConfigError("runtime_start_failed")
	}
	currentConfig.General.MixedPort = port
	isRunning.Store(true)
	resolver.ResetConnection()
	return nil
}

func (e *managedProfileEngine) Drain(ctx context.Context) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var previousUp, previousDown int64 = -1, -1
	stable := 0
	for {
		configMu.Lock()
		if !isRunning.Load() {
			handleCloseConnections()
		}
		configMu.Unlock()
		busy := false
		managedPlanes.Lock()
		for plane := range managedPlanes.items {
			plane.mu.Lock()
			if !plane.running {
				if len(plane.tcp) == 0 && plane.packets == 0 {
					delete(managedPlanes.items, plane)
				} else {
					busy = true
				}
			}
			plane.mu.Unlock()
		}
		managedPlanes.Unlock()
		up, down := statistic.DefaultManager.TotalTraffic(true)
		if !busy && up == previousUp && down == previousDown {
			stable++
		} else {
			stable = 0
		}
		if stable >= 3 {
			return nil
		}
		previousUp, previousDown = up, down
		select {
		case <-ctx.Done():
			return managedConfigError("traffic_unconfirmed")
		case <-ticker.C:
		}
	}
}

var displayTraffic = struct {
	sync.Mutex
	all, proxy Traffic
}{}

func displayTotalTraffic(onlyProxy bool) Traffic {
	displayTraffic.Lock()
	defer displayTraffic.Unlock()
	baseline := displayTraffic.all
	if onlyProxy {
		baseline = displayTraffic.proxy
	}
	up, down := statistic.DefaultManager.TotalTraffic(onlyProxy)
	return Traffic{Up: max(0, up-baseline.Up), Down: max(0, down-baseline.Down)}
}

func resetDisplayTraffic() {
	displayTraffic.Lock()
	defer displayTraffic.Unlock()
	displayTraffic.all.Up, displayTraffic.all.Down = statistic.DefaultManager.TotalTraffic(false)
	displayTraffic.proxy.Up, displayTraffic.proxy.Down = statistic.DefaultManager.TotalTraffic(true)
}
