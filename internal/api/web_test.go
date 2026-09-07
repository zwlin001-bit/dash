package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dash/internal/api"
	"dash/internal/app"
)

func TestWebHandler_RootAndSPAFallback(t *testing.T) {
	handler := api.Handler()

	routes := []struct {
		name string
		path string
	}{
		{"root", "/"},
		{"login", "/login"},
		{"nodes", "/nodes"},
		{"node_detail", "/nodes/node-123"},
		{"machines", "/machines"},
		{"settings", "/settings"},
		{"deep_unknown", "/some/random/spa/route"},
	}

	for _, tc := range routes {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			resp := w.Result()
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected status 200 for %s, got %d", tc.path, resp.StatusCode)
			}

			contentType := resp.Header.Get("Content-Type")
			if !strings.Contains(contentType, "text/html") {
				t.Errorf("expected text/html for %s, got %s", tc.path, contentType)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read response body: %v", err)
			}

			bodyStr := string(body)
			if !strings.Contains(bodyStr, `id="root"`) {
				t.Errorf("expected index.html body to contain id=\"root\", got: %s", bodyStr)
			}
		})
	}
}

func TestWebHandler_APINotFound(t *testing.T) {
	handler := api.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for API route, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("expected application/json for API 404, got %s", contentType)
	}
}

func TestWebModule_Register(t *testing.T) {
	t.Setenv("DASH_TEST_NO_SERVE", "1")

	mod := api.NewModule()
	if mod.Name() != "web" {
		t.Errorf("expected module name 'web', got '%s'", mod.Name())
	}

	a := &app.App{}
	if err := mod.Register(a); err != nil {
		t.Fatalf("unexpected error registering web module: %v", err)
	}

	if a.Mux == nil {
		t.Fatal("expected app.Mux to be initialized by web module")
	}

	// Verify Mux responds
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	a.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}
