package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockModule struct {
	name       string
	registerFn func(*App) error
}

func (m *mockModule) Name() string {
	return m.name
}

func (m *mockModule) Register(app *App) error {
	if m.registerFn != nil {
		return m.registerFn(app)
	}
	return nil
}

func TestRegisterModules_Success(t *testing.T) {
	app := &App{}
	var executed []string
	mods := []Module{
		&mockModule{
			name: "mod1",
			registerFn: func(a *App) error {
				executed = append(executed, "mod1")
				return nil
			},
		},
		&mockModule{
			name: "mod2",
			registerFn: func(a *App) error {
				executed = append(executed, "mod2")
				return nil
			},
		},
	}

	if err := RegisterModules(app, mods); err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if len(executed) != 2 || executed[0] != "mod1" || executed[1] != "mod2" {
		t.Fatalf("unexpected execution order: %v", executed)
	}
}

func TestRegisterModules_Failure(t *testing.T) {
	app := &App{}
	expectedErr := errors.New("init error")
	mods := []Module{
		&mockModule{
			name: "fail-mod",
			registerFn: func(a *App) error {
				return expectedErr
			},
		},
	}

	err := RegisterModules(app, mods)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRegisterModules_RealModules(t *testing.T) {
	app := &App{
		Mux:     nil,
		Config:  nil,
		Version: "test-ver",
	}

	if err := RegisterModules(app, modules); err != nil {
		t.Fatalf("failed to register real modules: %v", err)
	}

	if app.Mux == nil {
		t.Fatal("expected app.Mux to be initialized")
	}

	// 验证 /healthz 正常挂载并响应
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	app.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusOK {
		t.Fatalf("unexpected /healthz status code: %d", rec.Code)
	}

	// 验证 /api/v1/settings 正常挂载，不再返回 404 endpoint not found
	recSettings := httptest.NewRecorder()
	reqSettings := httptest.NewRequest("GET", "/api/v1/settings", nil)
	app.Mux.ServeHTTP(recSettings, reqSettings)
	if recSettings.Code != http.StatusOK {
		t.Fatalf("expected /api/v1/settings status 200, got %d (body: %s)", recSettings.Code, recSettings.Body.String())
	}
}
