package engine

import (
	"net"
	"net/netip"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

// testPacket is one UDP datagram as the TUN listener hands it to the tunnel.
type testPacket struct{ data []byte }

func (p *testPacket) Data() []byte                                { return p.data }
func (p *testPacket) WriteBack(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (p *testPacket) Drop()                                       {}
func (p *testPacket) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(198, 18, 0, 1), Port: 50443}
}

// handoff_fix.md F2 + F3 through the pinned core's real pipeline, without a TUN or a node:
// QUIC packets enter tunnel.HandleUDPPacket exactly as the TUN listener delivers them, the
// production rules send them to the built-in REJECT outbound, the statistics tracker sees
// every byte (so the all-traffic total grows), and the billed proxy-only total does not move.
func TestQUICPacketsAreRejectedAndNotBilled(t *testing.T) {
	data, err := BuildConfig(sampleProfile("n", map[string]any{"udp": true}))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := executor.ParseWithBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	tunnel.UpdateProxies(cfg.Proxies, cfg.Providers)
	tunnel.UpdateRules(cfg.Rules, cfg.SubRules, cfg.RuleProviders)
	tunnel.OnRunning()
	defer tunnel.OnSuspend()

	// The REJECT session ends as soon as its no-op connection is read, so the chain is recorded
	// when the tracker joins the manager rather than looked up afterwards.
	chains := make(chan C.Chain, 64)
	previous := statistic.DefaultRequestNotify
	statistic.DefaultRequestNotify = func(c statistic.Tracker) {
		if info := c.Info(); info.Metadata.NetWork == C.UDP && info.Metadata.DstPort == 443 {
			select {
			case chains <- info.Chain:
			default:
			}
		}
	}
	defer func() { statistic.DefaultRequestNotify = previous }()

	proxyUpBefore, proxyDownBefore := statistic.DefaultManager.TotalTraffic(true)
	allUpBefore, _ := statistic.DefaultManager.TotalTraffic(false)

	const packets, size = 20, 1200
	metadata := func() *C.Metadata {
		return &C.Metadata{NetWork: C.UDP, Type: C.TUN,
			SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 50443,
			DstIP: netip.MustParseAddr("203.0.113.9"), DstPort: 443}
	}
	for i := 0; i < packets; i++ {
		tunnel.Tunnel.HandleUDPPacket(&testPacket{data: make([]byte, size)}, metadata())
		time.Sleep(5 * time.Millisecond) // well under the per-worker queue: nothing is dropped for capacity
	}

	deadline := time.Now().Add(5 * time.Second)
	var allUp int64
	for time.Now().Before(deadline) {
		if allUp, _ = statistic.DefaultManager.TotalTraffic(false); allUp-allUpBefore >= packets*size {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := allUp - allUpBefore; got < packets*size {
		t.Fatalf("the REJECT tracker saw %d bytes, want %d: the packets never reached the outbound", got, packets*size)
	}
	select {
	case chain := <-chains:
		if chain.Last() != "REJECT" {
			t.Fatalf("the QUIC flow went to %v, want REJECT", chain)
		}
	default:
		t.Fatal("no tracker joined for the QUIC flow")
	}
	if up, down := statistic.DefaultManager.TotalTraffic(true); up != proxyUpBefore || down != proxyDownBefore {
		t.Fatalf("rejected QUIC was billed: proxy totals %d/%d -> %d/%d", proxyUpBefore, proxyDownBefore, up, down)
	}
}
