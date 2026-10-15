package network

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const wifi = `{"available":true,"interface":"wlan0","index":30,"addresses":["192.168.1.20","2001:db8:1:2::aa","fe80::1"],"dns":["192.168.1.1","fe80::53","2001:4860:4860::8888"],"metered":false}`

func TestHostSnapshotDecodesTheDefaultNetwork(t *testing.T) {
	s, err := ParseHostSnapshot(wifi)
	if err != nil {
		t.Fatal(err)
	}
	egress, err := s.Egress()
	if err != nil || egress != (Interface{Name: "wlan0", Index: 30}) {
		t.Fatalf("egress %+v %v", egress, err)
	}
	// Link-local resolvers need a zone the plain dialer cannot supply.
	if got := s.Resolvers(); !reflect.DeepEqual(got, []string{"192.168.1.1", "2001:4860:4860::8888"}) {
		t.Fatalf("resolvers %v", got)
	}
	// Same identity rules as a desktop interface: IPv6 by /64, link-local ignored.
	if got := s.Signature(); got != "wlan0|192.168.1.20|2001:db8:1:2::/64" {
		t.Fatalf("signature %q", got)
	}
	rotated, err := ParseHostSnapshot(strings.Replace(wifi, "2001:db8:1:2::aa", "2001:db8:1:2::bb", 1))
	if err != nil || rotated.Signature() != s.Signature() {
		t.Fatal("an IPv6 privacy-address rotation must not change the signature")
	}
}

// A report of the VPN itself (DNS 198.18.0.2, the tunnel resolver) must not hand the
// control plane a fake-ip resolver; only the real servers next to it survive.
func TestHostSnapshotNeverOffersTheTunnelResolver(t *testing.T) {
	s, err := ParseHostSnapshot(`{"available":true,"interface":"wlan0","index":3,"addresses":["10.0.2.16"],"dns":["198.18.0.2","198.19.255.1","10.0.2.3"],"metered":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Resolvers(); !reflect.DeepEqual(got, []string{"10.0.2.3"}) {
		t.Fatalf("resolvers %v", got)
	}
}

func TestHostSnapshotWithoutNetwork(t *testing.T) {
	s, err := ParseHostSnapshot(`{"available":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Egress(); !errors.Is(err, ErrNoNetwork) {
		t.Fatalf("got %v", err)
	}
	if s.Signature() != "none" {
		t.Fatal(s.Signature())
	}
}

func TestHostSnapshotIsDecodedStrictly(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":            ``,
		"not json":         `wlan0`,
		"unknown field":    `{"available":true,"interface":"wlan0","index":1,"proxy":"x"}`,
		"trailing data":    `{"available":false}{}`,
		"no interface":     `{"available":true,"index":1}`,
		"no index":         `{"available":true,"interface":"wlan0"}`,
		"bad name":         `{"available":true,"interface":"wlan0; rm","index":1}`,
		"bad address":      `{"available":true,"interface":"wlan0","index":1,"addresses":["300.1.1.1"]}`,
		"bad dns":          `{"available":true,"interface":"wlan0","index":1,"dns":["dns.example"]}`,
		"oversized report": `{"available":false,"interface":"` + strings.Repeat("a", maxHostSnapshot) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseHostSnapshot(raw); !errors.Is(err, ErrHostSnapshot) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}
