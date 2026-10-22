package api

import (
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// Round 7 of the presence plan (handoff_node_use_history_and_load_balancing_c.md 5.5 and
// 12): the Core of the last release before node groups (a63a882, built from git history by
// scripts/test-native.py) is what installed apps run until the new Core ships. It never
// calls /connections or release; it binds a line through /config when the user selects it
// and renews through /traffic. Against this tree it must see groups as lines, and the
// online list, the device cap and server capacity must all apply to it.

func (c *coreProc) selectOld(t *testing.T, id string) snapshotView {
	t.Helper()
	snap, _ := c.waitSnapshot("catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) > 0 })
	reply, s := c.call("select_node", c.gen, map[string]string{"node_id": id, "catalog_revision": snap.CatalogRevision})
	if !reply.OK {
		t.Fatalf("select refused outright: %+v", reply.Error)
	}
	return s
}

func TestReleasedCoreAgainstNodeGroupsPostgres(t *testing.T) {
	bin := os.Getenv("ASTERLINK_RELEASED_CORE_BIN")
	if bin == "" {
		t.Skip("set ASTERLINK_RELEASED_CORE_BIN (built from the released commit by scripts/test-native.py)")
	}
	e := newPresenceEnv(t, func(c *config.Config) {
		c.ClientReportInterval = 2 * time.Second // reports (and so renewals) every 2 s of real time
		c.Origins = []string{"http://127.0.0.1:3000"}
	})
	e.tick = time.Microsecond
	server := httptest.NewServer(e.router)
	defer server.Close()
	g := e.group("JP1", "[]")
	servers := e.servers(g, 5, 5) // two servers with the same proxy name
	full := e.group("Single", "[]")
	e.servers(full, 1)
	u := e.user(5) // D1: min(plan 5, CLIENT_MAX_ONLINE_DEVICES 2) = 2
	_, adminCookie := e.admin()

	cores := []*coreProc{}
	for range 3 {
		c := startCore(t, bin, server.URL)
		c.login(u.Email, presencePassword)
		cores = append(cores, c)
	}

	t.Run("groups are lines; selection binds and counts", func(t *testing.T) {
		snap, _ := cores[0].waitSnapshot("catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) > 0 })
		names := map[string]string{}
		for _, n := range snap.Nodes {
			names[n.ID] = n.Name
		}
		if len(snap.Nodes) != 2 || names[g.ID] != "JP1" || names[full.ID] != "Single" {
			t.Fatalf("released Core catalog: %+v", snap.Nodes)
		}
		if n := e.live(u.ID); n != 0 {
			t.Fatalf("signing in took %d slots", n)
		}
		for _, c := range cores[:2] {
			if s := c.selectOld(t, g.ID); s.ActionError != nil || !s.ConfigReady || s.SelectedNodeID != g.ID {
				t.Fatalf("select: %+v ready=%v selected=%s", s.ActionError, s.ConfigReady, s.SelectedNodeID)
			}
		}
		// The released Core binds at selection, so the slot is taken then (plan 5.5).
		if n := e.live(u.ID); n != 2 {
			t.Fatalf("live leases after two selections: %d", n)
		}
		for _, c := range cores[:2] {
			l := e.activeLease(c.device)
			if l == nil || l.Method != model.LeaseByLegacy || l.GroupID != g.ID {
				t.Fatalf("lease of %s: %+v", c.device, l)
			}
		}
	})

	t.Run("the third device is refused with device_limit", func(t *testing.T) {
		s := cores[2].selectOld(t, g.ID)
		if s.ActionError == nil || s.ActionError.Code != "device_limit" || s.ActionError.Message == "" || s.ConfigReady {
			t.Fatalf("third device: %+v ready=%v", s.ActionError, s.ConfigReady)
		}
		if n := e.live(u.ID); n != 2 {
			t.Fatalf("live leases %d", n)
		}
	})

	t.Run("connected traffic renews and reaches the website", func(t *testing.T) {
		mover := cores[0]
		mover.carry(0, 0)
		_, s := mover.call("connect", mover.gen, nil)
		if s.ActionError != nil || s.State != "connected" {
			t.Fatalf("connect: %+v %+v", s.ActionError, s.LastError)
		}
		before := e.activeLease(mover.device)
		mover.carry(1<<20, 4<<20)
		eventually(t, 10*time.Second, "the report to renew the legacy lease", func() bool {
			l := e.activeLease(mover.device)
			return l != nil && l.ID == before.ID && l.RenewedAt.After(before.RenewedAt)
		})
		devices := e.webGet(e.web(u), "/me/devices")
		online, _ := devices["online"].([]any)
		if devices["online_count"] != float64(2) || devices["device_limit"] != float64(2) || len(online) != 2 {
			t.Fatalf("/me/devices: %v", devices)
		}
		for _, item := range online {
			d := item.(map[string]any)
			if d["line"] != "JP1" || d["app_version"] != "e2e" || d["platform"] == "" {
				t.Fatalf("online device: %v", d)
			}
		}
		var buckets []model.UsageBucket
		e.db.Where("user_id = ?", u.ID).Find(&buckets)
		var down int64
		for _, b := range buckets {
			down += b.DownloadBytes
		}
		if down != 4<<20 {
			t.Fatalf("usage history download %d", down)
		}
	})

	t.Run("a withdrawn server stops the released Core; its next bind lands on the other server", func(t *testing.T) {
		mover := cores[0]
		before := e.activeLease(mover.device)
		var withdrawn model.Node
		for _, s := range servers {
			if s.ID == before.NodeID {
				withdrawn = s
			}
		}
		e.want(e.editServer(adminCookie, withdrawn, "", 5, "", false), 200, "")
		// The code it already knows (plan 5.5): profile_changed on the next /traffic receipt.
		stopped, _ := mover.waitSnapshot("the profile_changed stop", 15*time.Second, func(s snapshotView) bool {
			return s.State != "connected" && s.LastError != nil && s.LastError.Code == "profile_changed"
		})
		if stopped.Metering != nil && stopped.Metering.Pending {
			t.Fatalf("tail not settled: %+v", stopped.Metering)
		}
		if s := mover.selectOld(t, g.ID); s.ActionError != nil || !s.ConfigReady {
			t.Fatalf("re-select: %+v", s.ActionError)
		}
		after := e.activeLease(mover.device)
		if after == nil || after.NodeID == withdrawn.ID || after.GroupID != g.ID {
			t.Fatalf("re-bound to %+v (withdrawn %s)", after, withdrawn.ID)
		}
		if _, s := mover.call("connect", mover.gen, nil); s.State != "connected" {
			t.Fatalf("reconnect: %+v %+v", s.ActionError, s.LastError)
		}
		var sess model.NativeSession
		e.db.Take(&sess, "device_id = ? AND revoked_at IS NULL", mover.device)
		var reports []model.ClientTrafficReport
		e.db.Where("session_id = ?", sess.ID).Order("sequence").Find(&reports)
		for i, r := range reports {
			if r.Sequence != int64(i+1) {
				t.Fatalf("sequence gap at %d: %+v", i, r)
			}
		}
		if sess.DownloadBytes != 4<<20 {
			t.Fatalf("ledger download %d", sess.DownloadBytes)
		}
		if _, s := mover.call("disconnect", 0, nil); s.State == "connected" {
			t.Fatalf("disconnect: %s", s.State)
		}
	})

	t.Run("quitting the released Core frees its slot at once", func(t *testing.T) {
		// The withdrawal also ended this idle device's lease (sticky assignment had put both
		// devices on that server); it binds again on the remaining one first.
		quitter := cores[1]
		if s := quitter.selectOld(t, g.ID); s.ActionError != nil || !s.ConfigReady {
			t.Fatalf("re-select: %+v", s.ActionError)
		}
		held := e.activeLease(quitter.device)
		if held == nil {
			t.Fatal("no lease before the exit")
		}
		// Closing its channel (app exit, Windows pipe, macOS Helper) ends its session.
		quitter.close()
		ended := e.lease(held.ID)
		if ended.State != model.LeaseEnded {
			t.Fatalf("lease after the Core exited: %+v", ended)
		}
		t.Logf("released Core exit ended its lease as %q", ended.EndReason)
		if s := cores[2].selectOld(t, g.ID); s.ActionError != nil || !s.ConfigReady {
			t.Fatalf("slot not free after the exit: %+v", s.ActionError)
		}
	})

	t.Run("a killed released Core frees its slot when the lease lapses (90 s, injected clock)", func(t *testing.T) {
		killed := cores[0]
		if err := killed.cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_, _ = killed.cmd.Process.Wait()
		late := startCore(t, bin, server.URL)
		late.login(u.Email, presencePassword)
		if s := late.selectOld(t, g.ID); s.ActionError == nil || s.ActionError.Code != "device_limit" {
			t.Fatalf("slot freed before the lease lapsed: %+v", s.ActionError)
		}
		e.advance(e.cfg.Lease() - time.Second)
		if n := e.live(u.ID); n != 2 {
			t.Fatalf("at lease-1 s: %d live", n)
		}
		e.advance(2 * time.Second)
		if s := late.selectOld(t, g.ID); s.ActionError != nil || !s.ConfigReady {
			t.Fatalf("slot not returned after the lease: %+v", s.ActionError)
		}
	})

	t.Run("a full server refuses the released Core with group_full", func(t *testing.T) {
		first, second := e.user(2), e.user(2)
		a := startCore(t, bin, server.URL)
		a.login(first.Email, presencePassword)
		if s := a.selectOld(t, full.ID); s.ActionError != nil || !s.ConfigReady {
			t.Fatalf("first on the single server: %+v", s.ActionError)
		}
		b := startCore(t, bin, server.URL)
		b.login(second.Email, presencePassword)
		s := b.selectOld(t, full.ID)
		if s.ActionError == nil || s.ActionError.Code != "group_full" || s.ActionError.Message == "" || s.ConfigReady {
			t.Fatalf("second on the full server: %+v ready=%v", s.ActionError, s.ConfigReady)
		}
		t.Logf("released Core shows group_full as %q", s.ActionError.Message)
	})
}
