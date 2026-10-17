package engine

import (
	"encoding/binary"
	"encoding/json"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/metacubex/mihomo/hub/executor"
)

// iosTun parses the iOS template with the pinned core, as Start does.
func iosTun(t *testing.T) tunConfig {
	t.Helper()
	data, err := buildConfig(sampleProfile("n", nil), "ios")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := executor.ParseWithBytes(data)
	if err != nil {
		t.Fatalf("pinned core rejected the iOS template: %v", err)
	}
	if want := []string{"MATCH," + ProxyName, "MATCH,REJECT"}; len(cfg.Rules) != len(want) {
		t.Fatalf("rules %v", cfg.Rules)
	}
	return cfg.General.Tun
}

// The iOS document hands routing to NEPacketTunnelNetworkSettings exactly as Android hands it
// to the VpnService, and fixes both addresses.
func TestIOSTemplateHandsRoutingToTheProvider(t *testing.T) {
	ios := iosTun(t)
	android := androidTun(t)
	if ios.AutoRoute || ios.AutoDetectInterface || ios.StrictRoute || ios.Device != "" || ios.FileDescriptor != 0 {
		t.Fatalf("iOS must not program routes, detect interfaces, name a device or carry a descriptor: %+v", ios)
	}
	if ios.MTU != IOSMTU || !ios.Enable || ios.Stack != android.Stack ||
		strings.Join(ios.DNSHijack, ",") != strings.Join(android.DNSHijack, ",") {
		t.Fatalf("iOS diverged beyond routing/detection/mtu/addresses:\nios     %+v\nandroid %+v", ios, android)
	}
	if len(ios.Inet4Address) != 1 || ios.Inet4Address[0] != netip.MustParsePrefix(IOSInet4) {
		t.Fatalf("tunnel address %v", ios.Inet4Address)
	}
}

type iosSpecContract struct {
	Produced []struct {
		Name     string `json:"name"`
		NodeIPv6 bool   `json:"node_ipv6"`
		Spec     string `json:"spec"`
	} `json:"produced"`
	Accept []struct {
		Spec string `json:"spec"`
	} `json:"accept"`
}

// tun-spec-ios-v1: whatever the node and whatever the host's own IPv6, the provider is told to
// claim IPv6. The pinned core drops the IPv6 address on a host without global IPv6; both
// cases are produced here explicitly.
func TestIOSTunSpecContract(t *testing.T) {
	data, err := os.ReadFile("../../../../shared/contracts/tun-spec-ios-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var c iosSpecContract
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	withV6 := iosTun(t)
	if len(withV6.Inet6Address) == 0 {
		withV6.Inet6Address = []netip.Prefix{netip.MustParsePrefix(IOSInet6)}
	}
	withoutV6 := withV6
	withoutV6.Inet6Address = nil
	for _, conf := range []tunConfig{withV6, withoutV6} {
		spec, err := tunSpecFor(iosTunConf(conf))
		if err != nil {
			t.Fatal(err)
		}
		doc, _ := spec.encode()
		for _, produced := range c.Produced {
			// The spec does not depend on the node: refusal happens inside the tunnel.
			if doc != produced.Spec {
				t.Fatalf("%s:\n got %s\nwant %s", produced.Name, doc, produced.Spec)
			}
		}
	}
	for _, accepted := range c.Accept {
		var spec TunSpec
		if err := json.Unmarshal([]byte(accepted.Spec), &spec); err != nil || !spec.IPv6 {
			t.Fatalf("accept case %s", accepted.Spec)
		}
	}
}

// ---------------------------------------------------------------- IPv6 ingress refusal

var (
	appAddr  = netip.MustParseAddr("2001:db8::10")
	siteAddr = netip.MustParseAddr("2606:4700::1111")
)

func ipv6Packet(next byte, src, dst netip.Addr, payload []byte) []byte {
	p := make([]byte, ipv6HeaderLen+len(payload))
	writeIPv6Header(p, len(payload), next, src, dst)
	copy(p[ipv6HeaderLen:], payload)
	return p
}

func tcpSegment(flags byte, seq, ack uint32, data int) []byte {
	s := make([]byte, 20+data)
	binary.BigEndian.PutUint16(s[0:2], 50000)
	binary.BigEndian.PutUint16(s[2:4], 443)
	binary.BigEndian.PutUint32(s[4:8], seq)
	binary.BigEndian.PutUint32(s[8:12], ack)
	s[12] = 5 << 4
	s[13] = flags
	return s
}

// checksumOK verifies a reply's upper-layer checksum independently of the code that wrote it.
func checksumOK(t *testing.T, reply []byte) {
	t.Helper()
	src := netip.AddrFrom16([16]byte(reply[8:24]))
	dst := netip.AddrFrom16([16]byte(reply[24:40]))
	payload := append([]byte(nil), reply[ipv6HeaderLen:]...)
	field := 16 // TCP
	if reply[6] == protoICMPv6 {
		field = 2
	}
	got := binary.BigEndian.Uint16(payload[field:])
	payload[field], payload[field+1] = 0, 0
	if want := transportChecksum(src, dst, reply[6], payload); got != want || got == 0 {
		t.Fatalf("checksum %04x, want %04x", got, want)
	}
}

func TestRefuseIPv6TCP(t *testing.T) {
	// A SYN gets RST|ACK acknowledging the SYN, from the address it was sent to.
	reply := refuseIPv6(ipv6Packet(protoTCP, appAddr, siteAddr, tcpSegment(tcpFlagSYN, 1000, 0, 0)))
	if reply == nil || len(reply) != ipv6HeaderLen+20 || reply[6] != protoTCP {
		t.Fatalf("SYN reply %x", reply)
	}
	tcp := reply[ipv6HeaderLen:]
	if netip.AddrFrom16([16]byte(reply[8:24])) != siteAddr || netip.AddrFrom16([16]byte(reply[24:40])) != appAddr {
		t.Fatal("reply addresses not swapped")
	}
	if binary.BigEndian.Uint16(tcp[0:2]) != 443 || binary.BigEndian.Uint16(tcp[2:4]) != 50000 {
		t.Fatal("reply ports not swapped")
	}
	if tcp[13] != tcpFlagRST|tcpFlagACK || binary.BigEndian.Uint32(tcp[4:8]) != 0 || binary.BigEndian.Uint32(tcp[8:12]) != 1001 {
		t.Fatalf("SYN reset flags %02x seq %d ack %d", tcp[13], binary.BigEndian.Uint32(tcp[4:8]), binary.BigEndian.Uint32(tcp[8:12]))
	}
	checksumOK(t, reply)

	// A segment carrying ACK gets a bare RST at the acknowledged sequence.
	reply = refuseIPv6(ipv6Packet(protoTCP, appAddr, siteAddr, tcpSegment(tcpFlagACK, 5, 777, 10)))
	tcp = reply[ipv6HeaderLen:]
	if tcp[13] != tcpFlagRST || binary.BigEndian.Uint32(tcp[4:8]) != 777 {
		t.Fatalf("ACK reset flags %02x seq %d", tcp[13], binary.BigEndian.Uint32(tcp[4:8]))
	}
	checksumOK(t, reply)

	// A reset is never answered.
	if refuseIPv6(ipv6Packet(protoTCP, appAddr, siteAddr, tcpSegment(tcpFlagRST, 1, 0, 0))) != nil {
		t.Fatal("answered a reset")
	}
}

func TestRefuseIPv6UDPAndOthers(t *testing.T) {
	udp := make([]byte, 8+1400) // QUIC-sized: the quote is cut to the minimum MTU
	binary.BigEndian.PutUint16(udp[0:2], 51000)
	binary.BigEndian.PutUint16(udp[2:4], 443)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)))
	invoking := ipv6Packet(protoUDP, appAddr, siteAddr, udp)
	reply := refuseIPv6(invoking)
	if reply == nil || reply[6] != protoICMPv6 || len(reply) != icmpv6MaxError {
		t.Fatalf("UDP reply length %d", len(reply))
	}
	icmp := reply[ipv6HeaderLen:]
	if icmp[0] != icmpv6Unreach || icmp[1] != icmpv6AdminBan || string(icmp[8:48]) != string(invoking[:40]) {
		t.Fatal("ICMPv6 error does not quote the datagram")
	}
	checksumOK(t, reply)

	echo := []byte{128, 0, 0, 0, 0, 1, 0, 1}
	multicast := netip.MustParseAddr("ff02::1")
	for name, packet := range map[string][]byte{
		"icmpv6 echo":         ipv6Packet(protoICMPv6, appAddr, siteAddr, echo),
		"icmpv6 error":        ipv6Packet(protoICMPv6, appAddr, siteAddr, []byte{1, 4, 0, 0, 0, 0, 0, 0}),
		"multicast":           ipv6Packet(protoUDP, appAddr, multicast, udp[:8]),
		"unspecified source":  ipv6Packet(protoTCP, netip.IPv6Unspecified(), siteAddr, tcpSegment(tcpFlagSYN, 1, 0, 0)),
		"hop-by-hop header":   ipv6Packet(0, appAddr, siteAddr, make([]byte, 8)),
		"truncated":           ipv6Packet(protoTCP, appAddr, siteAddr, tcpSegment(tcpFlagSYN, 1, 0, 0))[:50],
		"short tcp":           ipv6Packet(protoTCP, appAddr, siteAddr, make([]byte, 10)),
		"ipv4":                append([]byte{0x45}, make([]byte, 39)...),
		"shorter than header": {0x60, 0, 0},
	} {
		if refuseIPv6(packet) != nil {
			t.Fatalf("%s was answered", name)
		}
	}
}
