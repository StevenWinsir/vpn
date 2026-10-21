package api

import (
	"net/http/httptest"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"

	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// Node groups round 6 (handoff_node_use_history_and_load_balancing_c.md 10.2): the desktop
// apps end the Core worker in three ways, and each must release the online slot at once
// rather than leave it to lapse:
//   - the IPC "shutdown" command: the macOS Helper's stopWorker (quitting the app);
//   - the command pipe closing: the Windows service's CoreHost.StopAsync (service stop,
//     system shutdown) and the macOS Helper's teardown;
//   - SIGTERM: the Helper's terminate() after its grace period, a logout or a system shutdown
//     (on Windows a console control event arrives the same way).
//
// The real Core binary (fake engine) against the real API; the clock is frozen, so a lease
// that ended here was released, never lapsed.
func TestCoreExitPathsReleaseTheSlot(t *testing.T) {
	bin := os.Getenv("ASTERLINK_CORE_BIN")
	if bin == "" {
		t.Skip("set ASTERLINK_CORE_BIN (built by scripts/test-native.py)")
	}
	e := newPresenceEnv(t, func(c *config.Config) {
		c.ClientReportInterval = 2 * time.Second
		c.Origins = []string{"http://127.0.0.1:3000"}
	})
	server := httptest.NewServer(e.router)
	defer server.Close()
	g := e.group("Exit", "[]")
	e.servers(g, 5)
	u := e.user(5)

	for _, path := range []struct {
		name string
		end  func(c *coreProc)
	}{
		{"the shutdown command (macOS quit)", func(c *coreProc) { c.call("shutdown", 0, nil) }},
		{"the command pipe closing (Windows service stop)", func(c *coreProc) { _ = c.in.Close() }},
		{"SIGTERM (terminate, logout, system shutdown)", func(c *coreProc) {
			if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(path.name, func(t *testing.T) {
			if runtime.GOOS == "windows" && path.name[:3] == "SIG" {
				t.Skip("no SIGTERM delivery to a child on Windows")
			}
			c := startCore(t, bin, server.URL)
			c.login(u.Email, presencePassword)
			c.selectLine(t, g.ID)
			c.connectOK(t)
			if e.activeLease(c.device) == nil {
				t.Fatal("no online lease while connected")
			}
			began := time.Now()
			path.end(c)
			exited := make(chan error, 1)
			go func() { exited <- c.cmd.Wait() }()
			select {
			case <-exited:
			case <-time.After(15 * time.Second):
				_ = c.cmd.Process.Kill()
				t.Fatal("the Core did not exit within 15 s")
			}
			took := time.Since(began)
			if l := e.activeLease(c.device); l != nil {
				t.Fatalf("still online after the Core exited: %+v", l)
			}
			var ended model.ConnectionLease
			e.db.Where("device_id = ?", c.device).Order("acquired_at DESC").Take(&ended)
			if ended.State != model.LeaseEnded || ended.EndReason != model.LeaseReleased {
				t.Fatalf("lease after exit: %s %q", ended.State, ended.EndReason)
			}
			if n := e.live(u.ID); n != 0 {
				t.Fatalf("%d live leases left", n)
			}
			t.Logf("released and exited %v after the request", took.Round(time.Millisecond))
		})
	}
}
