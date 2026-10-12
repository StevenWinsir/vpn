package agent

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"asterlink/nodeagent/control"
	"asterlink/nodeagent/forwarder"
)

const (
	grantA = "aaaaaaaa-0000-4000-8000-000000000001"
	grantB = "bbbbbbbb-0000-4000-8000-000000000002"
	uuidA  = "11111111-2222-4333-8444-555555555555"
	uuidB  = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

// fakeAPI is a control plane stand-in with the real server's usage semantics.
type fakeAPI struct {
	mu       sync.Mutex
	orders   []control.Order
	syncs    []control.SyncRequest
	reports  []control.UsageRequest
	stored   map[string]control.UsageRequest // "grant/epoch/seq" -> body
	failNext int                             // fail this many usage calls after applying them
	failSync bool
	reject   map[string]int // grant -> http status to return for usage
}

func newFake() *fakeAPI { return &fakeAPI{stored: map[string]control.UsageRequest{}} }

func (f *fakeAPI) Sync(_ context.Context, in control.SyncRequest) (control.SyncResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSync {
		return control.SyncResponse{}, &control.Error{Code: "network_unreachable", Transient: true}
	}
	f.syncs = append(f.syncs, in)
	orders := f.orders
	return control.SyncResponse{ServerTime: time.Now(), Grants: orders}, nil
}

func (f *fakeAPI) Usage(_ context.Context, in control.UsageRequest) (control.UsageResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := in.GrantID + "/" + strconv.FormatInt(in.Epoch, 10) + "/" + strconv.FormatInt(in.Sequence, 10)
	if prior, ok := f.stored[key]; ok {
		if prior != in {
			return control.UsageResponse{}, &control.Error{Status: 409, Code: "usage_conflict"}
		}
		return control.UsageResponse{Replayed: true}, nil
	}
	f.stored[key] = in
	f.reports = append(f.reports, in)
	if f.failNext > 0 {
		f.failNext--
		return control.UsageResponse{}, &control.Error{Code: "network_unreachable", Transient: true}
	}
	return control.UsageResponse{State: "ready", Epoch: in.Epoch, Sequence: in.Sequence}, nil
}

func (f *fakeAPI) last(grant string) (control.UsageRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.reports) - 1; i >= 0; i-- {
		if f.reports[i].GrantID == grant {
			return f.reports[i], true
		}
	}
	return control.UsageRequest{}, false
}

func echo(t *testing.T) net.Addr {
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

func connect(t *testing.T, fwd *forwarder.VLESS, uuid string, target net.Addr) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", fwd.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	raw, _ := hex.DecodeString(strings.ReplaceAll(uuid, "-", ""))
	host, port, _ := net.SplitHostPort(target.String())
	p, _ := strconv.Atoi(port)
	req := append([]byte{0}, raw...)
	req = append(req, 0, 1)
	req = binary.BigEndian.AppendUint16(req, uint16(p))
	req = append(append(req, 1), net.ParseIP(host).To4()...)
	if _, err = c.Write(req); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err = io.ReadFull(c, make([]byte, 2)); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	return c
}

func exchange(t *testing.T, c net.Conn, n int) {
	t.Helper()
	data := make([]byte, n)
	if _, err := c.Write(data); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(c, make([]byte, n)); err != nil {
		t.Fatal(err)
	}
}

func dead(c net.Conn) bool {
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err := c.Read(make([]byte, 1))
	var ne net.Error
	return err != nil && !(errors.As(err, &ne) && ne.Timeout())
}

type rig struct {
	t     *testing.T
	api   *fakeAPI
	fwd   *forwarder.VLESS
	ag    *Agent
	state string
	echo  net.Addr
}

func newRig(t *testing.T) *rig {
	t.Helper()
	fwd, err := forwarder.NewVLESS(forwarder.VLESSOptions{Listen: "127.0.0.1:0", AllowPrivateTargets: true, WatchInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fwd.Close() })
	r := &rig{t: t, api: newFake(), fwd: fwd, state: filepath.Join(t.TempDir(), "state.json"), echo: echo(t)}
	r.ag, err = New(Options{API: r.api, Fwd: fwd, StatePath: r.state, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) order(id, uuid string, ttl time.Duration, budget int64) {
	exp := time.Now().Add(ttl)
	r.api.mu.Lock()
	defer r.api.mu.Unlock()
	r.api.orders = []control.Order{{GrantID: id, Action: "install", Protocol: "vless", Credential: map[string]string{"uuid": uuid}, ExpiresAt: &exp, EpochBudgetBytes: budget}}
}

func (r *rig) sync() {
	r.t.Helper()
	if err := r.ag.SyncOnce(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) report() {
	r.t.Helper()
	if err := r.ag.ReportOnce(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

func TestOrdersInstallReportAndFinalRevoke(t *testing.T) {
	r := newRig(t)
	r.order(grantA, uuidA, time.Minute, 1<<20)
	r.sync()
	c := connect(t, r.fwd, uuidA, r.echo)
	exchange(t, c, 1000)
	r.report()
	first, ok := r.api.last(grantA)
	if !ok || first.Sequence != 1 || first.UploadBytes != 1000 || first.DownloadBytes != 1000 || first.Final || first.Epoch != r.ag.Epoch() {
		t.Fatalf("first report: %+v", first)
	}
	exchange(t, c, 500)
	r.report()
	if second, _ := r.api.last(grantA); second.Sequence != 2 || second.UploadBytes != 1500 {
		t.Fatalf("second report: %+v", second)
	}
	// Unchanged counters produce no report.
	n := len(r.api.reports)
	r.report()
	if len(r.api.reports) != n {
		t.Fatal("idle grant reported again")
	}
	exchange(t, c, 250)

	// The control plane revokes: flows die, one final report carries every byte.
	r.api.mu.Lock()
	r.api.orders = []control.Order{{GrantID: grantA, Action: "revoke"}}
	r.api.mu.Unlock()
	r.sync()
	if !dead(c) {
		t.Fatal("established flow survived a revoke order")
	}
	r.report()
	final, _ := r.api.last(grantA)
	if final.Sequence != 3 || !final.Final || final.UploadBytes != 1750 || final.DownloadBytes != 1750 {
		t.Fatalf("final report: %+v", final)
	}
	// The removal is announced exactly once, after the final report was acknowledged.
	r.api.mu.Lock()
	r.api.orders = nil
	r.api.mu.Unlock()
	r.sync()
	last := r.api.syncs[len(r.api.syncs)-1]
	if len(last.Removed) != 1 || last.Removed[0] != grantA || len(last.Installed) != 0 {
		t.Fatalf("removal announcement: %+v", last)
	}
	r.sync()
	if again := r.api.syncs[len(r.api.syncs)-1]; len(again.Removed) != 0 {
		t.Fatalf("removal announced twice: %+v", again)
	}
	// A re-install of a closed grant is refused: a finished series never resumes.
	r.order(grantA, uuidA, time.Minute, 1<<20)
	r.sync()
	r.api.mu.Lock()
	r.api.orders = nil
	r.api.mu.Unlock()
	if len(r.fwd.Usage()) != 0 {
		t.Fatal("closed grant was resurrected")
	}
}

func TestLostAcknowledgementRetriesTheSameReportVerbatim(t *testing.T) {
	r := newRig(t)
	r.order(grantA, uuidA, time.Minute, 1<<20)
	r.sync()
	c := connect(t, r.fwd, uuidA, r.echo)
	exchange(t, c, 100)
	r.api.failNext = 1
	if err := r.ag.ReportOnce(context.Background()); err == nil {
		t.Fatal("lost reply must surface as an error")
	}
	exchange(t, c, 900) // more traffic while the first report is unacknowledged
	r.report()          // must resend seq 1 with the ORIGINAL counters
	r.api.mu.Lock()
	var seqs []int64
	var ups []int64
	for _, rep := range r.api.reports {
		seqs, ups = append(seqs, rep.Sequence), append(ups, rep.UploadBytes)
	}
	r.api.mu.Unlock()
	if len(seqs) != 1 || seqs[0] != 1 || ups[0] != 100 {
		t.Fatalf("pending report was altered: seqs=%v ups=%v", seqs, ups)
	}
	r.report() // now the newer bytes go out as seq 2
	if rep, _ := r.api.last(grantA); rep.Sequence != 2 || rep.UploadBytes != 1000 {
		t.Fatalf("follow-up report: %+v", rep)
	}
}

func TestOfflineAgentStillEnforcesDeadlineAndCarriesCounters(t *testing.T) {
	r := newRig(t)
	r.order(grantA, uuidA, 600*time.Millisecond, 1<<20)
	r.sync()
	r.api.failSync = true // control plane unreachable from now on
	c := connect(t, r.fwd, uuidA, r.echo)
	exchange(t, c, 400)
	if !dead(c) {
		t.Fatal("flow outlived the lease while the control plane was unreachable")
	}
	if err := r.ag.Tick(); err != nil {
		t.Fatal(err)
	}
	if len(r.fwd.Usage()) != 0 {
		t.Fatal("expired grant still installed")
	}
	r.report()
	if rep, _ := r.api.last(grantA); rep.UploadBytes != 400 || rep.Final {
		t.Fatalf("lapse must report the bytes without ending the series: %+v", rep)
	}
	// Connectivity returns and the lease is renewed: the cumulative series continues.
	r.api.failSync = false
	r.order(grantA, uuidA, time.Minute, 1<<20)
	r.sync()
	c2 := connect(t, r.fwd, uuidA, r.echo)
	exchange(t, c2, 100)
	r.report()
	if rep, _ := r.api.last(grantA); rep.Sequence != 2 || rep.UploadBytes != 500 || rep.DownloadBytes != 500 {
		t.Fatalf("counters restarted instead of carrying: %+v", rep)
	}
}

func TestCrashRecoveryClosesTheOldEpochFromItsCheckpoint(t *testing.T) {
	r := newRig(t)
	r.order(grantA, uuidA, time.Minute, 1<<20)
	r.sync()
	c := connect(t, r.fwd, uuidA, r.echo)
	exchange(t, c, 2000)
	r.report()
	exchange(t, c, 700)
	if err := r.ag.Tick(); err != nil { // checkpoint: 2700 each way
		t.Fatal(err)
	}
	exchange(t, c, 50) // lost in the crash: after the last checkpoint
	oldEpoch := r.ag.Epoch()
	_ = r.fwd.Close() // the process dies; no graceful shutdown

	fwd2, err := forwarder.NewVLESS(forwarder.VLESSOptions{Listen: "127.0.0.1:0", AllowPrivateTargets: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fwd2.Close() })
	later := time.Unix(oldEpoch+1, 0)
	b, err := New(Options{API: r.api, Fwd: fwd2, StatePath: r.state, Now: func() time.Time { return later }})
	if err != nil {
		t.Fatal(err)
	}
	if b.Epoch() <= oldEpoch {
		t.Fatalf("epoch did not advance: %d -> %d", oldEpoch, b.Epoch())
	}
	if err = b.ReportOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	rep, _ := r.api.last(grantA)
	if rep.Epoch != oldEpoch || !rep.Final || rep.Sequence != 2 || rep.UploadBytes != 2700 || rep.DownloadBytes != 2700 {
		t.Fatalf("recovery report must close the OLD epoch from the checkpoint: %+v", rep)
	}
	if err = b.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.api.syncs[len(r.api.syncs)-1]; got.Epoch != b.Epoch() || len(got.Removed) != 1 || got.Removed[0] != grantA {
		t.Fatalf("recovered removal not announced: %+v", got)
	}
	// The recovered grant is not served again by the new process.
	r.order(grantA, uuidA, time.Minute, 1<<20)
	if err = b.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fwd2.Usage()) != 0 {
		t.Fatal("a grant closed by recovery was re-installed")
	}
}

func TestGracefulShutdownReportsEveryGrantFinal(t *testing.T) {
	r := newRig(t)
	exp := time.Now().Add(time.Minute)
	r.api.orders = []control.Order{
		{GrantID: grantA, Action: "install", Protocol: "vless", Credential: map[string]string{"uuid": uuidA}, ExpiresAt: &exp, EpochBudgetBytes: 1 << 20},
		{GrantID: grantB, Action: "install", Protocol: "vless", Credential: map[string]string{"uuid": uuidB}, ExpiresAt: &exp, EpochBudgetBytes: 1 << 20},
	}
	r.sync()
	ca, cb := connect(t, r.fwd, uuidA, r.echo), connect(t, r.fwd, uuidB, r.echo)
	exchange(t, ca, 10)
	exchange(t, cb, 20)
	r.ag.Shutdown(context.Background())
	for grant, want := range map[string]int64{grantA: 10, grantB: 20} {
		rep, ok := r.api.last(grant)
		if !ok || !rep.Final || rep.UploadBytes != want {
			t.Fatalf("%s: %+v", grant, rep)
		}
	}
	if !dead(ca) || !dead(cb) {
		t.Fatal("flows survived shutdown")
	}
}

func TestPermanentRejectionsDoNotWedgeTheSeries(t *testing.T) {
	r := newRig(t)
	r.order(grantA, uuidA, time.Minute, 1<<20)
	r.sync()
	c := connect(t, r.fwd, uuidA, r.echo)
	exchange(t, c, 10)
	r.api.mu.Lock()
	r.api.reject = map[string]int{grantA: 404}
	r.api.mu.Unlock()
	// grant_not_found: drop the series instead of retrying forever.
	api := &rejectingAPI{fakeAPI: r.api, code: "grant_not_found", status: 404}
	r.ag.o.API = api
	r.report()
	if _, still := r.ag.st.Grants[grantA]; still {
		t.Fatal("unknown grant kept forever")
	}
}

type rejectingAPI struct {
	*fakeAPI
	code   string
	status int
}

func (r *rejectingAPI) Usage(context.Context, control.UsageRequest) (control.UsageResponse, error) {
	return control.UsageResponse{}, &control.Error{Status: r.status, Code: r.code}
}

func TestStateFileIsPrivateAtomicAndNeverGuessed(t *testing.T) {
	r := newRig(t)
	info, err := os.Stat(r.state)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v %v", info, err)
	}
	if err = os.WriteFile(r.state, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = New(Options{API: r.api, Fwd: r.fwd, StatePath: r.state}); err == nil {
		t.Fatal("corrupt state must be refused, not silently reset")
	}
	// A lost file still yields a forward-moving epoch.
	_ = os.Remove(r.state)
	fresh, err := New(Options{API: r.api, Fwd: r.fwd, StatePath: r.state})
	if err != nil || fresh.Epoch() < time.Now().Unix()-1 {
		t.Fatalf("epoch after state loss: %d %v", fresh.Epoch(), err)
	}
}
