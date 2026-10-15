package mobile

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"asterlink/core/internal/ipc"
	"asterlink/core/internal/managed"
)

type fakePlatform struct{ protected atomic.Int32 }

func (p *fakePlatform) Protect(int32) bool      { p.protected.Add(1); return true }
func (p *fakePlatform) OpenTun(string) int32    { return -1 }
func (p *fakePlatform) NetworkSnapshot() string { return `{"available":false}` }

type duplex struct {
	in  *bytes.Reader
	out bytes.Buffer
}

func (d *duplex) Read(p []byte) (int, error)  { return d.in.Read(p) }
func (d *duplex) Write(p []byte) (int, error) { return d.out.Write(p) }

// viaPipe answers one request the way the desktop pipe server does.
func viaPipe(t *testing.T, request string) string {
	t.Helper()
	var in bytes.Buffer
	if err := ipc.WriteFrame(&in, []byte(request)); err != nil {
		t.Fatal(err)
	}
	conn := &duplex{in: bytes.NewReader(in.Bytes())}
	if err := ipc.Serve(conn, info()); err != nil {
		t.Fatal(err)
	}
	body, err := ipc.ReadFrame(&conn.out, ipc.MaxFrame)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// Every shared IPC case must produce the byte-identical response through the binding and
// through the pipe: one decoder, two transports (handoff_app_android.md 3.3, round 1).
func TestBindingAnswersEverySharedContractCaseLikeThePipe(t *testing.T) {
	data, err := os.ReadFile("../../../shared/contracts/ipc-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Cases []struct {
			Name    string `json:"name"`
			Padding int    `json:"request_padding_bytes"`
			Request string `json:"request"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Cases) < 8 {
		t.Fatalf("fixture has %d cases", len(contract.Cases))
	}
	for _, c := range contract.Cases {
		t.Run(c.Name, func(t *testing.T) {
			request := strings.ReplaceAll(c.Request, "%PAD%", strings.Repeat("a", c.Padding))
			// The fixture describes a Core without a session handler.
			got := newSession(nil).call(request)
			if want := viaPipe(t, request); got != want {
				t.Fatalf("binding and pipe differ\nbinding %s\npipe    %s", got, want)
			}
		})
	}
}

func decode(t *testing.T, raw string) ipc.Response {
	t.Helper()
	var r ipc.Response
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return r
}

func TestBeforeStartOnlyBuildIdentityAnswers(t *testing.T) {
	Stop()
	hello := decode(t, Call(`{"request_id":"h","protocol_version":1,"generation":0,"command":"hello"}`))
	var identity ipc.HelloResult
	if !hello.OK || json.Unmarshal(hello.Result, &identity) != nil || identity.CoreVersion != version || identity.MihomoRevision != mihomoRevision {
		t.Fatalf("hello %+v", hello)
	}
	for _, command := range []string{"read_snapshot", "connect", "login"} {
		r := decode(t, Call(`{"request_id":"x","protocol_version":1,"generation":0,"command":"`+command+`"}`))
		if r.OK || r.Error == nil || r.Error.Code != CodeNotStarted || r.RequestID != "x" {
			t.Fatalf("%s before Start: %+v", command, r)
		}
	}
}

func TestStartRejectsBadInput(t *testing.T) {
	Stop()
	dir := t.TempDir()
	if err := Start(nil, startJSON(dir)); err != errNoPlatform {
		t.Fatalf("nil platform: %v", err)
	}
	for name, build := range map[string]string{
		"empty":         ``,
		"unknown field": strings.TrimSuffix(startJSON(dir), "}") + `,"api_base":"https://x"}`,
		"trailing":      startJSON(dir) + `{}`,
		"relative":      startJSON("cache"),
		"unclean":       startJSON(dir + "/../" + filepath.Base(dir)),
		"missing":       startJSON(filepath.Join(dir, "absent")),
	} {
		if err := Start(&fakePlatform{}, build); err != errBuildConfig {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// startJSON encodes Start's input; paths are JSON-escaped (Windows CI runs these tests too).
func startJSON(dir string) string {
	body, _ := json.Marshal(map[string]string{"cache_dir": dir})
	return string(body)
}

func start(t *testing.T) {
	t.Helper()
	Stop()
	// Start points TMPDIR at the app cache; give it back so later t.TempDir calls work.
	t.Setenv("TMPDIR", os.Getenv("TMPDIR"))
	if err := Start(&fakePlatform{}, startJSON(t.TempDir())); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Stop)
}

func TestStartedCoreRunsTheManagedSession(t *testing.T) {
	start(t)
	if err := Start(&fakePlatform{}, startJSON(t.TempDir())); err != errAlreadyStarted {
		t.Fatalf("second Start: %v", err)
	}
	r := decode(t, Call(`{"request_id":"s","protocol_version":1,"generation":0,"command":"read_snapshot"}`))
	var snapshot managed.Snapshot
	if !r.OK || json.Unmarshal(r.Result, &snapshot) != nil || snapshot.State != managed.StateSignedOut {
		t.Fatalf("read_snapshot %+v", r)
	}
	// The login payload is decoded by the same strict service code as on the desktop.
	r = decode(t, Call(`{"request_id":"l","protocol_version":1,"generation":0,"command":"login","payload":{"email":"a@b.c"}}`))
	if r.OK || r.Error == nil || r.Error.Code != ipc.CodeInvalidRequest {
		t.Fatalf("bad login %+v", r)
	}
	// shutdown ends the session; later commands are refused until the next Start.
	if r = decode(t, Call(`{"request_id":"z","protocol_version":1,"generation":0,"command":"shutdown"}`)); !r.OK {
		t.Fatalf("shutdown %+v", r)
	}
	if r = decode(t, Call(`{"request_id":"y","protocol_version":1,"generation":0,"command":"read_snapshot"}`)); r.Error == nil || r.Error.Code != CodeNotStarted {
		t.Fatalf("after shutdown %+v", r)
	}
	if err := Start(&fakePlatform{}, startJSON(t.TempDir())); err != nil {
		t.Fatalf("restart after shutdown: %v", err)
	}
}

// The login request carries the platform of the build target, never one the app chose;
// on an Android build that is "android" (backend/internal/api/native.go accepts it).
func TestLoginCarriesThisBuildsPlatform(t *testing.T) {
	seen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/client/login" {
			body, _ := io.ReadAll(r.Body)
			var in struct {
				Platform string `json:"platform"`
			}
			_ = json.Unmarshal(body, &in)
			select {
			case seen <- in.Platform:
			default:
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_credentials","message":"x"}}`))
	}))
	defer server.Close()
	start(t)
	payload := `{"email":"a@b.c","password":"wrong-password","api_base":"` + server.URL + `/api/v1/client","channel":"development","device_id":"33333333-3333-4333-8333-333333333333","app_version":"0.1.0"}`
	r := decode(t, Call(`{"request_id":"l","protocol_version":1,"generation":0,"command":"login","payload":`+payload+`}`))
	var result struct {
		ActionError *ipc.Error `json:"action_error"`
	}
	if !r.OK || json.Unmarshal(r.Result, &result) != nil || result.ActionError == nil || result.ActionError.Code != "invalid_credentials" {
		t.Fatalf("login %s", Call(`{"request_id":"s","protocol_version":1,"generation":0,"command":"read_snapshot"}`))
	}
	got := <-seen
	if got != managed.ClientPlatform() {
		t.Fatalf("platform %q, want %q", got, managed.ClientPlatform())
	}
	if runtime.GOOS == "android" && got != "android" {
		t.Fatalf("an Android build logged in as %q", got)
	}
}

type blockingHandler struct{ entered chan struct{} }

func (h blockingHandler) Handle(ctx context.Context, _ ipc.Request) (any, *ipc.Error) {
	close(h.entered)
	<-ctx.Done()
	return nil, &ipc.Error{Code: "cancelled", Message: "cancelled"}
}
func (blockingHandler) Close() {}

// Stop must not wait out a 90-second connect: it cancels the command in flight.
func TestCloseInterruptsTheCommandInFlight(t *testing.T) {
	h := blockingHandler{entered: make(chan struct{})}
	s := newSession(h)
	done := make(chan string, 1)
	go func() { done <- s.call(`{"request_id":"c","protocol_version":1,"generation":0,"command":"connect"}`) }()
	<-h.entered
	start := time.Now()
	s.close()
	if r := decode(t, <-done); r.Error == nil || r.Error.Code != "cancelled" || time.Since(start) > 2*time.Second {
		t.Fatalf("%+v after %v", r, time.Since(start))
	}
	if r := decode(t, s.call(`{"request_id":"d","protocol_version":1,"generation":0,"command":"connect"}`)); r.Error == nil || r.Error.Code != CodeNotStarted {
		t.Fatalf("closed session answered %+v", r)
	}
}
