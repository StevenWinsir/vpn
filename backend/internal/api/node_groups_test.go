package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vpn/backend/internal/model"
)

// Round 1: until groups are managed on their own, every server saved through the
// per-server admin API keeps a one-server group with its id in step (D3). The group
// version moves only with public metadata; the admin response shape is unchanged.
func TestAdminNodeSaveMirrorsLegacyGroupPostgres(t *testing.T) {
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog = true
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{29}, 32))
	router := New(db, cfg)
	hash, _ := bcrypt.GenerateFromPassword([]byte("Group-Test-Password!"), 10)
	admin := model.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.invalid", Name: "Group tester", Role: "admin", Status: "active", PasswordHash: string(hash)}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	request := func(method, path, cookie string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", cfg.Origins[0])
		r.Header.Set("Cookie", cookie)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	login := request("POST", "/auth/login", "", loginInput{Email: admin.Email, Password: "Group-Test-Password!"})
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	cookies := []string{}
	for _, cookie := range login.Result().Cookies() {
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
	}
	cookie := strings.Join(cookies, "; ")
	proxy := func(name string, port int) string {
		return fmt.Sprintf("{name: %s, type: ss, server: 127.0.0.1, port: %d, cipher: aes-256-gcm, password: group-secret}", name, port)
	}
	save := func(id string, in nodeInput) []adminNode {
		t.Helper()
		path := "/admin/nodes"
		if id != "" {
			path += "/" + id
		}
		w := request("POST", path, cookie, in)
		if w.Code != 200 && w.Code != 201 {
			t.Fatalf("save: %d %s", w.Code, w.Body.String())
		}
		var out struct {
			Nodes []adminNode `json:"nodes"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Nodes
	}
	group := func(id string) model.NodeGroup {
		t.Helper()
		var g model.NodeGroup
		if err := db.First(&g, "id = ?", id).Error; err != nil {
			t.Fatalf("group %s: %v", id, err)
		}
		return g
	}
	in := nodeInput{YAML: "proxies: [" + proxy("JP1", 18443) + ", " + proxy("JP2", 18444) + "]", Region: "JP", LineType: "direct", RatePermille: 1000, Enabled: true, PlanIDs: []string{"starter"}}
	created := save("", in)
	if len(created) != 2 {
		t.Fatalf("batch import created %d servers", len(created))
	}
	for _, n := range created {
		g := group(n.ID)
		var server model.Node
		db.First(&server, "id = ?", n.ID)
		if server.GroupID == nil || *server.GroupID != n.ID || server.MaxConnections != 1000 {
			t.Fatalf("server not attached to its own group: %+v", server)
		}
		if g.Name != n.Name || g.Region != "JP" || g.LineType != "direct" || g.RatePermille != 1000 || !g.Enabled || g.PlanIDs != `["starter"]` || g.Version != 1 {
			t.Fatalf("group does not mirror its server: %+v", g)
		}
	}
	first := created[0]
	// Connection parameters only: the server version moves, the group version does not.
	in = nodeInput{YAML: "proxies: [" + proxy(first.Name, 19000) + "]", Region: "JP", LineType: "direct", RatePermille: 1000, Enabled: true, PlanIDs: []string{"starter"}, Version: first.Version}
	edited := save(first.ID, in)[0]
	if edited.Version != 2 || group(first.ID).Version != 1 {
		t.Fatalf("YAML-only edit: server v%d group v%d", edited.Version, group(first.ID).Version)
	}
	steps := []func(*nodeInput){
		func(n *nodeInput) { n.RatePermille = 1500 },
		func(n *nodeInput) { n.PlanIDs = []string{"pro", "max"} },
		func(n *nodeInput) { n.Enabled = false },
		func(n *nodeInput) { n.LineType, n.Region = "dedicated", "TW" },
	}
	for i, step := range steps {
		step(&in)
		in.Version = edited.Version
		edited = save(first.ID, in)[0]
		if g := group(first.ID); g.Version != int64(2+i) {
			t.Fatalf("metadata edit %d: group v%d", i, g.Version)
		}
	}
	if g := group(first.ID); g.RatePermille != 1500 || g.PlanIDs != `["max","pro"]` || g.Enabled || g.LineType != "dedicated" || g.Region != "TW" {
		t.Fatalf("group metadata not mirrored: %+v", g)
	}
	// The admin list keeps its exact pre-group shape.
	w := request("GET", "/admin/nodes", cookie, nil)
	var list struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Nodes) != 2 {
		t.Fatalf("list: %s", w.Body.String())
	}
	for _, n := range list.Nodes {
		if len(n) != 8 {
			t.Fatalf("admin node shape changed: %v", n)
		}
		for _, key := range []string{"id", "name", "region", "line_type", "rate_permille", "version", "enabled", "plan_ids"} {
			if _, ok := n[key]; !ok {
				t.Fatalf("admin node lost %s: %v", key, n)
			}
		}
	}
	// Deleting a server removes its now-empty group; the other group stays.
	if w := request("DELETE", "/admin/nodes/"+first.ID, cookie, map[string]int64{"version": edited.Version}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var count int64
	db.Model(&model.NodeGroup{}).Where("id = ?", first.ID).Count(&count)
	if count != 0 {
		t.Fatal("empty group left after deleting its only server")
	}
	group(created[1].ID)
}
