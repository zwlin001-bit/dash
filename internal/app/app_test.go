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

func TestApp_HandleAuthed_DefaultReject(t *testing.T) {
	a := app.NewApp(nil, config.DefaultConfig(), "test-ver")

	called := false
	a.HandleAuthed("GET /api/v1/protected", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected", nil)
	rec := httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when auth middleware not set, got %d", rec.Code)
	}
	if called {
		t.Fatal("handler should not have been called")
	}

	var errResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to decode json error body: %v", err)
	}
	errObj, ok := errResp["error"].(map[string]any)
	if !ok || errObj["code"] != "unauthorized" {
		t.Fatalf("expected error.code = unauthorized, got %v", errResp)
	}
}

func TestApp_HandleAuthed_WithAuthMiddleware(t *testing.T) {
	a := app.NewApp(nil, config.DefaultConfig(), "test-ver")

	// Set a mock middleware that checks for a special header
	a.SetAuthMiddleware(func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Auth") != "allowed" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"unauthorized"}}`))
				return
			}
			next(w, r)
		}
	})

	called := false
	a.HandleAuthed("GET /api/v1/test", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Unauthorized request
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	rec1 := httptest.NewRecorder()
	a.Mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec1.Code)
	}
	if called {
		t.Fatal("handler should not be called")
	}

	// Authorized request
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req2.Header.Set("X-Test-Auth", "allowed")
	rec2 := httptest.NewRecorder()
	a.Mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec2.Code)
	}
	if !called {
		t.Fatal("handler should have been called")
	}
}

func TestApp_HandlePublic(t *testing.T) {
	a := app.NewApp(nil, config.DefaultConfig(), "test-ver")

	called := false
	a.HandlePublic("GET /public-endpoint", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/public-endpoint", nil)
	rec := httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Fatal("handler should have been called")
	}

	routes := a.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Pattern != "GET /public-endpoint" || routes[0].Authed {
		t.Fatalf("unexpected route info: %+v", routes[0])
	}
}

func TestApp_HealthzHandler_DevNoAuth(t *testing.T) {
	// 1. 常规模式：dev_no_auth 未开启
	cfgNormal := config.DefaultConfig()
	cfgNormal.Server.DevNoAuth = false
	aNormal := app.NewApp(nil, cfgNormal, "test-ver")

	req1 := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec1 := httptest.NewRecorder()
	aNormal.HealthzHandler()(rec1, req1)

	var respNormal map[string]any
	if err := json.NewDecoder(rec1.Body).Decode(&respNormal); err != nil {
		t.Fatalf("decode healthz failed: %v", err)
	}
	if _, exists := respNormal["dev_no_auth"]; exists {
		t.Fatalf("expected dev_no_auth to be omitted when false, got %v", respNormal["dev_no_auth"])
	}

	// 2. 开启模式：dev_no_auth = true
	cfgDev := config.DefaultConfig()
	cfgDev.Server.DevNoAuth = true
	aDev := app.NewApp(nil, cfgDev, "test-ver")

	req2 := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec2 := httptest.NewRecorder()
	aDev.HealthzHandler()(rec2, req2)

	var respDev map[string]any
	if err := json.NewDecoder(rec2.Body).Decode(&respDev); err != nil {
		t.Fatalf("decode healthz failed: %v", err)
	}
	if val, ok := respDev["dev_no_auth"].(bool); !ok || !val {
		t.Fatalf("expected dev_no_auth: true in healthz response, got %v", respDev["dev_no_auth"])
	}
}

