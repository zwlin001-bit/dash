package ingest

import (
	"context"
	"fmt"
	"net/http"

	"dash/internal/app"
)

// Module 实现 app.Module 契约，装配时序采集落库核心服务与健康检查端点。
type Module struct {
	service *Service
}

// NewModule 创建 Ingest Module 实例。
func NewModule() *Module {
	return &Module{}
}

func (m *Module) Name() string {
	return "ingest"
}

// Service 返回 Ingest 服务实例。
func (m *Module) Service() *Service {
	return m.service
}

// Register 实现 app.Module 接口，完成依赖注入与路由装配。
func (m *Module) Register(a *app.App) error {
	if a == nil {
		return fmt.Errorf("ingest: app cannot be nil")
	}
	if a.Mux == nil {
		a.Mux = http.NewServeMux()
	}

	m.service = NewService(a.DB, a.Config, nil)
	a.Ingester = m.service

	if a.DB != nil {
		_ = m.service.Start(context.Background())
	}

	return nil
}
