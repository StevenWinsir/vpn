package mobile

import (
	"encoding/json"
	"runtime/debug"
	"testing"
)

// The iOS handoff functions answer ipc-v1 response documents with stable codes, are refused
// while no Core runs, and never accept a malformed ticket (shared/contracts/handoff-v1.json;
// the session semantics are tested in internal/managed).
func TestHandoffFunctionsAnswerResponseDocuments(t *testing.T) {
	Stop()
	for name, raw := range map[string]string{"export": ExportSession(), "adopt": AdoptSession("{}")} {
		if r := decode(t, raw); r.OK || r.Error == nil || r.Error.Code != CodeNotStarted {
			t.Fatalf("%s before Start: %+v", name, r)
		}
	}
	start(t)
	if r := decode(t, ExportSession()); r.OK || r.Error == nil || r.Error.Code != "not_signed_in" || r.Error.Message == "" {
		t.Fatalf("export without a session: %+v", r)
	}
	for _, ticket := range []string{"", "ticket", `{"version":1}`} {
		if r := decode(t, AdoptSession(ticket)); r.OK || r.Error == nil || r.Error.Code != "handoff_invalid" {
			t.Fatalf("adopt %q: %+v", ticket, r)
		}
	}
	// A refused adoption leaves the Core signed out and usable.
	r := decode(t, Call(`{"request_id":"s","protocol_version":1,"generation":0,"command":"read_snapshot"}`))
	var snapshot struct {
		State string `json:"state"`
	}
	if !r.OK || json.Unmarshal(r.Result, &snapshot) != nil || snapshot.State != "signed_out" {
		t.Fatalf("after refusals %+v", r)
	}
}

func TestMemoryControls(t *testing.T) {
	limit := debug.SetMemoryLimit(-1)
	percent := debug.SetGCPercent(100)
	debug.SetGCPercent(percent)
	t.Cleanup(func() { debug.SetMemoryLimit(limit); debug.SetGCPercent(percent) })

	TuneMemory(24<<20, 60)
	if got := debug.SetMemoryLimit(-1); got != 24<<20 {
		t.Fatalf("memory limit %d", got)
	}
	if got := debug.SetGCPercent(60); got != 60 {
		t.Fatalf("gc percent %d", got)
	}
	TuneMemory(0, 0) // keeps both
	if debug.SetMemoryLimit(-1) != 24<<20 {
		t.Fatal("zero changed the limit")
	}
	FreeMemory()
	var stats struct {
		Goroutines int    `json:"goroutines"`
		HeapInUse  uint64 `json:"heap_in_use"`
		Sys        uint64 `json:"sys"`
	}
	if err := json.Unmarshal([]byte(RuntimeStats()), &stats); err != nil || stats.Goroutines == 0 || stats.Sys == 0 {
		t.Fatalf("stats %+v %v", stats, err)
	}
}
