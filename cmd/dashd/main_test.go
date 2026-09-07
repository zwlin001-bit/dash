package main

import (
	"errors"
	"testing"

	"dash/internal/app"
	"dash/internal/config"
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

func TestRegisterModules_Order(t *testing.T) {
	expectedNames := []string{
		"auth",
		"inventory",
		"ingest",
		"control",
		"metrics",
		"events",
		"settings",
		"web",
	}

	if len(modules) != len(expectedNames) {
		t.Fatalf("expected %d modules, got %d", len(expectedNames), len(modules))
	}

	for i, mod := range modules {
		if mod.Name() != expectedNames[i] {
			t.Errorf("module at index %d: expected %s, got %s", i, expectedNames[i], mod.Name())
		}
	}
}

func TestRegisterModules_Execution(t *testing.T) {
	cfg := config.DefaultConfig()
	a := app.NewApp(nil, cfg, "test-version")

	if err := RegisterModules(a, modules); err != nil {
		t.Fatalf("failed to register modules: %v", err)
	}

	if a.Ingester == nil {
		t.Error("expected a.Ingester to be set after registering modules")
	}
	if a.Registry == nil {
		t.Error("expected a.Registry to be set after registering modules")
	}
}
