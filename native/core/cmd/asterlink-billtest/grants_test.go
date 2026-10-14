package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asterlink/core/internal/apiclient"
)

const sinkSecret = "0123456789abcdef-sink-token"

// fakeNode is a miniature managed node plus the control plane that governs it: the grant API, a VLESS-speaking
// forwarder that bills per grant, and a toggle for each way a bad node could cheat. It exists so the harness's own
// verdicts are tested in both directions: a faithful node passes, a node that skips one duty fails that case.
type fakeNode struct {
	t *testing.T

	api  *httptest.Server
	ln   net.Listener
	sink *httptest.Server
	udp  net.PacketConn

	sinkDown, sinkUp atomic.Int64

	mu       sync.Mutex
	total    int64
	used     int64 // units
	rate     int64
	grants   map[string]*fakeGrant
	byUUID   map[string]*fakeGrant
	sessions map[string]bool // token -> bound to a grant
	logins   int

	// faults
	lax        bool // a revoked grant keeps its established flows
	freeRiders bool // the forwarder does not bill
	oversell   bool // the forwarder keeps going past the quota
	acceptsRep bool // /traffic is accepted on a node-reported session
	noManaged  bool // the catalog lists no node-governed line
}

type fakeGrant struct {
	id, uuid, token string
	revoked         bool
	conns           []net.Conn
}

func sixteen(n int) string { return fmt.Sprintf("%08x-0000-4000-8000-%012x", n, n) }

func newFakeNode(t *testing.T, total int64) *fakeNode {
	t.Helper()
	n := &fakeNode{t: t, total: total, rate: 500, grants: map[string]*fakeGrant{}, byUUID: map[string]*fakeGrant{}, sessions: map[string]bool{}}
	n.startSink()
	var err error
	if n.ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	go n.acceptLoop()
	n.startAPI()
	t.Cleanup(func() {
		n.api.Close()
		n.sink.Close()
		n.udp.Close()
		n.ln.Close()
		n.mu.Lock()
		for _, g := range n.grants {
			for _, c := range g.conns {
				c.Close()
			}
		}
		n.mu.Unlock()
	})
	return n
}

func (n *fakeNode) startSink() {
	mux := http.NewServeMux()
	mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) {
		size, _ := strconv.ParseInt(r.URL.Query().Get("bytes"), 10, 64)
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		block := make([]byte, 32<<10)
		for sent := int64(0); sent < size; {
			part := min(int64(len(block)), size-sent)
			written, err := w.Write(block[:part])
			n.sinkDown.Add(int64(written))
			if err != nil {
				return
			}
			sent += int64(written)
		}
	})
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.Copy(io.Discard, r.Body)
		n.sinkUp.Add(got)
		_, _ = io.WriteString(w, "{}")
	})
	n.sink = httptest.NewServer(mux)
	var err error
	if n.udp, err = net.ListenPacket("udp", "127.0.0.1:0"); err != nil {
		n.t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 2048)
		for {
			size, from, err := n.udp.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = n.udp.WriteTo(buf[:size], from)
		}
	}()
}

func (n *fakeNode) bill(bytes int64) (stop bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.freeRiders {
		n.used += bytes * n.rate
	}
	return !n.oversell && n.used >= n.total*1000
}

func (n *fakeNode) acceptLoop() {
	for {
		c, err := n.ln.Accept()
		if err != nil {
			return
		}
		go n.serve(c)
	}
}

func (n *fakeNode) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	head := make([]byte, 18)
	if _, err := io.ReadFull(br, head); err != nil {
		return
	}
	id := fmt.Sprintf("%x-%x-%x-%x-%x", head[1:5], head[5:7], head[7:9], head[9:11], head[11:17])
	rest := make([]byte, 4)
	if _, err := io.ReadFull(br, rest[:3]); err != nil {
		return
	}
	command, port := rest[0], int(binary.BigEndian.Uint16(rest[1:3]))
	kind, _ := br.ReadByte()
	if kind != 1 {
		return
	}
	ip := make([]byte, 4)
	if _, err := io.ReadFull(br, ip); err != nil {
		return
	}
	n.mu.Lock()
	g := n.byUUID[id]
	live := g != nil && !g.revoked && (n.oversell || n.used < n.total*1000)
	if live {
		g.conns = append(g.conns, c)
	}
	n.mu.Unlock()
	if !live {
		return
	}
	target := net.JoinHostPort(net.IP(ip).String(), strconv.Itoa(port))
	if command == 2 {
		n.relayUDP(c, br, target)
		return
	}
	up, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer up.Close()
	_, _ = c.Write([]byte{0, 0})
	done := make(chan struct{}, 2)
	pump := func(dst io.Writer, src io.Reader) {
		buf := make([]byte, 16<<10)
		for {
			size, err := src.Read(buf)
			if size > 0 {
				if _, werr := dst.Write(buf[:size]); werr != nil {
					break
				}
				if n.bill(int64(size)) {
					c.Close()
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go pump(up, br)
	go pump(c, up)
	<-done
}

func (n *fakeNode) relayUDP(c net.Conn, br *bufio.Reader, target string) {
	up, err := net.Dial("udp", target)
	if err != nil {
		return
	}
	defer up.Close()
	_, _ = c.Write([]byte{0, 0})
	for {
		var length [2]byte
		if _, err := io.ReadFull(br, length[:]); err != nil {
			return
		}
		payload := make([]byte, binary.BigEndian.Uint16(length[:]))
		if _, err := io.ReadFull(br, payload); err != nil {
			return
		}
		_, _ = up.Write(payload)
		_ = up.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 2048)
		size, err := up.Read(buf)
		if err != nil {
			continue
		}
		if n.bill(int64(len(payload) + size)) {
			return
		}
		_, _ = c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(size)), buf[:size]...))
	}
}

func (n *fakeNode) revoke(g *fakeGrant) {
	n.mu.Lock()
	defer n.mu.Unlock()
	g.revoked = true
	if n.lax {
		return
	}
	for _, c := range g.conns {
		c.Close()
	}
}

// endAll is the operator ending the plan or the node going away: every grant is dead on the node.
func (n *fakeNode) endAll() {
	n.mu.Lock()
	var list []*fakeGrant
	for _, g := range n.grants {
		list = append(list, g)
	}
	n.mu.Unlock()
	for _, g := range list {
		n.revoke(g)
	}
}

func (n *fakeNode) session(token string) apiclient.Session {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.sessionLocked(token)
}

func (n *fakeNode) sessionLocked(token string) apiclient.Session {
	return apiclient.Session{ServerTime: time.Now().UTC(), SessionID: "s-" + token[:8], SessionIdleTimeout: 180, TotalBytes: n.total, UsedUnits: n.used,
		RemainingBytes: max(0, (n.total*1000-n.used)/1000), MeteringSource: apiclient.MeteringNode, RatePermille: n.rate, CanConnect: true, LeaseSeconds: 90}
}

func (n *fakeNode) startAPI() {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	problem := func(w http.ResponseWriter, status int, code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":{"code":%q}}`, code)
	}
	token := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		n.mu.Lock()
		_, ok := n.sessions[tok]
		n.mu.Unlock()
		if !ok {
			problem(w, http.StatusUnauthorized, "invalid_credentials")
		}
		return tok, ok
	}
	p := "/api/v1/client"
	mux.HandleFunc(p+"/capabilities", func(w http.ResponseWriter, r *http.Request) {
		reply(w, apiclient.Capabilities{ContractVersions: []int{1, 2}, MeteringModes: []string{"client_reported", "node_reported"}})
	})
	mux.HandleFunc(p+"/login", func(w http.ResponseWriter, r *http.Request) {
		var in apiclient.LoginRequest
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Password != "pw" {
			problem(w, http.StatusUnauthorized, "invalid_credentials")
			return
		}
		n.mu.Lock()
		n.logins++
		tok := fmt.Sprintf("%043d", n.logins)
		n.sessions[tok] = false
		out := apiclient.LoginResponse{Token: tok, User: apiclient.User{ID: "u", Email: in.Email}, Session: n.sessionLocked(tok)}
		n.mu.Unlock()
		reply(w, out)
	})
	mux.HandleFunc(p+"/session", func(w http.ResponseWriter, r *http.Request) {
		if tok, ok := token(w, r); ok {
			reply(w, n.session(tok))
		}
	})
	mux.HandleFunc(p+"/nodes", func(w http.ResponseWriter, r *http.Request) {
		tok, ok := token(w, r)
		if !ok {
			return
		}
		source := apiclient.MeteringNode
		if n.noManaged {
			source = ""
		}
		reply(w, apiclient.Catalog{SessionID: "s-" + tok[:8], Revision: "r1", Nodes: []apiclient.NodeChoice{{ID: sixteen(1), Name: "Managed VLESS", Region: "HK", LineType: "dedicated", RatePermille: n.rate, Version: 1, MeteringSource: source}}})
	})
	mux.HandleFunc(p+"/config", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := token(w, r); ok {
			problem(w, http.StatusUpgradeRequired, "client_upgrade_required")
		}
	})
	mux.HandleFunc(p+"/traffic", func(w http.ResponseWriter, r *http.Request) {
		tok, ok := token(w, r)
		if !ok {
			return
		}
		n.mu.Lock()
		bound := n.sessions[tok]
		n.mu.Unlock()
		if bound && !n.acceptsRep {
			problem(w, http.StatusConflict, "metering_source_mismatch")
			return
		}
		n.mu.Lock()
		n.used += 1 << 20 * n.rate
		n.mu.Unlock()
		reply(w, map[string]any{"session": n.session(tok), "replayed": false})
	})
	mux.HandleFunc(p+"/logout", func(w http.ResponseWriter, r *http.Request) { reply(w, map[string]bool{"logged_out": true}) })
	mux.HandleFunc(p+"/grants", func(w http.ResponseWriter, r *http.Request) {
		tok, ok := token(w, r)
		if !ok {
			return
		}
		n.mu.Lock()
		if n.used >= n.total*1000 {
			n.mu.Unlock()
			problem(w, http.StatusForbidden, "quota_exhausted")
			return
		}
		id := sixteen(100 + len(n.grants))
		g := &fakeGrant{id: id, uuid: sixteen(500 + len(n.grants)), token: tok}
		n.grants[id] = g
		n.byUUID[g.uuid] = g
		n.sessions[tok] = true
		n.mu.Unlock()
		reply(w, n.grantStatus(g))
	})
	mux.HandleFunc(p+"/grants/", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := token(w, r); !ok {
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, p+"/grants/")
		id, action, _ := strings.Cut(rest, "/")
		n.mu.Lock()
		g := n.grants[id]
		n.mu.Unlock()
		if g == nil {
			problem(w, http.StatusNotFound, "grant_not_found")
			return
		}
		if action == "revoke" {
			n.revoke(g)
		}
		reply(w, n.grantStatus(g))
	})
	n.api = httptest.NewServer(mux)
}

func (n *fakeNode) grantStatus(g *fakeGrant) apiclient.GrantStatus {
	if g.revoked {
		return apiclient.GrantStatus{GrantID: g.id, State: apiclient.GrantRevoked, NodeID: sixteen(1)}
	}
	host, port, _ := net.SplitHostPort(n.ln.Addr().String())
	doc := "mode: rule\nproxies:\n    - name: Managed VLESS\n      type: vless\n      server: " + host + "\n      port: " + port + "\n      uuid: " + g.uuid + "\n      udp: true\n      tls: false\nproxy-groups:\n    - name: VPN\n      type: select\n      proxies:\n        - Managed VLESS\nrules:\n    - MATCH,VPN\n"
	session := n.session(g.token)
	return apiclient.GrantStatus{GrantID: g.id, State: apiclient.GrantReady, YAML: doc, Version: sha(doc), NodeID: sixteen(1), Session: &session}
}

func (n *fakeNode) run(t *testing.T, opt grantOptions) *recorder {
	t.Helper()
	base, err := apiclient.ParseBase(n.api.URL+"/api/v1/client", "development")
	if err != nil {
		t.Fatal(err)
	}
	api := apiclient.New(base)
	trace := newLossy()
	api.WrapTransport(trace.wrap)
	sinkURL, _ := url.Parse(n.sink.URL)
	port, _ := strconv.Atoi(sinkURL.Port())
	udpAddr := n.udp.LocalAddr().(*net.UDPAddr)
	var udp *destination
	if opt.udpSink != "" {
		udp = &destination{udpAddr.IP.String(), udpAddr.Port}
	}
	if opt.cutWithin == 0 {
		opt.cutWithin = 3 * time.Second
	}
	g := &grantRun{
		cfg: config{email: "test@test.com", password: "pw", deviceID: testDevice, upload: 1 << 20, download: 2 << 20, background: 64 << 10},
		opt: opt, rec: &recorder{}, poll: 50 * time.Millisecond, quiet: 600 * time.Millisecond, api: api, trace: trace, sink: destination{sinkURL.Hostname(), port}, udp: udp, token: sinkSecret, now: time.Now,
	}
	g.stats = func(context.Context) (sinkStats, error) { return n.sinkTotals(), nil }
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	g.run(ctx)
	for _, c := range g.rec.cases {
		t.Logf("%-16s %-7s %s", c.ID, c.Result, c.Observed)
	}
	return g.rec
}

func (n *fakeNode) sinkTotals() sinkStats {
	return sinkStats{DownSent: n.sinkDown.Load(), UpReceived: n.sinkUp.Load()}
}

func results(r *recorder) map[string]string {
	out := map[string]string{}
	for _, c := range r.cases {
		out[c.ID] = c.Result
	}
	return out
}

func TestFaithfulNodePassesTheEnforcementCases(t *testing.T) {
	n := newFakeNode(t, 64<<20)
	got := results(n.run(t, grantOptions{udpSink: "x"}))
	for _, id := range []string{"G-CAPS", "G-NODES", "B04-reject", "B04-node-bills", "B05-revoke"} {
		if got[id] != "pass" {
			t.Errorf("%s = %q, want pass", id, got[id])
		}
	}
}

func TestNodeThatKeepsFlowsAfterRevokeFails(t *testing.T) {
	n := newFakeNode(t, 64<<20)
	n.lax = true
	got := results(n.run(t, grantOptions{cutWithin: 2 * time.Second}))
	if got["B05-revoke"] != "fail" {
		t.Fatalf("B05-revoke = %q, want fail: a node that leaves established flows alive must not pass", got["B05-revoke"])
	}
}

func TestNodeThatDoesNotBillFails(t *testing.T) {
	n := newFakeNode(t, 64<<20)
	n.freeRiders = true
	if got := results(n.run(t, grantOptions{})); got["B04-node-bills"] != "fail" {
		t.Fatalf("B04-node-bills = %q, want fail", got["B04-node-bills"])
	}
}

func TestServerThatAcceptsClientReportsOnANodeSessionFails(t *testing.T) {
	n := newFakeNode(t, 64<<20)
	n.acceptsRep = true
	if got := results(n.run(t, grantOptions{})); got["B04-reject"] != "fail" {
		t.Fatalf("B04-reject = %q, want fail: /traffic must not bill a node-governed session", got["B04-reject"])
	}
}

func TestNoNodeGovernedLineIsBlockedNotPassed(t *testing.T) {
	n := newFakeNode(t, 64<<20)
	n.noManaged = true
	got := results(n.run(t, grantOptions{}))
	if got["G-NODES"] != "blocked" || len(got) != 2 {
		t.Fatalf("expected only G-CAPS pass and G-NODES blocked, got %v", got)
	}
}

func TestOperatorEndingThePlanMustCutEstablishedFlows(t *testing.T) {
	n := newFakeNode(t, 64<<20)
	go func() { time.Sleep(4 * time.Second); n.endAll() }()
	got := results(n.run(t, grantOptions{holdFor: 20 * time.Second, holdCase: "B05-expire", udpSink: "x"}))
	if got["B05-expire"] != "pass" {
		t.Fatalf("B05-expire = %q", got["B05-expire"])
	}
}

func TestHoldFailsWhenNothingCutsTheFlows(t *testing.T) {
	n := newFakeNode(t, 64<<20)
	got := results(n.run(t, grantOptions{holdFor: 3 * time.Second, holdCase: "B05-expire"}))
	if got["B05-expire"] != "fail" {
		t.Fatalf("B05-expire = %q, want fail", got["B05-expire"])
	}
}

func TestRaceNeverOversellsTheQuota(t *testing.T) {
	n := newFakeNode(t, 6<<20)
	got := results(n.run(t, grantOptions{raceN: 3, raceMaxByte: 1 << 30}))
	if got["B06-race"] != "pass" {
		t.Fatalf("B06-race = %q", got["B06-race"])
	}
}

func TestRaceCatchesANodeThatOversells(t *testing.T) {
	n := newFakeNode(t, 6<<20)
	n.oversell = true
	got := results(n.run(t, grantOptions{raceN: 3, raceMaxByte: 1 << 30}))
	if got["B06-race"] != "fail" {
		t.Fatalf("B06-race = %q, want fail for an oversold quota", got["B06-race"])
	}
}

func TestRaceIsBlockedWhenTheQuotaIsTooLargeToContend(t *testing.T) {
	n := newFakeNode(t, 64<<20)
	if got := results(n.run(t, grantOptions{raceN: 2, raceMaxByte: 1 << 20})); got["B06-race"] != "blocked" {
		t.Fatalf("B06-race = %q, want blocked", got["B06-race"])
	}
}

func TestNodeBillBracket(t *testing.T) {
	const rate, total = 500, int64(1 << 30)
	body := int64(10 << 20)
	ok := func(units int64) []string {
		return nodeBillProblems(rate, units, body, 1<<16, total, units, (total*1000-units)/1000)
	}
	if p := ok(body * rate); len(p) != 0 {
		t.Fatalf("exact payload must pass: %v", p)
	}
	if p := ok((body + body/100) * rate); len(p) != 0 {
		t.Fatalf("1%% framing must pass: %v", p)
	}
	if p := ok(0); len(p) == 0 {
		t.Fatal("billing nothing must fail")
	}
	if p := ok(body * rate / 2); len(p) == 0 {
		t.Fatal("billing half the payload must fail")
	}
	if p := ok(body * rate * 2); len(p) == 0 {
		t.Fatal("billing double must fail")
	}
	if p := nodeBillProblems(rate, body*rate, body, 1<<16, total, body*rate, 1); len(p) == 0 {
		t.Fatal("a remaining balance off the formula must fail")
	}
}
