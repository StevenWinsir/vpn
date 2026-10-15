package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

// These tests run the round-7 acceptance tool (native/core/cmd/asterlink-billtest --grants) against the REAL API,
// PostgreSQL, mTLS and node agent binary on loopback, with the real traffic sink as the controlled target. The tool
// is what the Windows tester will run against a managed node; here it proves the tool itself against genuine components
// rather than only against its own fake, and that the controls it judges are in fact enforced by the node.

type sinkProc struct {
	http, udp string
}

func startSink(t *testing.T, token string) sinkProc {
	t.Helper()
	bin := os.Getenv("ASTERLINK_TRAFFICSINK_BIN")
	if bin == "" {
		t.Skip("run scripts/test-native.py: it builds the traffic sink and billtest binaries")
	}
	pick := func(network string) string {
		var addr string
		if network == "tcp" {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr = ln.Addr().String()
			ln.Close()
		} else {
			pc, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr = pc.LocalAddr().String()
			pc.Close()
		}
		return addr
	}
	s := sinkProc{http: pick("tcp"), udp: pick("udp")}
	cmd := exec.Command(bin, "--listen", s.http, "--udp-listen", s.udp, "--max-mib", "64")
	cmd.Env = []string{"ASTERLINK_SINK_TOKEN=" + token}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	eventually(t, 10*time.Second, "the sink to listen", func() bool {
		c, err := net.DialTimeout("tcp", s.http, 200*time.Millisecond)
		if err == nil {
			c.Close()
		}
		return err == nil
	})
	return s
}

// pointNodeAtForwarder rewrites the node's stored document so the grant document names the real forwarder's address, which is
// what an administrator enters in the node form.
func (n *e2eNode) pointNodeAtForwarder() {
	t := n.env.t
	t.Helper()
	host, port, _ := net.SplitHostPort(n.vless)
	number, _ := strconv.Atoi(port)
	yaml := fmt.Sprintf("proxies:\n  - {name: %s, type: vless, server: %s, port: %d, uuid: %s, udp: true}\n", n.node.Name, host, number, sharedVlessUUID)
	sealed, err := nodes.Encrypt(n.env.cfg.NodeEncryptionKey, n.node.ID, []byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if err = n.env.db.Model(&model.NodeConfig{}).Where("node_id = ?", n.node.ID).Update("ciphertext", sealed).Error; err != nil {
		t.Fatal(err)
	}
}

type billReport struct {
	Cases []struct {
		ID       string `json:"case_id"`
		Observed string `json:"observed"`
		Result   string `json:"result"`
	} `json:"cases"`
}

func runBilltest(t *testing.T, apiURL, email string, sink sinkProc, token string, extra ...string) map[string]string {
	t.Helper()
	bin := os.Getenv("ASTERLINK_BILLTEST_BIN")
	if bin == "" {
		t.Skip("run scripts/test-native.py: it builds the billtest binary")
	}
	out := filepath.Join(t.TempDir(), "report")
	args := append([]string{"--grants", "--spend-test-quota", "--output", out, "--udp-sink", sink.udp, "--cut-within-seconds", "8"}, extra...)
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{
		"ASTERLINK_BILL_API_BASE=" + apiURL + "/api/v1/client", "ASTERLINK_BILL_EMAIL=" + email, "ASTERLINK_BILL_PASSWORD=Native-Test-Password!",
		"ASTERLINK_BILL_SINK=http://" + sink.http, "ASTERLINK_BILL_SINK_TOKEN=" + token,
	}
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	runErr := cmd.Run()
	t.Logf("billtest output:\n%s", log.String())
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
		if c.Result == "fail" {
			t.Errorf("%s FAILED: %s", c.ID, c.Observed)
		}
	}
	return got
}

func TestBilltestGrantsAgainstTheRealNodeAgentPostgres(t *testing.T) {
	const sinkToken = "e2e-sink-token-0123456789"
	sink := startSink(t, sinkToken)
	n := newE2E(t, "1", func(c *config.Config) {
		c.ClientReportInterval = 2 * time.Second
		c.ClientLease = 6 * time.Second
		c.ClientIdleTimeout = 14 * time.Second
	})
	n.pointNodeAtForwarder()
	server := httptest.NewServer(n.env.router)
	defer server.Close()

	// A user with room for the controlled transfer: independent client, no report, node billing, revocation, idle expiry.
	u := n.env.newUser(512<<20, 8)
	got := runBilltest(t, server.URL, u.Email, sink, sinkToken, "--idle-expiry", "--idle-wait-seconds", "60")
	for _, id := range []string{"G-CAPS", "G-NODES", "B04-reject", "B04-node-bills", "B05-revoke", "B05-idle-expiry"} {
		if got[id] != "pass" {
			t.Errorf("%s = %q, want pass\n%s", id, got[id], n.agentLog())
		}
	}
}

func TestBilltestRaceAndOperatorCutAgainstTheRealNodeAgentPostgres(t *testing.T) {
	const sinkToken = "e2e-sink-token-0123456789"
	sink := startSink(t, sinkToken)
	n := newE2E(t, "1", func(c *config.Config) {
		c.ClientReportInterval = 2 * time.Second
		c.ClientLease = 6 * time.Second
		c.ClientIdleTimeout = 30 * time.Second
	})
	n.pointNodeAtForwarder()
	server := httptest.NewServer(n.env.router)
	defer server.Close()

	// B06: a few devices contend for the last bytes of a small quota; the account must never be oversold.
	u := n.env.newUser(6<<20, 8)
	got := runBilltest(t, server.URL, u.Email, sink, sinkToken, "--upload-mib", "1", "--download-mib", "1", "--race-sessions", "3", "--race-max-mib", "64")
	if got["B06-race"] != "pass" {
		t.Errorf("B06-race = %q, want pass", got["B06-race"])
	}

	// B05 (operator): the subscription ends while flows are open; the node must cut them.
	u2 := n.env.newUser(512<<20, 8)
	go func() {
		// The tool runs B04 (two grants), B05-revoke (one) and then opens the operator case's flows on the fourth grant.
		for {
			var count int64
			n.env.db.Model(&model.ConnectionGrant{}).Where("user_id = ? AND state = ?", u2.ID, "ready").Count(&count)
			var all int64
			n.env.db.Model(&model.ConnectionGrant{}).Where("user_id = ?", u2.ID).Count(&all)
			if all >= 4 && count >= 1 {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(3 * time.Second)
		n.env.db.Model(&model.Subscription{}).Where("id = ?", u2.subID).Update("expires_at", time.Now().Add(-time.Minute).UTC())
	}()
	got = runBilltest(t, server.URL, u2.Email, sink, sinkToken, "--hold-seconds", "40", "--hold-case", "B05-expire")
	if got["B05-expire"] != "pass" {
		t.Errorf("B05-expire = %q, want pass\n%s", got["B05-expire"], n.agentLog())
	}
}
