package events_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	eventsapi "dash/internal/api/events"
	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/migrate"
)

func setupTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("Skipping test: MySQL test DB not accessible: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	_, _ = d.Exec(ctx, "SELECT GET_LOCK('dash_events_test', 30)")
	t.Cleanup(func() {
		_, _ = d.Exec(context.Background(), "SELECT RELEASE_LOCK('dash_events_test')")
	})

	mig := migrate.New(d, "../../../migrations")
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("migration up failed: %v", err)
	}

	_, _ = d.Exec(ctx, "DELETE FROM events")
	_, _ = d.Exec(ctx, "DELETE FROM event_types")

	return d
}

func TestEventsAPI(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store := events.NewStore(d, 50)
	store.Start()
	defer store.Stop()
	oldStore := events.GetDefaultStore()
	defer events.SetDefaultStore(oldStore)
	events.SetDefaultStore(store)

	if err := store.SyncBuiltinTypes(ctx); err != nil {
		t.Fatalf("SyncBuiltinTypes failed: %v", err)
	}

	mux := http.NewServeMux()
	eventsapi.RegisterRoutes(mux, store)

	// 1. Emit an event
	events.Emit(ctx, events.Event{
		Type:       "node.offline",
		Source:     "control",
		TargetKind: "node",
		TargetID:   "01J00000000000000000000099",
		Title:      "节点离线测试",
		Payload: map[string]any{
			"node_name": "test-node",
		},
	})

	// 2. Test GET /api/v1/events with polling for drain
	var listResp struct {
		Items []events.EventRecord `json:"items"`
		Total int                  `json:"total"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest("GET", "/api/v1/events?event_type=node.offline", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			_ = json.Unmarshal(rec.Body.Bytes(), &listResp)
			if listResp.Total == 1 && len(listResp.Items) == 1 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if listResp.Total != 1 || len(listResp.Items) != 1 {
		t.Fatalf("expected 1 item, got total=%d len=%d", listResp.Total, len(listResp.Items))
	}
	eventID := listResp.Items[0].ID

	// 3. Test GET /api/v1/events/unread-count
	req := httptest.NewRequest("GET", "/api/v1/events/unread-count", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/events/unread-count returned status %d", rec.Code)
	}
	var unreadResp struct {
		UnreadCount int64 `json:"unread_count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &unreadResp)
	if unreadResp.UnreadCount != 1 {
		t.Fatalf("expected unread_count=1, got %d", unreadResp.UnreadCount)
	}

	// 4. Test POST /api/v1/events/read
	readBody, _ := json.Marshal(map[string]any{
		"ids": []string{eventID},
	})
	req = httptest.NewRequest("POST", "/api/v1/events/read", bytes.NewReader(readBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/events/read returned status %d", rec.Code)
	}

	// Verify unread-count is now 0
	req = httptest.NewRequest("GET", "/api/v1/events/unread-count", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &unreadResp)
	if unreadResp.UnreadCount != 0 {
		t.Fatalf("expected unread_count=0 after mark read, got %d", unreadResp.UnreadCount)
	}

	// 5. Test GET /api/v1/event-types
	req = httptest.NewRequest("GET", "/api/v1/event-types", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/event-types returned status %d", rec.Code)
	}
	var typesResp struct {
		Items []events.TypeDef `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &typesResp)
	if len(typesResp.Items) < 6 {
		t.Fatalf("expected at least 6 types, got %d", len(typesResp.Items))
	}

	// 6. Test PATCH /api/v1/event-types/{event_type}
	patchBody, _ := json.Marshal(map[string]any{
		"disposition": "drop",
		"severity":    "critical",
	})
	req = httptest.NewRequest("PATCH", "/api/v1/event-types/node.online", bytes.NewReader(patchBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/v1/event-types/node.online returned %d, body: %s", rec.Code, rec.Body.String())
	}
	var updatedType events.TypeDef
	_ = json.Unmarshal(rec.Body.Bytes(), &updatedType)
	if updatedType.Disposition != "drop" || updatedType.Severity != "critical" {
		t.Fatalf("unexpected updated type: %+v", updatedType)
	}
}
