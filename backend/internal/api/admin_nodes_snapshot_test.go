package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

func TestAdminNodeDetailConsistentSnapshotPostgres(t *testing.T) {
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32))
	node := model.Node{ID: uuid.NewString(), Name: "original", Region: "HK", LineType: "direct", RatePermille: 500, Version: 1, Enabled: true}
	oldYAML := "proxies: [{name: original, type: socks5, server: 127.0.0.1, port: 1080}]"
	newYAML := strings.ReplaceAll(oldYAML, "original", "updated")
	encrypt := func(yaml string) string {
		t.Helper()
		value, err := nodes.Encrypt(cfg.NodeEncryptionKey, node.ID, []byte(yaml))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	secret := model.NodeConfig{NodeID: node.ID, Ciphertext: encrypt(oldYAML), PlanIDs: "[]"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Omit("Node").Create(&secret).Error; err != nil {
		t.Fatal(err)
	}
	newCiphertext := encrypt(newYAML)
	type snapshotReader struct{}
	callback := "test:concurrent_node_edit"
	interleaved := false
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Context.Value(snapshotReader{}) != true || tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Node" || interleaved {
			return
		}
		interleaved = true
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := db.WithContext(ctx).Transaction(func(writer *gorm.DB) error {
			if e := writer.Model(&model.Node{}).Where("id = ?", node.ID).Updates(map[string]any{"name": "updated", "version": 2, "rate_permille": 1000}).Error; e != nil {
				return e
			}
			return writer.Model(&model.NodeConfig{}).Where("node_id = ?", node.ID).Updates(map[string]any{"ciphertext": newCiphertext, "plan_ids": `["pro"]`}).Error
		})
		if err != nil {
			tx.AddError(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Query().Remove(callback) })
	server := &Server{db: db, cfg: cfg}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("GET", "/api/v1/admin/nodes/"+node.ID, nil).WithContext(context.WithValue(context.Background(), snapshotReader{}, true))
	c.Params = gin.Params{{Key: "id", Value: node.ID}}
	server.adminNodeDetail(c)
	if response.Code != 200 || !interleaved {
		t.Fatalf("snapshot request failed: status=%d interleaved=%v", response.Code, interleaved)
	}
	var result adminNode
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Version != 1 || result.Name != "original" || result.RatePermille != 500 || result.YAML != oldYAML || len(result.PlanIDs) != 0 {
		t.Fatal("node metadata, version, plan permissions and YAML came from different committed versions")
	}
	if err := db.First(&node, "id = ?", node.ID).Error; err != nil || node.Version != 2 {
		t.Fatal("concurrent administrator update did not commit")
	}
}
