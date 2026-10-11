package service

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"asterlink/core/internal/ipc"
	"asterlink/core/internal/managed"
)

type pipe struct {
	in  *bytes.Buffer
	out bytes.Buffer
}

func (p *pipe) Read(b []byte) (int, error)  { return p.in.Read(b) }
func (p *pipe) Write(b []byte) (int, error) { return p.out.Write(b) }

func run(t *testing.T, manager *managed.Manager, requests ...string) []ipc.Response {
	t.Helper()
	in := &bytes.Buffer{}
	for _, r := range requests {
		if err := ipc.WriteFrame(in, []byte(r)); err != nil {
			t.Fatal(err)
		}
	}
	p := &pipe{in: in}
	if err := ipc.ServeWith(p, ipc.BuildInfo{CoreVersion: "t"}, New(manager)); err != nil {
		t.Fatal(err)
	}
	var out []ipc.Response
	for {
		frame, err := ipc.ReadFrame(&p.out, ipc.MaxFrame)
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		var r ipc.Response
		if err := json.Unmarshal(frame, &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
}

func req(id, command string, generation uint64, payload string) string {
	body := `{"request_id":"` + id + `","protocol_version":1,"generation":` + itoa(generation) + `,"command":"` + command + `"`
	if payload != "" {
		body += `,"payload":` + payload
	}
	return body + `}`
}

func itoa(n uint64) string { b, _ := json.Marshal(n); return string(b) }

func TestPayloadValidation(t *testing.T) {
	manager := managed.New(managed.Options{CoreVersion: "t"})
	good := `{"email":"a@b.cd","password":"pw","api_base":"https://demo.example.invalid/api/v1/client","channel":"release","device_id":"33333333-3333-4333-8333-333333333333","app_version":"1"}`
	long := strings.Repeat("p", 73)
	bad := map[string]string{
		"unknown field":     strings.Replace(good, `"app_version"`, `"yaml":"x","app_version"`, 1),
		"password too long": strings.Replace(good, `"pw"`, `"`+long+`"`, 1),
		"bad channel":       strings.Replace(good, `"release"`, `"debug"`, 1),
		"bad device":        strings.Replace(good, `3333-4333`, `zzzz-4333`, 1),
		"empty email":       strings.Replace(good, `a@b.cd`, ``, 1),
		"trailing":          good + `{}`,
	}
	for name, payload := range bad {
		out := run(t, manager, req("r1", "login", 0, payload))
		if len(out) != 1 || out[0].OK || out[0].Error.Code != ipc.CodeInvalidRequest {
			t.Errorf("%s: %+v", name, out)
		}
	}
	// Valid shape but an API base the channel forbids never reaches the network.
	out := run(t, manager, req("r2", "login", 0, strings.Replace(good, "https", "http", 1)))
	var result struct {
		ActionError *ipc.Error `json:"action_error"`
	}
	json.Unmarshal(out[0].Result, &result)
	if !out[0].OK || result.ActionError == nil || result.ActionError.Code != "invalid_api_base" {
		t.Fatalf("%+v %s", out[0], out[0].Result)
	}
}

func TestGenerationAndStatelessCommands(t *testing.T) {
	manager := managed.New(managed.Options{CoreVersion: "t"})
	out := run(t, manager,
		req("a", "read_snapshot", 0, ""),
		req("b", "connect", 99, ""),                   // stale generation
		req("c", "connect", manager.Generation(), ""), // current generation, but not signed in
		req("d", "select_node", manager.Generation(), `{"node_id":"x","catalog_revision":"y"}`),
		req("e", "disconnect", 5, ""),
		req("f", "logout", 5, ""),
		req("g", "read_snapshot", 0, `{"extra":1}`),
	)
	if !out[0].OK {
		t.Fatalf("snapshot: %+v", out[0])
	}
	if out[1].OK || out[1].Error.Code != "stale_generation" {
		t.Fatalf("stale generation accepted: %+v", out[1])
	}
	var action struct {
		ActionError *ipc.Error `json:"action_error"`
	}
	json.Unmarshal(out[2].Result, &action)
	if action.ActionError == nil || action.ActionError.Code != "not_signed_in" {
		t.Fatalf("connect while signed out: %s", out[2].Result)
	}
	if out[3].OK || out[3].Error.Code != ipc.CodeInvalidRequest {
		t.Fatalf("malformed select accepted: %+v", out[3])
	}
	if !out[4].OK || !out[5].OK {
		t.Fatal("disconnect/logout must always be accepted and idempotent")
	}
	if out[6].OK {
		t.Fatal("snapshot with a payload accepted")
	}
}

// The snapshot sample is also decoded by the Swift tests, so the two sides cannot drift.
func TestSnapshotMatchesSharedSample(t *testing.T) {
	data, err := os.ReadFile("../../../../shared/contracts/snapshot-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var sample struct {
		Contract        string                      `json:"contract"`
		ProtocolVersion int                         `json:"protocol_version"`
		Note            string                      `json:"note"`
		Snapshots       map[string]managed.Snapshot `json:"snapshots"`
	}
	if err := decoder.Decode(&sample); err != nil {
		t.Fatalf("sample does not match the Core's Snapshot type: %v", err)
	}
	if len(sample.Snapshots) < 3 {
		t.Fatal("sample too small")
	}
	// Re-encoding must be a stable superset: no field is silently dropped.
	for name, snap := range sample.Snapshots {
		encoded, _ := json.Marshal(snap)
		var roundTrip map[string]any
		json.Unmarshal(encoded, &roundTrip)
		for _, key := range []string{"generation", "state", "user", "entitlement", "nodes", "selected_node_id", "catalog_revision", "profile_version", "tunnel_health", "metering", "last_error", "core_version", "config_ready"} {
			if _, ok := roundTrip[key]; !ok {
				t.Errorf("%s: missing %s", name, key)
			}
		}
		if bytes.Contains(encoded, []byte("password")) || bytes.Contains(encoded, []byte("token")) || bytes.Contains(encoded, []byte("yaml")) {
			t.Errorf("%s: sample contains secret-bearing field names", name)
		}
	}
}
