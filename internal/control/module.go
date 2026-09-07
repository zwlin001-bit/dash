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
		// 免鉴权白名单：Agent 首次入网注册接口，通过一次性 enrollment_token 鉴权
		a.HandlePublic("/api/agent/v1/enroll", NewEnrollHandler(a.DB).ServeHTTP)
		// 免鉴权白名单：Agent WebSocket RPC 长连接，升级前通过 Authorization: Bearer <agent_token> 鉴权
		a.HandlePublic("/api/agent/v1/rpc", wsH.ServeHTTP)
		// 免鉴权白名单：Agent HTTP 上报降级通道，请求体携带 agent 鉴权信息
		a.HandlePublic("/api/agent/v1/report", fbH.ServeHTTP)
	}

	installH := NewInstallScriptHandler(a.Config, a.DB)
	dlH := NewDownloadHandler(a.Config)
	// 免鉴权白名单：公开装机脚本下载
	a.HandlePublic("GET /install.sh", installH.ServeHTTP)
	// 免鉴权白名单：Agent 各架构二进制产物公开下载
	a.HandlePublic("GET /dl/{filename}", dlH.ServeHTTP)
	a.HandlePublic("GET /dl/", dlH.ServeHTTP)

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
