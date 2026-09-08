package alert

import (
	"flag"
	"net/http"

	"dash/internal/alert"
	"dash/internal/app"
	"dash/internal/notify"
)

// Module implements the app.Module contract for alert rules and evaluation engine.
type Module struct {
	engine  *alert.Engine
	store   *alert.Store
	handler *Handler
}

// NewModule creates an alert API module.
func NewModule() *Module {
	return &Module{}
}

func (m *Module) Name() string {
	return "alert"
}

func (m *Module) Register(a *app.App) error {
	m.store = alert.NewStore(a.DB)

	disp := notify.GetDefaultDispatcher()
	cfg := alert.DefaultConfig()

	if ae, ok := a.AlertEngine.(*alert.Engine); ok && ae != nil {
		m.engine = ae
	} else {
		m.engine = alert.NewEngine(a.DB, m.store, disp, cfg)
		a.AlertEngine = m.engine
	}

	// 仅在真实运行阶段启动后台主循环 (单测时显式调用 Evaluate)
	if flag.Lookup("test.v") == nil {
		_ = m.engine.Start()
	}

	m.handler = NewHandler(m.engine, m.store)
	m.registerRoutes(a)

	return nil
}

func (m *Module) registerRoutes(a *app.App) {
	// Rules CRUD
	a.HandleAuthed("GET /api/v1/alerts/rules", m.handler.handleListRules)
	a.HandleAuthed("POST /api/v1/alerts/rules", m.handler.handleCreateRule)
	a.HandleAuthed("GET /api/v1/alerts/rules/{id}", m.handler.handleGetRule)
	a.HandleAuthed("PUT /api/v1/alerts/rules/{id}", m.handler.handleUpdateRule)
	a.HandleAuthed("DELETE /api/v1/alerts/rules/{id}", m.handler.handleDeleteRule)

	// Events and active alerts
	a.HandleAuthed("GET /api/v1/alerts/events", m.handler.handleListEvents)
	a.HandleAuthed("GET /api/v1/alerts/active", m.handler.handleGetActiveAlerts)

	// Manual evaluation trigger
	a.HandleAuthed("POST /api/v1/alerts/eval", m.handler.handleTriggerEval)
}

// RegisterRoutes registers routes directly to an http.ServeMux (useful for testing).
func RegisterRoutes(mux *http.ServeMux, eng *alert.Engine, st *alert.Store) {
	h := NewHandler(eng, st)
	mux.HandleFunc("GET /api/v1/alerts/rules", h.handleListRules)
	mux.HandleFunc("POST /api/v1/alerts/rules", h.handleCreateRule)
	mux.HandleFunc("GET /api/v1/alerts/rules/{id}", h.handleGetRule)
	mux.HandleFunc("PUT /api/v1/alerts/rules/{id}", h.handleUpdateRule)
	mux.HandleFunc("DELETE /api/v1/alerts/rules/{id}", h.handleDeleteRule)
	mux.HandleFunc("GET /api/v1/alerts/events", h.handleListEvents)
	mux.HandleFunc("GET /api/v1/alerts/active", h.handleGetActiveAlerts)
	mux.HandleFunc("POST /api/v1/alerts/eval", h.handleTriggerEval)
}

// Engine returns the module's alert engine instance.
func (m *Module) Engine() *alert.Engine {
	return m.engine
}

// Store returns the module's alert store instance.
func (m *Module) Store() *alert.Store {
	return m.store
}
