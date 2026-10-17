package managed

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"asterlink/core/internal/apiclient"
)

type handoffContract struct {
	TicketFields []string `json:"ticket_fields"`
	TTLSeconds   int      `json:"ttl_seconds"`
	Now          string   `json:"now"`
	Accept       []struct {
		Name   string `json:"name"`
		Ticket string `json:"ticket"`
	} `json:"accept"`
	Reject []struct {
		Name   string `json:"name"`
		Code   string `json:"code"`
		Ticket string `json:"ticket"`
	} `json:"reject"`
	Adopt []struct {
		Name   string `json:"name"`
		Expect string `json:"expect"`
	} `json:"adopt"`
	ErrorCodes []string `json:"error_codes"`
}

func loadHandoffContract(t *testing.T) handoffContract {
	t.Helper()
	data, err := os.ReadFile("../../../../shared/contracts/handoff-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var c handoffContract
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// H01 (static half): every accept ticket parses, every reject ticket is refused with its
// code, the field list and TTL match the Go type, and every code has a Chinese message.
func TestHandoffContractTickets(t *testing.T) {
	c := loadHandoffContract(t)
	now, err := time.Parse(time.RFC3339, c.Now)
	if err != nil {
		t.Fatal(err)
	}
	if time.Duration(c.TTLSeconds)*time.Second != HandoffTTL {
		t.Fatalf("contract ttl %d s, Go %v", c.TTLSeconds, HandoffTTL)
	}
	var probe map[string]any
	encoded, _ := json.Marshal(HandoffTicket{})
	_ = json.Unmarshal(encoded, &probe)
	if len(probe) != len(c.TicketFields) {
		t.Fatalf("ticket has %d fields, contract lists %d", len(probe), len(c.TicketFields))
	}
	for _, field := range c.TicketFields {
		if _, ok := probe[field]; !ok {
			t.Fatalf("ticket lacks contract field %q", field)
		}
	}
	for _, tc := range c.Accept {
		ticket, err := ParseHandoff([]byte(tc.Ticket), now)
		if err != nil {
			t.Fatalf("%s: %v", tc.Name, err)
		}
		again, _ := json.Marshal(ticket)
		if _, err := ParseHandoff(again, now); err != nil {
			t.Fatalf("%s: re-encoded ticket refused: %v", tc.Name, err)
		}
	}
	for _, tc := range c.Reject {
		_, err := ParseHandoff([]byte(tc.Ticket), now)
		if err == nil || code(t, err) != tc.Code {
			t.Fatalf("%s: want %s, got %v", tc.Name, tc.Code, err)
		}
	}
	for _, c := range c.ErrorCodes {
		if apiclient.Message(c) == apiclient.Message("no-such-code") {
			t.Fatalf("code %s has no message", c)
		}
	}
	if len(c.Adopt) < 8 {
		t.Fatalf("contract lists %d adopt scenarios", len(c.Adopt))
	}
}

// The ticket carries the bearer token; no formatting verb may print it.
func TestHandoffTicketPrintsRedacted(t *testing.T) {
	ticket := HandoffTicket{Token: fakeToken, SessionID: "s", Generation: 3}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(verb, ticket), fakeToken) {
			t.Fatalf("%s printed the token", verb)
		}
	}
}

// exportOrFail is an app-side export after a login and a selected line.
func exportOrFail(t *testing.T, m *Manager) HandoffTicket {
	t.Helper()
	ticket, err := m.ExportHandoff(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

// H01 round trip: app -> extension -> app with a connected, metered segment in between.
// One server session, one continuous ledger, and at no point two processes holding a
// session.
func TestHandoffRoundTrip(t *testing.T) {
	f := newFakeAPI(t)
	app := login(t, f, &recordingEngine{})
	selectA(t, app)
	logins := f.loginHits.Load()

	ticket := exportOrFail(t, app)
	if ticket.Generation != 1 || ticket.SelectedNodeID != nodeA || ticket.Token != fakeToken || ticket.User.Email != "test@test.com" {
		t.Fatalf("ticket: %v %+v", ticket, ticket.User)
	}
	snap := app.Snapshot()
	if snap.State != StateReleased || snap.User != nil {
		t.Fatalf("released app: %+v", snap)
	}
	if _, err := app.Connect(t.Context()); code(t, err) != "not_signed_in" {
		t.Fatalf("a released app must not connect: %v", err)
	}
	if f.logoutHits.Load() != 0 {
		t.Fatal("export revoked the session")
	}

	// The ticket survives its own encoding (it crosses process boundaries as JSON).
	raw, _ := json.Marshal(ticket)
	parsed, err := ParseHandoff(raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	engine := &recordingEngine{}
	tunnel := newTestManager(t, engine)
	snap, err = tunnel.AdoptHandoff(t.Context(), parsed)
	if err != nil || snap.State != StateReady || snap.SelectedNodeID != nodeA || !snap.ConfigReady || snap.User == nil {
		t.Fatalf("adopt: %v %+v", err, snap)
	}
	engine.up.Store(20 << 20)
	if snap, err = tunnel.Connect(t.Context()); err != nil || snap.State != StateConnected {
		t.Fatalf("connect after adopt: %v %+v", err, snap)
	}
	engine.up.Store(20<<20 + 1000)
	engine.down.Store(80 << 20)

	back, err := tunnel.ExportHandoff(t.Context())
	if err != nil || back.Generation != 2 || back.LastSequence == 0 {
		t.Fatalf("export back: %v %v", err, back)
	}
	f.mu.Lock()
	server := f.session
	f.mu.Unlock()
	// Everything counted after the connect baseline was acknowledged before the export.
	if server.UploadBytes != 1000 || server.DownloadBytes != 80<<20 || server.LastSequence != back.LastSequence {
		t.Fatalf("ledger at export: up %d down %d seq %d (ticket %d)", server.UploadBytes, server.DownloadBytes, server.LastSequence, back.LastSequence)
	}
	if tunnel.Snapshot().State != StateReleased || engine.stops.Load() == 0 {
		t.Fatal("the extension still holds the session or the data path")
	}

	snap, err = app.AdoptHandoff(t.Context(), back)
	if err != nil || snap.State != StateReady || snap.SelectedNodeID != nodeA {
		t.Fatalf("app adopts back: %v %+v", err, snap)
	}
	if f.loginHits.Load() != logins || f.logoutHits.Load() != 0 {
		t.Fatal("a handoff must neither log in again nor revoke")
	}
	// The app may connect locally again from the same ledger (meter.Begin accepts it).
	if snap = app.Logout(t.Context()); snap.State != StateSignedOut || f.logoutHits.Load() != 1 {
		t.Fatalf("logout after the round trip: %+v", snap)
	}
}

// H01 refusals on the adopting side.
func TestHandoffAdoptRefusals(t *testing.T) {
	f := newFakeAPI(t)
	app := login(t, f, &recordingEngine{})
	selectA(t, app)
	ticket := exportOrFail(t, app)

	t.Run("session_id mismatch", func(t *testing.T) {
		other := ticket
		other.SessionID = "44444444-4444-4444-8444-444444444444"
		m := newTestManager(t, &recordingEngine{})
		snap, err := m.AdoptHandoff(t.Context(), other)
		if code(t, err) != "handoff_mismatch" || snap.State != StateSignedOut || snap.LastError == nil {
			t.Fatalf("%v %+v", err, snap)
		}
	})
	t.Run("server behind the ledger", func(t *testing.T) {
		ahead := ticket
		ahead.LastSequence = 99
		m := newTestManager(t, &recordingEngine{})
		if _, err := m.AdoptHandoff(t.Context(), ahead); code(t, err) != "handoff_conflict" {
			t.Fatal(err)
		}
	})
	t.Run("session revoked", func(t *testing.T) {
		revoked := ticket
		revoked.Token = strings.Repeat("B", 43)
		m := newTestManager(t, &recordingEngine{})
		snap, err := m.AdoptHandoff(t.Context(), revoked)
		if code(t, err) != "native_session_required" || snap.State != StateSignedOut {
			t.Fatalf("%v %+v", err, snap)
		}
	})
	t.Run("duplicate adopt and older generation", func(t *testing.T) {
		m := newTestManager(t, &recordingEngine{})
		if _, err := m.AdoptHandoff(t.Context(), ticket); err != nil {
			t.Fatal(err)
		}
		if _, err := m.AdoptHandoff(t.Context(), ticket); code(t, err) != "handoff_busy" {
			t.Fatalf("duplicate: %v", err)
		}
		if _, err := m.ExportHandoff(t.Context()); err != nil {
			t.Fatal(err)
		}
		// Released again: the very ticket it adopted before is now a replay.
		if _, err := m.AdoptHandoff(t.Context(), ticket); code(t, err) != "handoff_stale" {
			t.Fatalf("replay: %v", err)
		}
	})
	t.Run("own ticket back", func(t *testing.T) {
		// The app's own process never adopted generation 1, so it may take it back when the
		// extension failed before adopting.
		snap, err := app.AdoptHandoff(t.Context(), ticket)
		if err != nil || snap.State != StateReady {
			t.Fatalf("%v %+v", err, snap)
		}
	})
}

// A transient failure leaves the ticket usable; a ticket is consumed only by a success.
func TestHandoffAdoptTransientFailureRetries(t *testing.T) {
	f := newFakeAPI(t)
	app := login(t, f, &recordingEngine{})
	ticket := exportOrFail(t, app)
	f.mutate(func() { f.sessionErr = &apiError{503, "client_service_unavailable"} })
	m := newTestManager(t, &recordingEngine{})
	if _, err := m.AdoptHandoff(t.Context(), ticket); err == nil {
		t.Fatal("adopt succeeded against a failing server")
	}
	f.mutate(func() { f.sessionErr = nil })
	if snap, err := m.AdoptHandoff(t.Context(), ticket); err != nil || snap.State == StateSignedOut {
		t.Fatalf("retry: %v %+v", err, snap)
	}
}

// Export refuses while a batch is unacknowledged and keeps the session where it is.
func TestHandoffExportRefusesUnsettled(t *testing.T) {
	f := newFakeAPI(t)
	engine := &recordingEngine{}
	m := login(t, f, engine)
	selectA(t, m)
	if _, err := m.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	engine.down.Store(4096)
	f.mutate(func() { f.trafficErr = &apiError{503, "client_service_unavailable"} })
	if _, err := m.ExportHandoff(t.Context()); code(t, err) != "handoff_unsettled" {
		t.Fatalf("export with an unacknowledged batch: %v", err)
	}
	if snap := m.Snapshot(); snap.User == nil || snap.State == StateReleased {
		t.Fatalf("the session must stay here: %+v", snap)
	}
	f.mutate(func() { f.trafficErr = nil })
	if _, err := m.ExportHandoff(t.Context()); err != nil {
		t.Fatalf("export once settled: %v", err)
	}
}

// Without a session there is nothing to export.
func TestHandoffExportNeedsSession(t *testing.T) {
	m := newTestManager(t, &recordingEngine{})
	if _, err := m.ExportHandoff(t.Context()); code(t, err) != "not_signed_in" {
		t.Fatal(err)
	}
}
