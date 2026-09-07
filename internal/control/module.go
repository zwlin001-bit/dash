package control

import (
	"context"
	"fmt"
	"net/http"

	"dash/internal/app"
	"dash/internal/config"
	"dash/internal/db"
)

// ControlModule 实现 app.Module 接口，负责 Agent 接入端点注册与连接管理。
type ControlModule struct {
	Registry *Registry
}

// NewModule 创建 ControlModule 实例。
func NewModule() *ControlModule {
	return &ControlModule{}
}

func (m *ControlModule) Name() string {
	return "control"
}

func (m *ControlModule) Register(a *app.App) error {
	if a == nil {
		return fmt.Errorf("control: app cannot be nil")
	}
	if a.Mux == nil {
		a.Mux = http.NewServeMux()
	}

	m.Registry = NewRegistry(a.DB, nil)
	a.Registry = m.Registry
	if a.DB != nil {
		m.Registry.Start(context.Background())
		wsH := NewWSHandler(a.DB, m.Registry, a.Config)
		fbH := NewFallbackHandler(a.DB, m.Registry)
		if a.Ingester != nil {
			if ing, ok := a.Ingester.(MetricsIngester); ok {
				wsH.SetIngester(ing)
				fbH.SetIngester(ing)
			}
		}
		a.Mux.Handle("/api/agent/v1/enroll", NewEnrollHandler(a.DB))
		a.Mux.Handle("/api/agent/v1/rpc", wsH)
		a.Mux.Handle("/api/agent/v1/report", fbH)
	}

	installH := NewInstallScriptHandler(a.Config, a.DB)
	dlH := NewDownloadHandler(a.Config)
	a.Mux.Handle("GET /install.sh", installH)
	a.Mux.Handle("GET /dl/{filename}", dlH)
	a.Mux.Handle("GET /dl/", dlH)

	return nil
}

// RegisterRoutes 显式将 Agent 协议接入端点注册至 ServeMux。
func RegisterRoutes(mux *http.ServeMux, database *db.DB, registry *Registry, cfg *config.Config) {
	mux.Handle("/api/agent/v1/enroll", NewEnrollHandler(database))
	mux.Handle("/api/agent/v1/rpc", NewWSHandler(database, registry, cfg))
	mux.Handle("/api/agent/v1/report", NewFallbackHandler(database, registry))
	installH := NewInstallScriptHandler(cfg, database)
	dlH := NewDownloadHandler(cfg)
	mux.Handle("GET /install.sh", installH)
	mux.Handle("GET /dl/{filename}", dlH)
	mux.Handle("GET /dl/", dlH)
}
