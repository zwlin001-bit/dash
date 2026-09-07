package settings

import (
	"fmt"
	"net/http"

	"dash/internal/app"
	"dash/internal/control"
)

// SettingsModule 实现 app.Module 接口，注册系统设置相关端点。
type SettingsModule struct {
	service *Service
}

func NewModule() *SettingsModule {
	return &SettingsModule{}
}

func (m *SettingsModule) Name() string {
	return "settings"
}

func (m *SettingsModule) Service() *Service {
	return m.service
}

func (m *SettingsModule) Register(a *app.App) error {
	if a == nil {
		return fmt.Errorf("settings: app cannot be nil")
	}
	if a.Mux == nil {
		a.Mux = http.NewServeMux()
	}

	var reg *control.Registry
	if r, ok := a.Registry.(*control.Registry); ok {
		reg = r
	}
	m.service = NewService(a.DB, reg)
	m.RegisterRoutes(a.Mux)
	return nil
}

func (m *SettingsModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/settings", m.service.HandleGet)
	mux.HandleFunc("PATCH /api/v1/settings", m.service.HandlePatch)
}
