package metrics

import (
	"fmt"
	"net/http"

	"dash/internal/app"
	"dash/internal/ingest"
)

// Module 实现 app.Module 契约，装配时序查询与 SSE 服务。
type Module struct {
	handler *Handler
	store   *LatestStore
}

// NewModule 创建默认 MetricsModule。
func NewModule() *Module {
	store := NewLatestStore()
	return &Module{
		store:   store,
		handler: NewHandler(nil, store),
	}
}

// NewModuleWithStore 创建指定 LatestStore 的 MetricsModule。
func NewModuleWithStore(store *LatestStore) *Module {
	if store == nil {
		store = NewLatestStore()
	}
	return &Module{
		store:   store,
		handler: NewHandler(nil, store),
	}
}

func (m *Module) Name() string {
	return "metrics"
}

// Store 返回关联的内存最新值存储。
func (m *Module) Store() *LatestStore {
	return m.store
}

// Handler 返回关联的 HTTP Handler。
func (m *Module) Handler() *Handler {
	return m.handler
}

// Register 实现 app.Module 接口，注册路由至 a.Mux。
func (m *Module) Register(a *app.App) error {
	if a == nil {
		return fmt.Errorf("metrics: app cannot be nil")
	}
	if a.Mux == nil {
		a.Mux = http.NewServeMux()
	}

	// 若 App 中已注入数据库实例，绑定至 QueryEngine
	if a.DB != nil {
		m.handler.engine = NewQueryEngine(a.DB)
	}

	// 若已注入 Ingester，连接 LatestCache 更新至 SSE 广播
	if a.Ingester != nil {
		if s, ok := a.Ingester.(interface{ Latest() *ingest.LatestCache }); ok {
			s.Latest().OnUpdate(func(nodeID string, l ingest.NodeLatest) {
				m.store.BroadcastMetrics(nodeID, l)
			})
		}
	}

	m.handler.RegisterRoutes(a.Mux)
	return nil
}
