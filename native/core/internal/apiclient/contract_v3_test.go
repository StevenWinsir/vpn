package apiclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// The Core speaks native-v3 (online leases): the shared fixture, also decoded by the API's
// own tests, must fit the Core's types exactly, and every code a client can meet must have
// Chinese text here (plan 8.4; APPs show what the Core gives them).

type v3File struct {
	Version          int                        `json:"contract_version"`
	Cases            map[string]json.RawMessage `json:"cases"`
	ReleaseReasons   []string                   `json:"release_reasons"`
	ConnectionStates []string                   `json:"connection_states"`
	SessionReasons   map[string]string          `json:"session_reasons"`
	Errors           map[string]struct {
		Status int    `json:"status"`
		Where  string `json:"where"`
	} `json:"errors"`
}

func loadV3(t *testing.T) v3File {
	t.Helper()
	data, err := os.ReadFile("../../../../shared/contracts/native-v3.json")
	if err != nil {
		t.Fatal(err)
	}
	var f v3File
	if err = json.Unmarshal(data, &f); err != nil || f.Version != 3 {
		t.Fatalf("contract: %v", err)
	}
	return f
}

func v3Field(t *testing.T, f v3File, name, key string) json.RawMessage {
	t.Helper()
	var c map[string]json.RawMessage
	if err := json.Unmarshal(f.Cases[name], &c); err != nil || c[key] == nil {
		t.Fatalf("case %s has no %s", name, key)
	}
	return c[key]
}

func TestCoreTypesAcceptTheV3Contract(t *testing.T) {
	f := loadV3(t)
	var caps Capabilities
	strictInto(t, v3Field(t, f, "capabilities", "response"), &caps)
	if !slices.Contains(caps.ContractVersions, 3) {
		t.Fatalf("capabilities: %+v", caps)
	}

	var request ConnectionRequest
	strictInto(t, v3Field(t, f, "connection_create", "request"), &request)
	var created ConnectionResponse
	strictInto(t, v3Field(t, f, "connection_create", "response"), &created)
	digest := sha256.Sum256([]byte(created.YAML))
	if created.Connection.GroupID != request.GroupID || created.Connection.State != ConnectionActive || created.Connection.ID != created.Session.ConnectionID ||
		created.Version != hex.EncodeToString(digest[:]) || created.Session.ProfileVersion != created.Version || created.NodeID == "" ||
		created.Session.ConnectionExpiresAt == nil || !created.Session.ConnectionExpiresAt.Equal(created.Connection.ExpiresAt) {
		t.Fatalf("connection_create: %+v", created)
	}

	var release struct {
		Reason string `json:"reason"`
	}
	strictInto(t, v3Field(t, f, "connection_release", "request"), &release)
	var released ReleaseResponse
	strictInto(t, v3Field(t, f, "connection_release", "response"), &released)
	if released.Connection.State != ConnectionEnded || released.Session.ConnectionState != ConnectionEnded || release.Reason != ReleaseUser {
		t.Fatalf("connection_release: %+v", released)
	}

	var expired Session
	strictInto(t, v3Field(t, f, "session_connection_expired", "response"), &expired)
	if expired.Reason != "connection_expired" || expired.CanConnect || expired.ConnectionState != ConnectionExpired {
		t.Fatalf("session_connection_expired: %+v", expired)
	}

	var grantRequest struct {
		NodeID      string `json:"node_id"`
		AvoidNodeID string `json:"avoid_node_id"`
	}
	strictInto(t, v3Field(t, f, "grant_create", "request"), &grantRequest)
	var pending GrantStatus
	strictInto(t, v3Field(t, f, "grant_create", "response"), &pending)
	var ready GrantStatus
	strictInto(t, v3Field(t, f, "grant_ready", "response"), &ready)
	if ready.State != GrantReady || ready.ServerID == "" || ready.NodeID != grantRequest.NodeID || ready.Session == nil || ready.Session.ConnectionID == "" || ready.Session.GrantID != ready.GrantID {
		t.Fatalf("grant_ready: %+v", ready)
	}
	var node Session
	strictInto(t, v3Field(t, f, "session_node_reported", "response"), &node)
	var revoke struct {
		Reason string `json:"reason"`
	}
	strictInto(t, v3Field(t, f, "grant_revoke", "request"), &revoke)
	var revoked GrantStatus
	strictInto(t, v3Field(t, f, "grant_revoke", "response"), &revoked)

	var catalog Catalog
	strictInto(t, v3Field(t, f, "catalog_connection", "response"), &catalog)
	if catalog.Connection == nil || catalog.Connection.State != ConnectionEnded || len(catalog.Nodes) != 1 {
		t.Fatalf("catalog_connection: %+v", catalog)
	}

	ours := []string{ReleaseUser, ReleaseStopped, ReleaseNetworkChanged, ReleaseLifecycle, ReleaseTunnelUnhealthy, ReleaseTunnelFailed}
	slices.Sort(ours)
	theirs := slices.Clone(f.ReleaseReasons)
	slices.Sort(theirs)
	if !slices.Equal(ours, theirs) || !slices.Contains(theirs, revoke.Reason) {
		t.Fatalf("release reasons: Core %v contract %v", ours, theirs)
	}
	if !slices.Equal(f.ConnectionStates, []string{ConnectionActive, ConnectionExpired, ConnectionEnded}) {
		t.Fatalf("connection states: %v", f.ConnectionStates)
	}
}

// Every v3 code and session reason a user can meet has its own Chinese text, and none of
// them ends the login session.
func TestV3ClientFacingCodesHaveMessages(t *testing.T) {
	f := loadV3(t)
	generic := Message("definitely_unknown_code")
	codes := []string{}
	for code := range f.Errors {
		codes = append(codes, code)
	}
	for reason := range f.SessionReasons {
		codes = append(codes, reason)
	}
	for _, code := range codes {
		if code == "invalid_idempotency_key" || code == "invalid_release" {
			continue // the Core never sends a malformed key or reason
		}
		if Message(code) == generic {
			t.Errorf("%s has no localized message", code)
		}
		if TerminatesSession(code) {
			t.Errorf("%s must not log the user out", code)
		}
	}
	for code, text := range map[string]string{
		"device_limit":          "已达到同时在线设备上限，请先断开其他设备",
		"group_full":            "该线路当前已满，请稍后再试或选择其他线路",
		"group_unavailable":     "该线路暂无可用服务器，请选择其他线路",
		"connection_reassigned": "线路服务器已调整，正在为你重新连接",
		"connection_not_found":  "连接记录不存在，请重新连接",
		"connection_expired":    "在线状态已过期，请重新连接",
		"grant_required":        "此线路需使用节点授权连接",
	} {
		if Message(code) != text {
			t.Errorf("%s: %q, plan 8.4 says %q", code, Message(code), text)
		}
	}
}
