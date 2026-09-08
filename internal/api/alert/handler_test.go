package alert_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dash/internal/alert"
	apiAlert "dash/internal/api/alert"
	"dash/internal/db"
	"dash/internal/migrate"
	"dash/internal/ulid"
)

func setupTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("skipping test, MySQL 33306 not available: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, _ = d.Exec(ctx, "SELECT GET_LOCK('dash_alert_test', 60)")
	t.Cleanup(func() {
		_, _ = d.Exec(context.Background(), "SELECT RELEASE_LOCK('dash_alert_test')")
	})

	m := migrate.New(d, "../../../migrations")
	if err := m.Up(ctx); err != nil {
		t.Fatalf("run migrations failed: %v", err)
	}

	tables := []string{
		"alert_rule_channels", "alert_events", "alert_rules",
		"sample_host_1m", "sample_host_1h", "sample_host", "nodes",
	}
	for _, tbl := range tables {
		_, _ = d.Exec(ctx, fmt.Sprintf("DELETE FROM %s", tbl))
	}

	return d
}

func TestAlertRulesAPI_CRUD(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	store := alert.NewStore(database)
	engine := alert.NewEngine(database, store, nil, alert.DefaultConfig())

	mux := http.NewServeMux()
	apiAlert.RegisterRoutes(mux, engine, store)

	// 1. Create Rule (POST /api/v1/alerts/rules)
	rulePayload := map[string]any{
		"name":        "Test High CPU",
		"is_enabled":  true,
		"rule_kind":   "metric",
		"scope_kind":  "all",
		"metric_code": "cpu_pct",
		"compare_op":  "gt",
		"threshold":   85.5,
		"duration_s":  60,
		"severity":    "warning",
		"silence_s":   1800,
		"channel_ids": []string{"ch-test-1", "ch-test-2"},
	}
	body, _ := json.Marshal(rulePayload)
	req := httptest.NewRequest("POST", "/api/v1/alerts/rules", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/alerts/rules expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created alert.AlertRule
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created rule failed: %v", err)
	}
	if created.ID == "" || created.Name != "Test High CPU" {
		t.Fatalf("unexpected created rule: %+v", created)
	}
	if len(created.ChannelIDs) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(created.ChannelIDs))
	}

	// 2. Get Rule (GET /api/v1/alerts/rules/{id})
	req = httptest.NewRequest("GET", "/api/v1/alerts/rules/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET rule expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var fetched alert.AlertRule
	_ = json.Unmarshal(w.Body.Bytes(), &fetched)
	if fetched.ID != created.ID || fetched.Threshold != 85.5 {
		t.Fatalf("unexpected fetched rule: %+v", fetched)
	}

	// 3. List Rules (GET /api/v1/alerts/rules)
	req = httptest.NewRequest("GET", "/api/v1/alerts/rules", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/alerts/rules expected 200, got %d", w.Code)
	}
	var listResp struct {
		Items []*alert.AlertRule `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Items) < 1 {
		t.Fatalf("expected at least 1 rule in list, got %d", len(listResp.Items))
	}

	// 4. Update Rule (PUT /api/v1/alerts/rules/{id})
	created.Name = "Updated High CPU"
	created.Threshold = 95.0
	created.ChannelIDs = []string{"ch-test-1"}
	body, _ = json.Marshal(created)
	req = httptest.NewRequest("PUT", "/api/v1/alerts/rules/"+created.ID, bytes.NewReader(body))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PUT rule expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify updated
	updated, _ := store.GetRule(context.Background(), created.ID)
	if updated.Name != "Updated High CPU" || updated.Threshold != 95.0 || len(updated.ChannelIDs) != 1 {
		t.Fatalf("update verification failed: %+v", updated)
	}

	// 5. Delete Rule (DELETE /api/v1/alerts/rules/{id})
	req = httptest.NewRequest("DELETE", "/api/v1/alerts/rules/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("DELETE rule expected 200, got %d", w.Code)
	}

	// Verify deletion
	_, err := store.GetRule(context.Background(), created.ID)
	if err != db.ErrNotFound {
		t.Fatalf("expected ErrNotFound after deletion, got %v", err)
	}
}

func TestAlertEventsAPI_Query(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	ctx := context.Background()
	store := alert.NewStore(database)
	engine := alert.NewEngine(database, store, nil, alert.DefaultConfig())

	mux := http.NewServeMux()
	apiAlert.RegisterRoutes(mux, engine, store)

	nodeID := ulid.New()
	ruleID := ulid.New()
	nowMs := time.Now().UnixMilli()

	_, _ = database.Exec(ctx, "INSERT INTO nodes (id, name, conn_state, created_at_ms, updated_at_ms) VALUES (?, 'node-api-test', 'online', ?, ?)",
		nodeID, nowMs, nowMs)
	_ = store.CreateRule(ctx, &alert.AlertRule{
		ID:        ruleID,
		Name:      "API Event Rule",
		IsEnabled: true,
		RuleKind:  "metric",
		CompareOp: "gt",
		Threshold: 90,
	})

	// Create 1 firing event and 1 resolved event
	ev1 := &alert.AlertEvent{
		ID:          ulid.New(),
		AlertRuleID: ruleID,
		NodeID:      nodeID,
		EventState:  alert.EventStateFiring,
		FiredAtMs:   nowMs - 10000,
		CreatedAtMs: nowMs - 10000,
	}
	_ = store.CreateEvent(ctx, ev1)

	resMs := nowMs
	ev2 := &alert.AlertEvent{
		ID:           ulid.New(),
		AlertRuleID:  ruleID,
		NodeID:       nodeID,
		EventState:   alert.EventStateResolved,
		FiredAtMs:    nowMs - 20000,
		ResolvedAtMs: &resMs,
		CreatedAtMs:  nowMs - 20000,
	}
	_ = store.CreateEvent(ctx, ev2)

	// 1. GET /api/v1/alerts/events (all events)
	req := httptest.NewRequest("GET", "/api/v1/alerts/events", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET events expected 200, got %d", w.Code)
	}
	var resp struct {
		Items []*alert.AlertEvent `json:"items"`
		Total int                 `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Fatalf("expected 2 events, got total=%d items=%d", resp.Total, len(resp.Items))
	}

	// 2. GET /api/v1/alerts/events?state=firing
	req = httptest.NewRequest("GET", "/api/v1/alerts/events?state=firing", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 1 || resp.Items[0].EventState != alert.EventStateFiring {
		t.Fatalf("expected 1 firing event, got total=%d items=%d", resp.Total, len(resp.Items))
	}

	// 3. GET /api/v1/alerts/active
	req = httptest.NewRequest("GET", "/api/v1/alerts/active", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/alerts/active expected 200, got %d", w.Code)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 1 || resp.Items[0].ID != ev1.ID {
		t.Fatalf("expected ev1 in active alerts, got %+v", resp.Items)
	}
}
