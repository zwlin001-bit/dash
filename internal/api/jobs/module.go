package jobs

import (
	"context"

	"dash/internal/app"
	"dash/internal/jobs"
	"dash/internal/logx"
)

// Module 实现 app.Module 契约。
type Module struct {
	engine   *jobs.Engine
	registry *jobs.Registry
	handler  *Handler
	cfg      jobs.Config
}

// NewModule 创建 Module 实例。
func NewModule(cfg ...jobs.Config) *Module {
	c := jobs.DefaultConfig()
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return &Module{
		cfg: c,
	}
}

func (m *Module) Name() string {
	return "jobs"
}

func (m *Module) Engine() *jobs.Engine {
	return m.engine
}

func (m *Module) Registry() *jobs.Registry {
	return m.registry
}

func (m *Module) Handler() *Handler {
	return m.handler
}

func (m *Module) Register(a *app.App) error {
	m.registry = jobs.NewRegistry()
	m.engine = jobs.NewEngine(a.DB, m.registry, m.cfg)
	m.handler = NewHandler(m.engine)

	a.JobEngine = m.engine

	// 注册控制台鉴权路由
	a.HandleAuthed("GET /api/v1/jobs", m.handler.HandleListJobs)
	a.HandleAuthed("POST /api/v1/jobs", m.handler.HandleSubmitJob)
	a.HandleAuthed("GET /api/v1/jobs/kinds", m.handler.HandleListKinds)
	a.HandleAuthed("GET /api/v1/jobs/{id}", m.handler.HandleGetJob)
	a.HandleAuthed("POST /api/v1/jobs/{id}/cancel", m.handler.HandleCancelJob)
	a.HandleAuthed("POST /api/v1/jobs/{id}/retry", m.handler.HandleRetryJob)
	a.HandleAuthed("GET /api/v1/jobs/{id}/stream", m.handler.HandleStream)

	// 启动引擎后台调度与崩溃恢复
	if a.DB != nil {
		if err := m.engine.Start(context.Background()); err != nil {
			logx.Error("failed to start jobs engine", "err", err)
			return err
		}
	}

	return nil
}

// Stop 停止 Job 引擎。
func (m *Module) Stop() {
	if m.engine != nil {
		m.engine.Stop()
	}
}
