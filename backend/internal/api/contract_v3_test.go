package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// native-v3 (presence plan 8.1) adds online leases on top of v2. The fixture is shared
// with the Core; these tests make the live types and handlers agree with it.

type v3Contract struct {
	Version          int                        `json:"contract_version"`
	Cases            map[string]json.RawMessage `json:"cases"`
	ReleaseReasons   []string                   `json:"release_reasons"`
	ConnectionStates []string                   `json:"connection_states"`
	SessionReasons   map[string]string          `json:"session_reasons"`
	Errors           map[string]struct {
		Status int `json:"status"`
	} `json:"errors"`
}

func readV3(t *testing.T) v3Contract {
	t.Helper()
	data, err := os.ReadFile("testdata/native_v3_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var c v3Contract
	if err = json.Unmarshal(data, &c); err != nil || c.Version != 3 {
		t.Fatalf("bad v3 contract: %v", err)
	}
	return c
}

func (c v3Contract) section(t *testing.T, name, field string) json.RawMessage {
	t.Helper()
	var kase map[string]json.RawMessage
	if err := json.Unmarshal(c.Cases[name], &kase); err != nil || kase[field] == nil {
		t.Fatalf("v3 case %s has no %s", name, field)
	}
	return kase[field]
}

// contractShapeV3 fails when a live JSON object differs in field names from the fixture.
func contractShapeV3(t *testing.T, name, field string, body []byte) {
	t.Helper()
	want, got := shapeOf(readV3(t).section(t, name, field)), shapeOf(body)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("%s drifted from native-v3:\n fixture %s\n live    %s", name, describe(want), describe(got))
	}
}

func TestV3FixtureFitsLiveTypes(t *testing.T) {
	c := readV3(t)
	var created connectionResponse
	strict(t, c.section(t, "connection_create", "response"), &created)
	if created.Connection.ID != created.Session.ConnectionID || created.Connection.State != "active" || created.Session.ConnectionState != "active" ||
		!created.Connection.ExpiresAt.Equal(*created.Session.ConnectionExpiresAt) || nativeProfileVersion([]byte(created.YAML)) != created.Version || created.Session.MeteringSource != meteringClient {
		t.Fatalf("connection_create fixture is inconsistent: %+v", created)
	}
	var released struct {
		Connection connectionView `json:"connection"`
		Session    nativeState    `json:"session"`
	}
	strict(t, c.section(t, "connection_release", "response"), &released)
	if released.Connection.State != "ended" || released.Session.ConnectionState != "ended" {
		t.Fatal("connection_release fixture")
	}
	var expired nativeState
	strict(t, c.section(t, "session_connection_expired", "response"), &expired)
	if expired.Reason != "connection_expired" || expired.CanConnect || expired.AuthorizationExpiresAt != nil || expired.ConnectionState != "expired" {
		t.Fatal("session_connection_expired fixture")
	}
	var ready grantResponse
	strict(t, c.section(t, "grant_ready", "response"), &ready)
	var node nativeState
	strict(t, c.section(t, "session_node_reported", "response"), &node)
	if ready.Session.ConnectionID == "" || node.ConnectionID == "" {
		t.Fatal("v3 grant sessions must carry their lease")
	}
	var request struct {
		NodeID      string `json:"node_id"`
		AvoidNodeID string `json:"avoid_node_id"`
	}
	strict(t, c.section(t, "grant_create", "request"), &request)
	var catalog nativeCatalog
	strict(t, c.section(t, "catalog_connection", "response"), &catalog)
	if catalog.Connection == nil || catalog.Connection.State != "ended" || len(catalog.Revision) != 64 {
		t.Fatal("catalog_connection fixture")
	}
	var revoke struct {
		Reason string `json:"reason"`
	}
	strict(t, c.section(t, "grant_revoke", "request"), &revoke)
	if _, known := releaseReasons[revoke.Reason]; !known {
		t.Fatal("grant_revoke reason is not a release reason")
	}
	var reasons []string
	for reason := range releaseReasons {
		reasons = append(reasons, reason)
	}
	slices.Sort(reasons)
	fixture := slices.Clone(c.ReleaseReasons)
	slices.Sort(fixture)
	if !slices.Equal(reasons, fixture) {
		t.Fatalf("release reasons: live %v fixture %v", reasons, fixture)
	}
	if !slices.Equal(c.ConnectionStates, []string{"active", "expired", "ended"}) {
		t.Fatal("connection states")
	}
}

// Every v3 error code and session reason is emitted by the handlers.
func TestV3CodesAreEmitted(t *testing.T) {
	c := readV3(t)
	var source strings.Builder
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			data, _ := os.ReadFile(f)
			source.Write(data)
		}
	}
	codes := []string{}
	for code := range c.Errors {
		codes = append(codes, code)
	}
	for code := range c.SessionReasons {
		codes = append(codes, code)
	}
	for _, code := range codes {
		if !strings.Contains(source.String(), `"`+code+`"`) {
			t.Errorf("v3 code %s is never emitted", code)
		}
	}
}
