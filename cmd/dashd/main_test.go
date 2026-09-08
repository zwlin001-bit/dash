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
		"jobs",
		"notify",
		"settings",
		"cloud",
		"guard",
		"alert",
		"billing",
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
	if a.JobEngine == nil {
		t.Error("expected a.JobEngine to be set after registering modules")
	}
}

func TestParseCLIArgs(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantSubcmd   string
		wantCleanLen int
	}{
		{"empty", []string{}, "", 0},
		{"serve only", []string{"serve"}, "serve", 0},
		{"migrate only", []string{"migrate"}, "migrate", 0},
		{"serve with flags after", []string{"serve", "-config", "test.toml"}, "serve", 2},
		{"serve with flags before", []string{"-config", "test.toml", "serve"}, "serve", 2},
		{"migrate with flags after", []string{"migrate", "-config", "test.toml"}, "migrate", 2},
		{"migrate with flags before", []string{"-config", "test.toml", "migrate"}, "migrate", 2},
		{"flags only defaults to empty subcmd", []string{"-config", "test.toml"}, "", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subcmd, clean := parseCLIArgs(tt.args)
			if subcmd != tt.wantSubcmd {
				t.Errorf("got subcmd %q, want %q", subcmd, tt.wantSubcmd)
			}
			if len(clean) != tt.wantCleanLen {
				t.Errorf("got clean args len %d, want %d", len(clean), tt.wantCleanLen)
			}
		})
	}
}
