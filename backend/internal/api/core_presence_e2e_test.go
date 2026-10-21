package api

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// Round 5 of the presence plan (handoff_node_use_history_and_load_balancing_c.md 12): the
// real Core binary (fake engine, no root) against the real API and PostgreSQL. The server
// runs on the presence clock, so lease expiry is reached by advancing it, never by waiting
// 90 real seconds. Each Core is a separate device of one account.

func (c *coreProc) selectLine(t *testing.T, id string) {
	t.Helper()
	snap, _ := c.waitSnapshot("catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) > 0 })
	if reply, s := c.call("select_node", c.gen, map[string]string{"node_id": id, "catalog_revision": snap.CatalogRevision}); !reply.OK || s.ActionError != nil || !s.ConfigReady {
		t.Fatalf("select: %+v %+v", reply.Error, s.ActionError)
	}
}

func (c *coreProc) connectOK(t *testing.T) snapshotView {
	t.Helper()
	_, s := c.call("connect", c.gen, nil)
	if s.ActionError != nil || s.State != "connected" {
		t.Fatalf("connect: %+v %+v", s.ActionError, s.LastError)
	}
	return s
}

// activeLease is the device's lease that admission would count, if any.
func (e *presenceEnv) activeLease(device string) *model.ConnectionLease {
	var l model.ConnectionLease
	if e.db.Where("device_id = ? AND state = ?", device, model.LeaseActive).Take(&l).Error != nil {
		return nil
	}
	return &l
}

func TestCoreOnlineLeasesEndToEnd(t *testing.T) {
	bin := os.Getenv("ASTERLINK_CORE_BIN")
	if bin == "" {
		t.Skip("set ASTERLINK_CORE_BIN (built by scripts/test-native.py)")
	}
	e := newPresenceEnv(t, func(c *config.Config) {
		c.ClientReportInterval = 2 * time.Second // reports (and so renewals) every 2 s of real time
		c.Origins = []string{"http://127.0.0.1:3000"}
	})
	e.tick = time.Microsecond // successive leases of one session are ordered by time
	server := httptest.NewServer(e.router)
	defer server.Close()
	g := e.group("JP1", "[]")
	servers := e.servers(g, 5, 5)
	u := e.user(5) // D1: min(plan 5, CLIENT_MAX_ONLINE_DEVICES 2) = 2 online devices
	_, adminCookie := e.admin()

	cores := []*coreProc{}
	for range 3 {
		c := startCore(t, bin, server.URL)
		c.login(u.Email, presencePassword)
		c.selectLine(t, g.ID)
		cores = append(cores, c)
	}
	if n := e.live(u.ID); n != 0 {
		t.Fatalf("signing in and selecting took %d slots", n)
	}

	t.Run("two devices online, the third refused", func(t *testing.T) {
		cores[0].connectOK(t)
		cores[1].connectOK(t)
		_, s := cores[2].call("connect", cores[2].gen, nil)
		if s.ActionError == nil || s.ActionError.Code != "device_limit" || s.ActionError.Message != "已达到同时在线设备上限，请先断开其他设备" || s.State == "connected" {
			t.Fatalf("third device: %+v %s", s.ActionError, s.State)
		}
		if n := e.live(u.ID); n != 2 {
			t.Fatalf("live leases %d", n)
		}
		// The proxy name every device sees is the group's, wherever it was placed.
		for _, c := range cores[:2] {
			if l := e.activeLease(c.device); l == nil || l.GroupID != g.ID {
				t.Fatalf("lease of %s: %+v", c.device, l)
			}
		}
	})

	t.Run("a disconnect is visible to the backend at once", func(t *testing.T) {
		off := time.Now()
		if _, s := cores[0].call("disconnect", 0, nil); s.State != "ready" || s.LastError != nil {
			t.Fatalf("disconnect: %s %+v", s.State, s.LastError)
		}
		if n := e.live(u.ID); n != 1 {
			t.Fatalf("live leases right after the disconnect reply: %d", n)
		}
		var ended model.ConnectionLease
		e.db.Where("device_id = ?", cores[0].device).Order("acquired_at DESC").Take(&ended)
		if ended.State != model.LeaseEnded || ended.EndReason != model.LeaseReleased {
			t.Fatalf("released lease: %+v", ended)
		}
		devices := e.webGet(e.web(u), "/me/devices")
		if devices["online_count"] != float64(1) {
			t.Fatalf("/me/devices: %v", devices)
		}
		t.Logf("offline on the website %v after the disconnect command", time.Since(off).Round(time.Millisecond))
		cores[2].connectOK(t) // the slot is free immediately
	})

	t.Run("a killed Core's slot returns after the lease (90 s, injected clock)", func(t *testing.T) {
		killed := cores[1]
		if err := killed.cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_, _ = killed.cmd.Process.Wait()
		if _, s := cores[0].call("connect", cores[0].gen, nil); s.ActionError == nil || s.ActionError.Code != "device_limit" {
			t.Fatalf("slot freed before the lease lapsed: %+v", s.ActionError)
		}
		// 89 s later the killed device still counts; the live one keeps renewing.
		e.advance(89 * time.Second)
		renewedBy := e.now()
		eventually(t, 10*time.Second, "the live device to renew on the new clock", func() bool {
			l := e.activeLease(cores[2].device)
			return l != nil && !l.RenewedAt.Before(renewedBy)
		})
		if n := e.live(u.ID); n != 2 {
			t.Fatalf("at +89 s: %d live", n)
		}
		e.advance(2 * time.Second)
		if n := e.live(u.ID); n != 1 {
			t.Fatalf("at +91 s: %d live", n)
		}
		cores[0].connectOK(t)
	})

	t.Run("a withdrawn server moves the device by itself with a continuous ledger", func(t *testing.T) {
		mover := cores[0]
		before := e.activeLease(mover.device)
		if before == nil {
			t.Fatal("no lease")
		}
		mover.carry(3<<20, 9<<20)
		eventually(t, 10*time.Second, "the bytes to be reported", func() bool {
			var sess model.NativeSession
			e.db.Take(&sess, "device_id = ? AND revoked_at IS NULL", mover.device)
			return sess.DownloadBytes == 9<<20
		})
		var withdrawn model.Node
		for _, s := range servers {
			if s.ID == before.NodeID {
				withdrawn = s
			}
		}
		started := time.Now()
		e.want(e.editServer(adminCookie, withdrawn, "", 5, "", false), 200, "")
		var after *model.ConnectionLease
		eventually(t, 15*time.Second, "the automatic reconnect to the other server", func() bool {
			after = e.activeLease(mover.device)
			return after != nil && after.ID != before.ID && mover.snapshot().State == "connected"
		})
		t.Logf("moved to another server %v after the admin disabled the first", time.Since(started).Round(time.Millisecond))
		if after.NodeID == withdrawn.ID || after.GroupID != g.ID {
			t.Fatalf("reconnected to %s (withdrawn %s)", after.NodeID, withdrawn.ID)
		}
		var old model.ConnectionLease
		e.db.Take(&old, "id = ?", before.ID)
		if old.State != model.LeaseEnded || old.EndReason != model.LeaseNodeUnavailable {
			t.Fatalf("withdrawn lease: %+v", old)
		}
		s := mover.snapshot()
		if s.LastError != nil || s.SelectedNodeID != g.ID {
			t.Fatalf("after the move: %+v %s", s.LastError, s.SelectedNodeID)
		}
		mover.carry(5<<20, 20<<20) // 2 MiB up, 11 MiB down more through the new server
		if _, off := mover.call("disconnect", 0, nil); off.State != "ready" || off.Metering == nil || off.Metering.Pending || off.LastError != nil {
			t.Fatalf("disconnect: %+v", off)
		}
		var sess model.NativeSession
		e.db.Take(&sess, "device_id = ? AND revoked_at IS NULL", mover.device)
		var reports []model.ClientTrafficReport
		e.db.Where("session_id = ?", sess.ID).Order("sequence").Find(&reports)
		var charged int64
		for i, r := range reports {
			if r.Sequence != int64(i+1) {
				t.Fatalf("sequence gap at %d: %+v", i, r)
			}
			charged += r.ChargedUnits
		}
		if sess.UploadBytes != 5<<20 || sess.DownloadBytes != 20<<20 || charged != (25<<20)*1000 {
			t.Fatalf("ledger: up %d down %d charged %d over %d reports", sess.UploadBytes, sess.DownloadBytes, charged, len(reports))
		}
		var buckets []model.UsageBucket
		e.db.Where("user_id = ?", u.ID).Find(&buckets)
		var up, down, units int64
		for _, b := range buckets {
			up, down, units = up+b.UploadBytes, down+b.DownloadBytes, units+b.ChargedUnits
		}
		var sub model.Subscription
		e.db.Take(&sub, "user_id = ?", u.ID)
		if up != 5<<20 || down != 20<<20 || units != sub.UsedUnits {
			t.Fatalf("history %d/%d/%d, ledger %d units", up, down, units, sub.UsedUnits)
		}
		history := e.webGet(e.web(u), "/me/usage-history")
		if history["bucket_seconds"] != float64(10800) {
			t.Fatalf("usage history: %v", history)
		}
	})

	for _, c := range cores {
		if c.cmd.ProcessState == nil {
			for _, secret := range []string{"group-secret", "cipher", presencePassword} {
				if strings.Contains(c.all.String(), secret) {
					t.Fatalf("Core output leaked %q", secret)
				}
			}
		}
	}
}
