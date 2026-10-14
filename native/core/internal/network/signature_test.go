package network

import (
	"net/netip"
	"testing"
)

func addrs(list ...string) []netip.Addr {
	out := make([]netip.Addr, len(list))
	for i, s := range list {
		out[i] = netip.MustParseAddr(s)
	}
	return out
}

func TestSignatureIgnoresIPv6TemporaryAddressRotation(t *testing.T) {
	before := signatureFrom("Wi-Fi", addrs("192.168.1.20", "2001:db8:1:2::10", "2001:db8:1:2:aaaa:bbbb:cccc:dddd", "fe80::1"))
	after := signatureFrom("Wi-Fi", addrs("192.168.1.20", "2001:db8:1:2::10", "2001:db8:1:2:1111:2222:3333:4444", "fe80::2"))
	if before != after {
		t.Fatalf("a rotated privacy address changed the signature:\n%s\n%s", before, after)
	}
}

func TestSignatureChangesWhenTheNetworkMoves(t *testing.T) {
	base := signatureFrom("Wi-Fi", addrs("192.168.1.20", "2001:db8:1:2::10"))
	for name, other := range map[string]string{
		"new IPv4 address":  signatureFrom("Wi-Fi", addrs("192.168.1.21", "2001:db8:1:2::10")),
		"new IPv6 prefix":   signatureFrom("Wi-Fi", addrs("192.168.1.20", "2001:db8:9:9::10")),
		"other adapter":     signatureFrom("Ethernet", addrs("192.168.1.20", "2001:db8:1:2::10")),
		"IPv6 disappeared":  signatureFrom("Wi-Fi", addrs("192.168.1.20")),
		"IPv4 disappeared":  signatureFrom("Wi-Fi", addrs("2001:db8:1:2::10")),
		"second IPv4 added": signatureFrom("Wi-Fi", addrs("192.168.1.20", "10.0.0.5", "2001:db8:1:2::10")),
	} {
		if other == base {
			t.Errorf("%s did not change the signature", name)
		}
	}
}

func TestSignatureIsOrderIndependentAndSkipsNonRoutable(t *testing.T) {
	a := signatureFrom("Ethernet", addrs("10.0.0.5", "192.168.1.20", "127.0.0.1", "169.254.7.7", "::1"))
	b := signatureFrom("Ethernet", addrs("192.168.1.20", "10.0.0.5"))
	if a != b {
		t.Fatalf("%s != %s", a, b)
	}
}
