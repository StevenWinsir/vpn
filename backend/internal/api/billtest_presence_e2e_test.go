package api

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// The round-5 presence cases of the acceptance tool (asterlink-billtest --presence: P01
// device limit, P02 group full, P03 reassignment) run here against the REAL API and
// PostgreSQL. The test plays the operator: when the tool asks for the server to be
// withdrawn, it disables the server the tool's device is on through the admin API.
func TestBilltestPresenceAgainstTheRealAPIPostgres(t *testing.T) {
	bin := os.Getenv("ASTERLINK_BILLTEST_BIN")
	if bin == "" {
		t.Skip("run scripts/test-native.py: it builds the billtest binary")
	}
	e := newPresenceEnv(t, func(c *config.Config) {
		c.ClientReportInterval = 2 * time.Second
		c.Origins = []string{"http://127.0.0.1:3000"}
	})
	e.tick = time.Microsecond // successive leases of one session are ordered by time
	server := httptest.NewServer(e.router)
	defer server.Close()
	jp := e.group("JP1", "[]")
	e.servers(jp, 5, 5)
	full := e.group("Full1", "[]")
	e.servers(full, 1) // one slot: the second device must hear group_full
	u := e.user(5)     // effective limit min(5, 2) = 2
	_, adminCookie := e.admin()

	out := filepath.Join(t.TempDir(), "report")
	cmd := exec.Command(bin, "--presence", "--use-device-slots", "--output", out, "--device-limit", "2", "--line", "JP1", "--full-line", "Full1",
		"--reassign-wait-seconds", "30", "--reconnect-within-seconds", "15", "--max-seconds", "120")
	cmd.Env = []string{"ASTERLINK_BILL_API_BASE=" + server.URL + "/api/v1/client", "ASTERLINK_BILL_EMAIL=" + u.Email, "ASTERLINK_BILL_PASSWORD=" + presencePassword,
		"ASTERLINK_BILL_DEVICE_ID=6f9619ff-8b86-4d01-b42d-00cf4fc964ff"}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	var mu sync.Mutex
	withdrawn := ""
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		log.WriteString(line + "\n")
		if !strings.Contains(line, "PRESENCE-REASSIGN-READY") {
			continue
		}
		// Exactly one device of the account is online now: withdraw its server.
		var lease model.ConnectionLease
		if err := e.db.Where("user_id = ? AND state = ? AND group_id = ?", u.ID, model.LeaseActive, jp.ID).Take(&lease).Error; err != nil {
			t.Errorf("no lease to withdraw: %v", err)
			continue
		}
		mu.Lock()
		withdrawn = lease.NodeID
		mu.Unlock()
		e.want(e.editServer(adminCookie, e.nodeRow(lease.NodeID), "", 5, "", false), 200, "")
	}
	_, _ = io.Copy(io.Discard, stdout)
	runErr := cmd.Wait()
	t.Logf("billtest --presence output:\n%s", log.String())
	data, err := os.ReadFile(filepath.Join(out, "billing-report.json"))
	if err != nil {
		t.Fatalf("no report (%v, run error %v)", err, runErr)
	}
	var report billReport
	if err = json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range report.Cases {
		got[c.ID] = c.Result
	}
	for _, id := range []string{"P-LOGIN", "P01-admit", "P01-refuse", "P01-release", "P02-group-full", "P03-reassign"} {
		if got[id] != "pass" {
			t.Errorf("%s = %q, want pass", id, got[id])
		}
	}
	if withdrawn == "" || runErr != nil {
		t.Fatalf("withdrawn %q, run error %v", withdrawn, runErr)
	}
	// The tool released everything it took: no device of the account is online afterwards.
	if n := e.live(u.ID); n != 0 {
		t.Fatalf("%d leases left online after the tool exited", n)
	}
}
