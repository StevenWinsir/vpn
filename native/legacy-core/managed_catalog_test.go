//go:build !cgo

package main

import (
	"context"
	"core/managed"
	"strings"
	"testing"

	"github.com/metacubex/mihomo/tunnel"
)

func TestCatalogAppliesOnlyOneCredentialAndDisplaysAuthorizedChoices(t *testing.T) {
	engine := newManagedTestEngine(t)
	data := "proxies: [{name: Ordinary, type: http, server: 127.0.0.1, port: 9}]\nproxy-groups: [{name: VPN, type: select, proxies: [Ordinary]}]\nrules: ['MATCH,VPN']\n"
	p := managedProfileFixture(data)
	p.NodeID = "11111111-1111-4111-8111-111111111111"
	p.Session.RatePermille = 500
	p.Nodes = []managed.NodeChoice{
		{ID: p.NodeID, Name: "Ordinary", LineType: "direct", RatePermille: 500, Version: 1},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "Premium", LineType: "dedicated", RatePermille: 1000, Version: 1},
	}
	prepared, err := engine.Prepare(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	view, err := prepared.Apply(context.Background(), managed.ConfigurationOwner{Generation: 1, UserID: "user-a", SessionID: "session-a"})
	if err != nil || len(view.Groups) != 1 || len(view.Groups[0].Proxies) != 2 || view.Groups[0].Selected != "Ordinary" {
		t.Fatal("authorized catalog not displayed")
	}
	if tunnel.AllProxies()["Ordinary"] == nil || tunnel.AllProxies()["Premium"] != nil {
		t.Fatal("Mihomo loaded more than the rate-bound node")
	}
	if _, err = engine.Select(context.Background(), view.Owner, view.ID, "VPN", "Premium"); err == nil {
		t.Fatal("catalog selection bypassed backend rebinding")
	}
	bad := managedProfileFixture(strings.Replace(data, "proxies: [Ordinary]", "proxies: [Ordinary, DIRECT]", 1))
	bad.NodeID, bad.Nodes, bad.Session.RatePermille = p.NodeID, p.Nodes, 500
	if _, err = engine.Prepare(context.Background(), bad); err == nil {
		t.Fatal("catalog allowed an unmetered route")
	}
}
