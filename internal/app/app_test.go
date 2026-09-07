package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dash/internal/app"
	"dash/internal/config"
)

type mockRegistry struct {
	count int
}

func (m *mockRegistry) OnlineCount() int {
	return m.count
}

type mockIngester struct {
	droppedBatches uint64
	droppedRows    uint64
	stopped        bool
}

func (m *mockIngester) DroppedBatches() uint64 {
	return m.droppedBatches
}

func (m *mockIngester) DroppedRows() uint64 {
	return m.droppedRows
}

func (m *mockIngester) Stop() {
	m.stopped = true
}

func TestApp_Lifecycle(t *testing.T) {
	cfg := config.DefaultConfig()
	a := app.NewApp(nil, cfg, "v1.2.3")

	if a.Mux == nil {
		t.Fatal("expected non-nil Mux")
	}
	if a.Version != "v1.2.3" {
		t.Fatalf("expected version v1.2.3, got %s", a.Version)
	}

	ing := &mockIngester{droppedBatches: 5, droppedRows: 42}
	a.Ingester = ing

	a.FlushIngest()
	if !ing.stopped {
		t.Error("expected Ingester.Stop() to be called on FlushIngest")
	}

	if err := a.Close(); err != nil {
		t.Fatalf("unexpected error closing app: %v", err)
	}
}

func TestApp_HealthzHandler_DBNil(t *testing.T) {
	cfg := config.DefaultConfig()
	a := app.NewApp(nil, cfg, "test-ver")

	reg := &mockRegistry{count: 3}
	ing := &mockIngester{droppedBatches: 2, droppedRows: 10}
	a.Registry = reg
	a.Ingester = ing

	h := a.HealthzHandler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when DB is nil, got %d", rec.Code)
	}

	var data map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	if data["db"] != "error" {
		t.Errorf("expected db: error, got %v", data["db"])
	}
	if data["status"] != "error" {
		t.Errorf("expected status: error, got %v", data["status"])
	}
	if data["version"] != "test-ver" {
		t.Errorf("expected version: test-ver, got %v", data["version"])
	}
	if data["agents_online"] != float64(3) {
		t.Errorf("expected agents_online: 3, got %v", data["agents_online"])
	}
	if data["dropped_batches"] != float64(2) {
		t.Errorf("expected dropped_batches: 2, got %v", data["dropped_batches"])
	}
	if data["dropped_rows"] != float64(10) {
		t.Errorf("expected dropped_rows: 10, got %v", data["dropped_rows"])
	}
}

func TestApp_HealthzHandler_MethodNotAllowed(t *testing.T) {
	a := app.NewApp(nil, config.DefaultConfig(), "test-ver")
	h := a.HealthzHandler()

	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 Method Not Allowed for POST /healthz, got %d", rec.Code)
	}
}
