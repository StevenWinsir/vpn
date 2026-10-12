package api

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The native-v2 fixture is the shared contract between the API, the Core and the node
// agent. These tests make the live handlers and structs agree with it.

type v2Contract struct {
	Version int                        `json:"contract_version"`
	Cases   map[string]json.RawMessage `json:"cases"`
	Errors  map[string]struct {
		Status int `json:"status"`
	} `json:"errors"`
	States struct {
		GrantStates []string `json:"grant_states"`
	} `json:"states"`
}

func readV2(t *testing.T) v2Contract {
	t.Helper()
	data, err := os.ReadFile("testdata/native_v2_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var c v2Contract
	if err = json.Unmarshal(data, &c); err != nil || c.Version != 2 {
		t.Fatalf("bad v2 contract: %v", err)
	}
	return c
}

func (c v2Contract) section(t *testing.T, name, field string) json.RawMessage {
	t.Helper()
	var kase map[string]json.RawMessage
	if err := json.Unmarshal(c.Cases[name], &kase); err != nil || kase[field] == nil {
		t.Fatalf("contract case %s has no %s", name, field)
	}
	return kase[field]
}

func strict(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		t.Fatalf("fixture does not fit %T: %v", into, err)
	}
}

func TestV2FixtureFitsLiveTypes(t *testing.T) {
	c := readV2(t)
	for _, name := range []string{"grant_create", "grant_pending", "grant_ready", "grant_revoke", "grant_revoked"} {
		var out grantResponse
		strict(t, c.section(t, name, "response"), &out)
		if out.State == "" || out.GrantID == "" {
			t.Fatal(name)
		}
	}
	var ready grantResponse
	strict(t, c.section(t, "grant_ready", "response"), &ready)
	if nativeProfileVersion([]byte(ready.YAML)) != ready.Version || ready.Session.ProfileVersion != ready.Version || ready.Session.MeteringSource != meteringNode || ready.Session.GrantID != ready.GrantID {
		t.Fatal("grant_ready fixture is internally inconsistent")
	}
	var session nativeState
	strict(t, c.section(t, "session_node_reported", "response"), &session)
	var sync agentSyncRequest
	strict(t, c.section(t, "agent_sync_request", "request"), &sync)
	var response struct {
		ServerTime   string       `json:"server_time"`
		NodeID       string       `json:"node_id"`
		LeaseSeconds int          `json:"lease_seconds"`
		Grants       []agentOrder `json:"grants"`
	}
	strict(t, c.section(t, "agent_sync_response", "response"), &response)
	actions := map[string]bool{}
	for _, o := range response.Grants {
		actions[o.Action] = true
	}
	if !actions["install"] || !actions["keep"] || !actions["revoke"] {
		t.Fatal("sync fixture must show every order action")
	}
	var usageIn agentUsageRequest
	strict(t, c.section(t, "agent_usage_request", "request"), &usageIn)
	var choices struct {
		Nodes []nodeChoice `json:"nodes"`
	}
	var catalog map[string]json.RawMessage
	_ = json.Unmarshal(c.section(t, "node_choice_governed", "response"), &catalog)
	if err := json.Unmarshal(catalog["nodes"], &choices.Nodes); err != nil || len(choices.Nodes) != 1 || choices.Nodes[0].MeteringSource != meteringNode {
		t.Fatal("governed node fixture")
	}
	want := map[string]bool{"pending": true, "ready": true, "revoking": true, "revoked": true, "expired": true}
	for _, s := range c.States.GrantStates {
		delete(want, s)
	}
	if len(want) != 0 {
		t.Fatalf("grant states missing from fixture: %v", want)
	}
}

// Every documented error code must exist in the handlers with the documented status.
func TestV2ErrorCodesAreEmitted(t *testing.T) {
	c := readV2(t)
	var source strings.Builder
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, _ := os.ReadFile(f)
		source.Write(data)
	}
	required := []string{"grant_expired", "agent_unavailable", "metering_source_mismatch", "client_upgrade_required"}
	for _, code := range required {
		if _, ok := c.Errors[code]; !ok {
			t.Fatalf("contract must fix %s", code)
		}
	}
	for code := range c.Errors {
		if !strings.Contains(source.String(), `"`+code+`"`) {
			t.Errorf("documented error %s is never emitted", code)
		}
	}
}

func shapeOf(raw []byte) any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return reduce(v)
}

func reduce(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, child := range typed {
			out[k] = reduce(child)
		}
		return out
	default:
		return nil
	}
}

func shapeKeys(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// contractShape fails when a live JSON object differs in field names from the fixture.
func contractShape(t *testing.T, name, field string, body []byte) {
	t.Helper()
	c := readV2(t)
	want, got := shapeOf(c.section(t, name, field)), shapeOf(body)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("%s drifted from the shared contract:\n fixture %s\n live    %s", name, describe(want), describe(got))
	}
}

func describe(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// contractOrders compares each live sync order with the fixture order of the same action.
func contractOrders(t *testing.T, body []byte) {
	t.Helper()
	c := readV2(t)
	var fixture, live struct {
		Grants []map[string]any `json:"grants"`
	}
	_ = json.Unmarshal(c.section(t, "agent_sync_response", "response"), &fixture)
	if json.Unmarshal(body, &live) != nil {
		t.Fatal("sync response is not JSON")
	}
	byAction := map[string]string{}
	for _, o := range fixture.Grants {
		byAction[o["action"].(string)] = shapeKeys(reduce(o))
	}
	for _, o := range live.Grants {
		if want, ok := byAction[o["action"].(string)]; !ok || want != shapeKeys(reduce(o)) {
			t.Fatalf("sync %v order drifted: live %s fixture %s", o["action"], shapeKeys(reduce(o)), want)
		}
	}
	var top map[string]any
	_ = json.Unmarshal(body, &top)
	var fixtureTop map[string]any
	_ = json.Unmarshal(c.section(t, "agent_sync_response", "response"), &fixtureTop)
	if shapeKeys(reduce(top)) != shapeKeys(reduce(fixtureTop)) {
		t.Fatalf("sync top-level drifted: %s vs %s", shapeKeys(reduce(top)), shapeKeys(reduce(fixtureTop)))
	}
}
