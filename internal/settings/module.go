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

	var reg *control.Registry
	if r, ok := a.Registry.(*control.Registry); ok {
		reg = r
	}
	m.service = NewService(a.DB, reg)
	m.RegisterAppRoutes(a)
	return nil
}

// RegisterAppRoutes 注册系统设置 API 路由到 App（以鉴权方式注册）。
func (m *SettingsModule) RegisterAppRoutes(a *app.App) {
	a.HandleAuthed("GET /api/v1/settings", m.service.HandleGet)
	a.HandleAuthed("PATCH /api/v1/settings", m.service.HandlePatch)
}

func (m *SettingsModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/settings", m.service.HandleGet)
	mux.HandleFunc("PATCH /api/v1/settings", m.service.HandlePatch)
}
