package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// These tests run the REAL node agent binary (with its reference VLESS forwarder)
// against the REAL API and PostgreSQL over real mTLS, on loopback. They prove, with
// no client cooperation at all, that the node alone decides who may use it and how
// much they used (handoff_app.md section 9: B04, B05, B07).

type pki struct {
	caPEM, caKeyless []byte
	ca               *x509.Certificate
	caKey            *ecdsa.PrivateKey
}

func newPKI(t *testing.T) *pki {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	return &pki{caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), ca: ca, caKey: key}
}

// issue returns a certificate chained to the CA plus its PEM key and cert.
func (p *pki) issue(t *testing.T, cn string, usage x509.ExtKeyUsage, ips ...net.IP) (cert tls.Certificate, certPEM, keyPEM []byte, leaf *x509.Certificate) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{usage}, KeyUsage: x509.KeyUsageDigitalSignature, IPAddresses: ips}
	der, err := x509.CreateCertificate(rand.Reader, template, p.ca, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ = x509.ParseCertificate(der)
	return
}

// controlPlane is the agent API on a real mTLS listener that can be stopped and
// restarted on the same address.
type controlPlane struct {
	t      *testing.T
	addr   string
	cfg    *tls.Config
	handle http.Handler
	srv    *http.Server
}

func (c *controlPlane) start() {
	c.t.Helper()
	ln, err := net.Listen("tcp", c.addr)
	if err != nil {
		c.t.Fatal(err)
	}
	c.srv = &http.Server{Handler: c.handle, TLSConfig: c.cfg, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = c.srv.ServeTLS(ln, "", "") }()
}

func (c *controlPlane) stop() {
	c.t.Helper()
	if c.srv != nil {
		_ = c.srv.Close()
		c.srv = nil
	}
}

type e2eNode struct {
	env       *grantEnv
	pki       *pki
	cp        *controlPlane
	node      model.Node
	agentID   string
	vless     string
	dir       string
	cmd       *exec.Cmd
	statePath string
	echo      net.Addr
}

func startEcho(t *testing.T) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return ln.Addr()
}

func newE2E(t *testing.T, usageSeconds string, mutate func(*config.Config)) *e2eNode {
	t.Helper()
	if os.Getenv("ASTERLINK_NODEAGENT_BIN") == "" {
		t.Skip("run scripts/test-native.py: it builds the node agent binary")
	}
	e := newGrantEnv(t, func(c *config.Config) {
		c.ClientLease = 4 * time.Second
		c.GrantBudgetBytes = 64 << 20
		if mutate != nil {
			mutate(c)
		}
	})
	p := newPKI(t)
	addr := freeAddr(t)
	serverCert, _, _, _ := p.issue(t, "control-plane", x509.ExtKeyUsageServerAuth, net.ParseIP("127.0.0.1"))
	pool := x509.NewCertPool()
	pool.AddCert(p.ca)
	n := &e2eNode{env: e, pki: p, dir: t.TempDir(), echo: startEcho(t)}
	n.cp = &controlPlane{t: t, addr: addr, handle: e.agent, cfg: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}}
	n.cp.start()
	t.Cleanup(n.cp.stop)

	_, certPEM, keyPEM, leaf := p.issue(t, "node-agent", x509.ExtKeyUsageClientAuth)
	n.write("client.pem", certPEM)
	n.write("client.key", keyPEM)
	n.write("ca.pem", p.caPEM)
	sum := sha256.Sum256(leaf.Raw)
	n.node = e.addNode("E2E-Node", 500, true)
	row := model.NodeAgent{ID: newUUID(), NodeID: n.node.ID, Name: "e2e", CertFingerprint: hex.EncodeToString(sum[:]), Enabled: true, Protocols: "[]"}
	if err := e.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	n.agentID, n.vless, n.statePath = row.ID, freeAddr(t), filepath.Join(n.dir, "state.json")
	n.startAgent(usageSeconds)
	t.Cleanup(n.killAgent)
	return n
}

func (n *e2eNode) write(name string, data []byte) {
	n.env.t.Helper()
	if err := os.WriteFile(filepath.Join(n.dir, name), data, 0o600); err != nil {
		n.env.t.Fatal(err)
	}
}

func (n *e2eNode) startAgent(usageSeconds string) {
	t := n.env.t
	t.Helper()
	cmd := exec.Command(os.Getenv("ASTERLINK_NODEAGENT_BIN"))
	cmd.Env = []string{
		"NODEAGENT_API_URL=https://" + n.cp.addr, "NODEAGENT_CLIENT_CERT_FILE=" + filepath.Join(n.dir, "client.pem"), "NODEAGENT_CLIENT_KEY_FILE=" + filepath.Join(n.dir, "client.key"),
		"NODEAGENT_SERVER_CA_FILE=" + filepath.Join(n.dir, "ca.pem"), "NODEAGENT_STATE_FILE=" + n.statePath, "NODEAGENT_VLESS_LISTEN=" + n.vless,
		"NODEAGENT_ALLOW_PLAINTEXT=true", "NODEAGENT_ALLOW_PRIVATE_TARGETS=true", "NODEAGENT_SYNC_SECONDS=1", "NODEAGENT_USAGE_SECONDS=" + usageSeconds,
	}
	logFile, _ := os.Create(filepath.Join(n.dir, "agent.log"))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	n.cmd = cmd
	// The listener is up once the port accepts; the agent is up once the API saw it.
	eventually(t, 10*time.Second, "agent to come online", func() bool {
		var a model.NodeAgent
		if n.env.db.First(&a, "id = ?", n.agentID).Error != nil || a.LastSeenAt == nil {
			return false
		}
		return time.Since(*a.LastSeenAt) < 3*time.Second && strings.Contains(a.Protocols, "vless")
	})
}

func (n *e2eNode) killAgent() {
	if n.cmd != nil && n.cmd.Process != nil {
		_ = n.cmd.Process.Signal(syscall.SIGKILL)
		_, _ = n.cmd.Process.Wait()
		n.cmd = nil
	}
}

func (n *e2eNode) agentLog() string {
	data, _ := os.ReadFile(filepath.Join(n.dir, "agent.log"))
	return string(data)
}

func eventually(t *testing.T, within time.Duration, what string, ok func() bool) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < within {
		if ok() {
			return time.Since(start)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
	return 0
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return hex.EncodeToString(b[:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:])
}

var uuidInYAML = regexp.MustCompile(`uuid: ([0-9a-f-]{36})`)

// readyCredential waits for a grant to become ready and returns its UUID, taken only
// from the document a real client would receive.
func (n *e2eNode) readyCredential(token, grant string) string {
	t := n.env.t
	t.Helper()
	var uuid string
	eventually(t, 10*time.Second, "grant to become ready", func() bool {
		w := n.env.client("GET", "/grants/"+grant, token, nil, nil)
		if w.Code != 200 {
			return false
		}
		var out struct {
			YAML string `json:"yaml"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if m := uuidInYAML.FindStringSubmatch(out.YAML); m != nil {
			uuid = m[1]
		}
		return uuid != ""
	})
	return uuid
}

func (n *e2eNode) dial(uuid string) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", n.vless, 3*time.Second)
	if err != nil {
		return nil, err
	}
	raw, _ := hex.DecodeString(strings.ReplaceAll(uuid, "-", ""))
	host, port, _ := net.SplitHostPort(n.echo.String())
	p, _ := strconv.Atoi(port)
	req := append([]byte{0}, raw...)
	req = append(req, 0, 1)
	req = binary.BigEndian.AppendUint16(req, uint16(p))
	req = append(append(req, 1), net.ParseIP(host).To4()...)
	if _, err = c.Write(req); err != nil {
		c.Close()
		return nil, err
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err = io.ReadFull(c, make([]byte, 2)); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func roundTripBytes(c net.Conn, n int) error {
	if _, err := c.Write(make([]byte, n)); err != nil {
		return err
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := io.ReadFull(c, make([]byte, n))
	return err
}

func isDead(c net.Conn, within time.Duration) bool {
	_ = c.SetReadDeadline(time.Now().Add(within))
	_, err := c.Read(make([]byte, 1))
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return false
	}
	return err != nil
}

func TestNodeAgentEndToEndPostgres(t *testing.T) {
	n := newE2E(t, "1", nil)
	e := n.env
	u := e.newUser(1<<30, 3)
	token := e.login(u)

	w := e.createGrant(token, n.node.ID, "key-e2e-0000001")
	if w.Code != 202 {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	id := grantID(t, w)
	uuid := n.readyCredential(token, id)
	if uuid == sharedVlessUUID {
		t.Fatal("the shared administrator credential was delivered")
	}

	// The shared credential from the administrator's YAML is useless on this node.
	if c, err := n.dial(sharedVlessUUID); err == nil {
		c.Close()
		t.Fatal("shared credential accepted by the node")
	}

	// B04: traffic is billed with zero client reports (no /traffic call is ever made).
	c, err := n.dial(uuid)
	if err != nil {
		t.Fatalf("dial: %v\n%s", err, n.agentLog())
	}
	defer c.Close()
	const chunk = 200 * 1024
	if err = roundTripBytes(c, chunk); err != nil {
		t.Fatal(err)
	}
	want := int64(2*chunk) * 500
	took := eventually(t, 10*time.Second, "node usage to be billed", func() bool { return e.subscription(u.subID).UsedUnits == want })
	t.Logf("node-reported usage billed %v after the transfer: %d units for %d payload bytes at 0.5x", took, want, 2*chunk)
	if sub := e.subscription(u.subID); sub.UploadBytes != chunk || sub.DownloadBytes != chunk {
		t.Fatalf("directions: %+v", sub)
	}
	if st := e.client("GET", "/session", token, nil, nil); !strings.Contains(st.Body.String(), `"metering_source":"node_reported"`) {
		t.Fatalf("session: %s", st.Body.String())
	}

	// B05: the extracted credential stops working at once, on established and new flows.
	other, err := n.dial(uuid)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err = roundTripBytes(other, 1000); err != nil {
		t.Fatal(err)
	}
	revoked := time.Now()
	if rev := e.client("POST", "/grants/"+id+"/revoke", token, nil, struct{}{}); rev.Code != 200 {
		t.Fatalf("revoke: %d", rev.Code)
	}
	for _, flow := range []net.Conn{c, other} {
		for !isDead(flow, 200*time.Millisecond) {
			if time.Since(revoked) > 8*time.Second {
				t.Fatal("established flow survived revocation")
			}
		}
	}
	t.Logf("revocation reached the node and killed established flows after %v", time.Since(revoked))
	if time.Since(revoked) > 5*time.Second {
		t.Fatalf("revocation took %v (target <= 5s)", time.Since(revoked))
	}
	if fresh, err := n.dial(uuid); err == nil {
		fresh.Close()
		t.Fatal("revoked credential opened a new connection")
	}
	// Everything carried is accounted, including the tail after the last periodic report.
	finalUnits := int64(2*chunk+2*1000) * 500
	eventually(t, 10*time.Second, "revocation to settle with the final cursor", func() bool {
		g := e.grant(id)
		return g.State == "revoked" && g.ReleasedAt != nil && e.subscription(u.subID).UsedUnits == finalUnits
	})
	if g := e.grant(id); g.NeedsReconcile || g.FinalEpoch != g.Epoch {
		t.Fatalf("clean revocation flagged for reconciliation: %+v", g)
	}
	if p := e.period(u.subID); p.ReservedUnits != 0 {
		t.Fatalf("reservation not returned: %d", p.ReservedUnits)
	}
}

func TestNodeEnforcesLeaseWhenControlPlaneVanishesPostgres(t *testing.T) {
	n := newE2E(t, "1", nil)
	e := n.env
	u := e.newUser(1<<30, 3)
	token := e.login(u)
	id := grantID(t, e.createGrant(token, n.node.ID, "key-e2e-lease-01"))
	uuid := n.readyCredential(token, id)
	c, err := n.dial(uuid)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = roundTripBytes(c, 1000); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "usage reported", func() bool { return e.subscription(u.subID).UsedUnits == 2000*500 })

	// B07: the API / network disappears. The node must stop on its own at the lease deadline.
	n.cp.stop()
	cut := time.Now()
	for !isDead(c, 200*time.Millisecond) {
		if time.Since(cut) > 12*time.Second {
			t.Fatalf("flow still alive %v after losing the control plane (lease 4s)\n%s", time.Since(cut), n.agentLog())
		}
	}
	t.Logf("node closed the flow %v after the control plane vanished (lease 4s)", time.Since(cut))
	if fresh, err := n.dial(uuid); err == nil {
		fresh.Close()
		t.Fatal("expired lease opened a new connection")
	}
	// Traffic that happened while offline is reported once the control plane returns.
	n.cp.start()
	eventually(t, 10*time.Second, "the grant to be served again after reconnecting", func() bool {
		again, err := n.dial(uuid)
		if err != nil {
			return false
		}
		defer again.Close()
		return roundTripBytes(again, 500) == nil
	})
	eventually(t, 10*time.Second, "cumulative counters to continue across the outage", func() bool {
		return e.subscription(u.subID).UsedUnits == int64(2000+1000)*500
	})
}

func TestNodeAgentCrashRecoveryDoesNotLoseCheckpointedUsagePostgres(t *testing.T) {
	// Reports every 30s: the bytes below are only in the agent's checkpoint when it dies.
	n := newE2E(t, "30", nil)
	e := n.env
	u := e.newUser(1<<30, 3)
	token := e.login(u)
	id := grantID(t, e.createGrant(token, n.node.ID, "key-e2e-crash-01"))
	uuid := n.readyCredential(token, id)
	c, err := n.dial(uuid)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// The first periodic report happens at start; wait for it so the next bytes are fresh.
	time.Sleep(1500 * time.Millisecond)
	if err = roundTripBytes(c, 64*1024); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1800 * time.Millisecond) // the 1s checkpoint tick persists the counters
	if used := e.subscription(u.subID).UsedUnits; used != 0 {
		t.Fatalf("test premise broken: %d units already reported", used)
	}
	n.killAgent() // SIGKILL: no graceful shutdown, no final report
	if !isDead(c, 2*time.Second) {
		t.Fatal("flow survived the agent")
	}
	n.startAgent("30")
	want := int64(2*64*1024) * 500
	eventually(t, 15*time.Second, "the previous epoch to be closed from the checkpoint", func() bool {
		g := e.grant(id)
		return g.FinalEpoch != 0 && e.subscription(u.subID).UsedUnits == want
	})
	g := e.grant(id)
	if g.NeedsReconcile {
		t.Fatalf("a recovered epoch must close cleanly: %+v", g)
	}
	t.Logf("restarted agent reported the old epoch's checkpoint (%d bytes) as its final word; log:\n%s", g.ChargedBytes, n.agentLog())
	// The recovered grant is finished; the user gets a new one and billing continues on the new epoch.
	eventually(t, 10*time.Second, "the finished grant to be retired", func() bool {
		s := e.grant(id).State
		return s == "revoked" || s == "expired" || s == "revoking"
	})
	if rev := e.client("POST", "/grants/"+id+"/revoke", token, nil, struct{}{}); rev.Code != 200 {
		t.Fatalf("revoke after crash: %d", rev.Code)
	}
	id2 := grantID(t, e.createGrant(token, n.node.ID, "key-e2e-crash-02"))
	uuid2 := n.readyCredential(token, id2)
	c2, err := n.dial(uuid2)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err = roundTripBytes(c2, 1000); err != nil {
		t.Fatal(err)
	}
	// Bytes carried since the last 1s checkpoint are the documented loss window of a
	// hard kill, so let the checkpoint land before killing.
	time.Sleep(1500 * time.Millisecond)
	n.killAgent()
	n.startAgent("1")
	eventually(t, 15*time.Second, "second epoch usage to be billed", func() bool {
		return e.subscription(u.subID).UsedUnits == want+int64(2000)*500
	})
}
