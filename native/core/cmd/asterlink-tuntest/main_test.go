//go:build !windows

package main

import (
	"reflect"
	"testing"
)

func TestParseRouteLinesFiltersHeadersAndVolatileNeighbours(t *testing.T) {
	input := `Routing tables

Internet:
Destination        Gateway            Flags               Netif Expire
default            192.168.1.1        UGScg                 en1
a-neighbour        3c:a1:61:1a:53:c6  UHLWIir               en1   1189
169.254             link#15            UCS                   en1      !

Internet6:
Destination        Gateway            Flags               Netif Expire
::/0               fe80::1            UGcg                  en1
fe80::1            link#15            UHLWI                 en1   1190
`
	want := []string{
		"169.254 link#15 UCS en1",
		"::/0 fe80::1 UGcg en1",
		"default 192.168.1.1 UGScg en1",
	}
	if got := parseRouteLines(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseRouteLines() = %#v, want %#v", got, want)
	}
}

func TestParseRouteLinesKeepsTunnelAndDefaultRoutes(t *testing.T) {
	input := `Destination Gateway Flags Netif Expire
default 192.168.1.1 UGScg en1
10.0.0/8 10.0.0.1 UGSc utun9
192.168.1.1 3c:a1:61:1a:53:c6 UHLWI en1 900
`
	want := []string{
		"10.0.0/8 10.0.0.1 UGSc utun9",
		"default 192.168.1.1 UGScg en1",
	}
	if got := parseRouteLines(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseRouteLines() = %#v, want %#v", got, want)
	}
}
