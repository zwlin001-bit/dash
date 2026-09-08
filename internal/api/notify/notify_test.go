package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dash/internal/crypto"
	"dash/internal/db"
	"dash/internal/migrate"
	"dash/internal/notify"
	_ "github.com/go-sql-driver/mysql"
)

func setupTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("skipping test: cannot connect to MySQL on 33306: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mig := migrate.New(d, "../../../migrations")
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("migration up failed: %v", err)
	}

	_, _ = d.Exec(ctx, "DELETE FROM notify_deliveries")
	_, _ = d.Exec(ctx, "DELETE FROM notify_rules")
	_, _ = d.Exec(ctx, "DELETE FROM notify_channels")
	_, _ = d.Exec(ctx, "DELETE FROM credentials")

	return d
}

func setupTestCrypto(t *testing.T) *crypto.Manager {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	mgr, err := crypto.NewManagerWithKey(key)
	if err != nil {
		t.Fatalf("setup crypto failed: %v", err)
	}
	return mgr
}

func TestChannelsCRUDAndMasking(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	cryptoMgr := setupTestCrypto(t)

	store := notify.NewStore(database, cryptoMgr)
	engine := notify.NewTemplateEngine("")
	dispatcher := notify.NewDispatcher(store, engine, "dash.example.com", 100)

	mux := http.NewServeMux()
	RegisterRoutes(mux, store, dispatcher)

	// 1. Create channel
	createBody := map[string]any{
		"name":         "Main TG Bot",
		"channel_kind": "telegram",
		"secret":       "123456:BOT_TOKEN_XYZ",
		"config_json":  `{"chat_id":"-100123456"}`,
	}
	bodyBytes, _ := json.Marshal(createBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notify/channels", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var createdChannel notify.Channel
	_ = json.Unmarshal(w.Body.Bytes(), &createdChannel)

	if createdChannel.ID == "" {
		t.Fatal("expected non-empty channel ID")
	}
	if createdChannel.MaskedSecret == "123456:BOT_TOKEN_XYZ" {
		t.Fatal("secret was not masked in response!")
	}
	if !strings.HasPrefix(createdChannel.MaskedSecret, "••••") {
		t.Fatalf("expected masked secret starting with ••••, got: %s", createdChannel.MaskedSecret)
	}

	// 2. List channels
	req = httptest.NewRequest(http.MethodGet, "/api/v1/notify/channels", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var listResp struct {
		Items []*notify.Channel `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Items) != 1 {
		t.Fatalf("expected 1 channel in list, got %d", len(listResp.Items))
	}
	if listResp.Items[0].MaskedSecret == "123456:BOT_TOKEN_XYZ" {
		t.Fatal("plaintext token leaked in channel list response")
	}

	// 3. Update channel without secret (preserves existing secret)
	updateBody := map[string]any{
		"name":        "Updated TG Bot Name",
		"is_enabled":  true,
		"config_json": `{"chat_id":"-100999999"}`,
		"secret":      "", // Leave empty
	}
	uBytes, _ := json.Marshal(updateBody)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/notify/channels/"+createdChannel.ID, bytes.NewReader(uBytes))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	// Verify decrypted secret is still the original secret
	sec, err := store.GetDecryptedSecret(context.Background(), createdChannel.CredentialID)
	if err != nil || sec != "123456:BOT_TOKEN_XYZ" {
		t.Fatalf("original secret was lost after update: %s, err=%v", sec, err)
	}

	// 4. Delete channel
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/notify/channels/"+createdChannel.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	// Verify channel is deleted
	req = httptest.NewRequest(http.MethodGet, "/api/v1/notify/channels/"+createdChannel.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w.Code)
	}
}

func TestRulesCRUD(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	cryptoMgr := setupTestCrypto(t)

	store := notify.NewStore(database, cryptoMgr)
	engine := notify.NewTemplateEngine("")
	dispatcher := notify.NewDispatcher(store, engine, "dash.example.com", 100)

	mux := http.NewServeMux()
	RegisterRoutes(mux, store, dispatcher)

	// Create channel for rule target
	ch, _ := store.CreateChannel(context.Background(), "Target Ch", "telegram", "tok", `{"chat_id":"1"}`)

	// 1. Create rule
	ruleBody := map[string]any{
		"name":              "Rule 1",
		"is_enabled":        true,
		"event_pattern":     "node.*",
		"min_severity":      "warning",
		"notify_channel_id": ch.ID,
		"throttle_s":        120,
	}
	rBytes, _ := json.Marshal(ruleBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notify/rules", bytes.NewReader(rBytes))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var createdRule notify.Rule
	_ = json.Unmarshal(w.Body.Bytes(), &createdRule)
	if createdRule.ID == "" {
		t.Fatal("expected rule ID")
	}

	// 2. List rules
	req = httptest.NewRequest(http.MethodGet, "/api/v1/notify/rules", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	var listResp struct {
		Items []*notify.Rule `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Items) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(listResp.Items))
	}

	// 3. Update rule
	createdRule.MinSeverity = "critical"
	uBytes, _ := json.Marshal(createdRule)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/notify/rules/"+createdRule.ID, bytes.NewReader(uBytes))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Delete rule
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/notify/rules/"+createdRule.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
}

func TestDeliveriesList(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	cryptoMgr := setupTestCrypto(t)

	store := notify.NewStore(database, cryptoMgr)
	engine := notify.NewTemplateEngine("")
	dispatcher := notify.NewDispatcher(store, engine, "dash.example.com", 100)

	mux := http.NewServeMux()
	RegisterRoutes(mux, store, dispatcher)

	// Insert test delivery
	_ = store.CreateDelivery(context.Background(), &notify.Delivery{
		EventID:      "01J_EV1",
		ChannelID:    "01J_CH1",
		State:        "sent",
		Attempt:      1,
		RenderedText: "sample rendered text",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notify/deliveries?limit=10", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Items []*notify.Delivery `json:"items"`
		Total int                `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Fatalf("expected 1 delivery, got total=%d, items=%d", resp.Total, len(resp.Items))
	}
	if resp.Items[0].State != "sent" {
		t.Fatalf("expected state sent, got %s", resp.Items[0].State)
	}
	if resp.Items[0].RenderedText != "sample rendered text" {
		t.Fatalf("expected rendered text, got %s", resp.Items[0].RenderedText)
	}
}
