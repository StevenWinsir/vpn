package apiclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// The shared native-v2 fixture is also decoded by the API's own tests; here the Core
// proves its types accept exactly what the contract promises.

type v2File struct {
	Version int                        `json:"contract_version"`
	Cases   map[string]json.RawMessage `json:"cases"`
	Errors  map[string]struct {
		Status int    `json:"status"`
		Where  string `json:"where"`
	} `json:"errors"`
}

func loadV2(t *testing.T) v2File {
	t.Helper()
	data, err := os.ReadFile("../../../../shared/contracts/native-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var f v2File
	if err = json.Unmarshal(data, &f); err != nil || f.Version != 2 {
		t.Fatalf("contract: %v", err)
	}
	return f
}

func field(t *testing.T, f v2File, name, key string) json.RawMessage {
	t.Helper()
	var c map[string]json.RawMessage
	if err := json.Unmarshal(f.Cases[name], &c); err != nil || c[key] == nil {
		t.Fatalf("case %s has no %s", name, key)
	}
	return c[key]
}

func strictInto(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		t.Fatalf("%T does not accept the contract: %v", into, err)
	}
}

func TestCoreTypesAcceptTheV2Contract(t *testing.T) {
	f := loadV2(t)
	for _, name := range []string{"grant_create", "grant_pending", "grant_ready", "grant_revoke", "grant_revoked"} {
		var g GrantStatus
		strictInto(t, field(t, f, name, "response"), &g)
		if g.GrantID == "" || g.State == "" {
			t.Fatalf("%s: %+v", name, g)
		}
	}
	var ready GrantStatus
	strictInto(t, field(t, f, "grant_ready", "response"), &ready)
	digest := sha256.Sum256([]byte(ready.YAML))
	if ready.State != GrantReady || ready.Version != hex.EncodeToString(digest[:]) || ready.Session == nil ||
		ready.Session.ProfileVersion != ready.Version || ready.Session.MeteringSource != MeteringNode || ready.Session.GrantID != ready.GrantID || ready.Session.GrantState != GrantReady {
		t.Fatalf("grant_ready: %+v", ready)
	}
	var session Session
	strictInto(t, field(t, f, "session_node_reported", "response"), &session)
	var caps Capabilities
	strictInto(t, field(t, f, "capabilities", "response"), &caps)
	if len(caps.ContractVersions) != 2 || caps.CorePolicyRevision == "" {
		t.Fatalf("capabilities: %+v", caps)
	}
	var catalog struct {
		SessionID string       `json:"session_id"`
		Revision  string       `json:"revision"`
		Reason    string       `json:"reason"`
		Nodes     []NodeChoice `json:"nodes"`
	}
	strictInto(t, field(t, f, "node_choice_governed", "response"), &catalog)
	if len(catalog.Nodes) != 1 || catalog.Nodes[0].MeteringSource != MeteringNode {
		t.Fatalf("catalog: %+v", catalog)
	}
}

// Every code the contract adds that a client can meet must have user-facing Chinese
// text, and the states that end a session must be recognised.
func TestV2ClientFacingErrorsHaveMessages(t *testing.T) {
	f := loadV2(t)
	for _, code := range []string{"agent_unavailable", "metering_source_mismatch", "client_upgrade_required", "grant_expired", "grant_not_found", "idempotency_conflict", "quota_exhausted", "profile_changed"} {
		if _, ok := f.Errors[code]; !ok {
			t.Fatalf("contract does not define %s", code)
		}
		if Message(code) == Message("definitely_unknown_code") {
			t.Fatalf("%s has no localized message", code)
		}
	}
	if !TerminatesSession("metering_source_mismatch") {
		t.Fatal("a session bound to the other metering source cannot recover without a new login")
	}
	if TerminatesSession("grant_expired") || TerminatesSession("agent_unavailable") {
		t.Fatal("a dead grant or an unavailable agent must not log the user out")
	}
}
