package api

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

// TestCoreWithRealAgentEndToEndPostgres is the whole stage-4 loop with every real
// component except the TUN device: the real Core binary logs in, picks a node-metered
// line, obtains a grant that the real node agent installs, "connects", and then
// real bytes sent with that credential through the real forwarder are billed by the
// node alone. The Core never reports a byte. Finally the subscription ends and both
// the Core and the node stop.
func TestCoreWithRealAgentEndToEndPostgres(t *testing.T) {
	coreBin := os.Getenv("ASTERLINK_CORE_BIN")
	if coreBin == "" {
		t.Skip("set ASTERLINK_CORE_BIN (built by scripts/test-native.py)")
	}
	n := newE2E(t, "1", func(c *config.Config) {
		c.ClientReportInterval = 2 * time.Second // keep-alive cadence (lease 4s)
	})
	e := n.env
	server := httptest.NewServer(e.router)
	defer server.Close()
	u := e.newUser(1<<30, 2)

	core := startCore(t, coreBin, server.URL)
	snap := core.login(u.Email, "Native-Test-Password!")
	if snap.State != "ready" {
		t.Fatalf("login: %+v", snap)
	}
	snap, _ = core.waitSnapshot("catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) == 1 })
	if snap.Nodes[0].MeteringSource != "node_reported" || snap.Nodes[0].RatePermille != 500 {
		t.Fatalf("catalog must advertise the node as node-metered: %+v", snap.Nodes)
	}
	reply, selected := core.call("select_node", core.gen, map[string]string{"node_id": n.node.ID, "catalog_revision": snap.CatalogRevision})
	if !reply.OK || selected.ActionError != nil || !selected.ConfigReady {
		t.Fatalf("select: %+v %+v", reply.Error, selected.ActionError)
	}
	began := time.Now()
	reply, connected := core.call("connect", core.gen, nil)
	if !reply.OK || connected.ActionError != nil || connected.State != "connected" {
		t.Fatalf("connect: %+v %+v\n%s", reply.Error, connected.ActionError, n.agentLog())
	}
	t.Logf("Core obtained a node-confirmed grant and connected in %v", time.Since(began))
	if connected.Metering == nil || connected.Metering.Source != "node_reported" {
		t.Fatalf("metering: %+v", connected.Metering)
	}

	// The test plays the TUN: it uses the credential the Core was given (read from the
	// database exactly as the node would be told) and sends real traffic through the
	// real forwarder. The Core's engine carries nothing, so no client report can exist.
	var grant model.ConnectionGrant
	if err := e.db.Where("user_id = ? AND state = ?", u.ID, "ready").First(&grant).Error; err != nil {
		t.Fatalf("no ready grant for the connected user: %v", err)
	}
	credential, err := nodes.OpenCredential(e.cfg.NodeEncryptionKey, grant.ID, grant.CredentialCipher)
	if err != nil {
		t.Fatal(err)
	}
	c, err := n.dial(credential["uuid"])
	if err != nil {
		t.Fatalf("the Core's credential does not work on the node: %v", err)
	}
	defer c.Close()
	const chunk = 128 * 1024
	if err = roundTripBytes(c, chunk); err != nil {
		t.Fatal(err)
	}
	want := int64(2*chunk) * 500
	eventually(t, 10*time.Second, "the node to bill the traffic", func() bool { return e.subscription(u.subID).UsedUnits == want })
	// The balance the Core shows comes from the server, which got it from the node.
	wantRemaining := (int64(1<<30)*1000 - want) / 1000
	snap, took := core.waitSnapshot("the Core to show the node-billed balance", 10*time.Second, func(s snapshotView) bool {
		return s.Metering != nil && s.Metering.ConfirmedRemainingBytes == wantRemaining
	})
	t.Logf("Core showed the node-billed balance %v after the bytes crossed the node (remaining %d)", took, snap.Metering.ConfirmedRemainingBytes)
	var reports int64
	e.db.Model(&model.ClientTrafficReport{}).Where("user_id = ?", u.ID).Count(&reports)
	if reports != 0 {
		t.Fatalf("the Core sent %d client traffic reports in node-reported mode", reports)
	}
	if strings.Contains(string(core.all.Bytes()), credential["uuid"]) || strings.Contains(string(core.all.Bytes()), sharedVlessUUID) {
		t.Fatal("the Core exposed a credential to its host process")
	}

	// The subscription ends. The Core stops on its next keep-alive; the node stops on
	// the revoke order that follows. Both are measured.
	ended := time.Now()
	if err = e.db.Model(&model.Subscription{}).Where("id = ?", u.subID).Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	core.waitSnapshot("the Core to stop after the subscription ended", 15*time.Second, func(s snapshotView) bool {
		// "blocked" is the Core's own verdict that the account may no longer connect. Its
		// LastError is only a transient note: the keep-alive loop clears it on the next
		// successful poll (manager.go), so waiting for LastError raced with that poll. The
		// enforcement that matters is asserted below: the node closes the established flow.
		if s.State == "blocked" {
			return s.Entitlement != nil && !s.Entitlement.CanConnect
		}
		return s.State != "connected" && s.State != "stopping" && s.LastError != nil
	})
	coreStop := time.Since(ended)
	for !isDead(c, 200*time.Millisecond) {
		if time.Since(ended) > 15*time.Second {
			t.Fatal("the node still carried the flow after the subscription ended")
		}
	}
	t.Logf("subscription ended: Core stopped after %v, node closed the established flow after %v", coreStop, time.Since(ended))
	eventually(t, 10*time.Second, "the grant to settle", func() bool {
		g := e.grant(grant.ID)
		return g.State == "revoked" && g.ReleasedAt != nil
	})
	if again, err := n.dial(credential["uuid"]); err == nil {
		again.Close()
		t.Fatal("the credential still works after the subscription ended")
	}
}
