package managed

import (
	"testing"
	"time"
)

func TestCatalogMetadataValidation(t *testing.T) {
	a := NodeChoice{ID: "11111111-1111-4111-8111-111111111111", Name: "Ordinary", LineType: "direct", RatePermille: 500, Version: 1}
	b := NodeChoice{ID: "22222222-2222-4222-8222-222222222222", Name: "Premium", LineType: "dedicated", RatePermille: 1000, Version: 1}
	p := Profile{Nodes: []NodeChoice{a, b}, NodeID: a.ID, Session: Status{RatePermille: 500}}
	if !p.ValidCatalog() {
		t.Fatal("valid catalog rejected")
	}
	for _, mutate := range []func(*Profile){
		func(p *Profile) { p.NodeID = "" },
		func(p *Profile) { p.NodeID = "33333333-3333-4333-8333-333333333333" },
		func(p *Profile) { p.Session.RatePermille = 1000 },
		func(p *Profile) { p.Nodes[1].ID = p.Nodes[0].ID },
		func(p *Profile) { p.Nodes[1].Name = p.Nodes[0].Name },
		func(p *Profile) { p.Nodes[1].RatePermille = 0 },
		func(p *Profile) { p.Nodes[1].RatePermille = 10001 },
	} {
		copy := p
		copy.Nodes = append([]NodeChoice(nil), p.Nodes...)
		mutate(&copy)
		if copy.ValidCatalog() {
			t.Fatal("invalid catalog accepted")
		}
	}
}

func TestMeterWeightedEstimateAndOverflow(t *testing.T) {
	for _, test := range []struct{ rate, raw, remaining int64 }{
		{500, 200, 900}, {1000, 200, 800}, {1500, 200, 700},
		{500, 1, 999}, {500, 1999, 0}, {10000, MaxCounter * 2, 0},
	} {
		expires := time.Now().Add(time.Hour)
		m := Meter{clock: time.Now, deadline: expires, status: Status{CanConnect: true, ExpiresAt: expires, RemainingBytes: 1000, RatePermille: test.rate}, upload: test.raw / 2, download: test.raw - test.raw/2}
		status := m.Status()
		if status.RemainingBytes != test.remaining || status.CanConnect != (test.remaining > 0) {
			t.Fatalf("rate %d raw %d: remaining=%d allowed=%v", test.rate, test.raw, status.RemainingBytes, status.CanConnect)
		}
	}
}
