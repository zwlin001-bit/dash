package billing

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"dash/internal/alert"
	"dash/internal/app"
	"dash/internal/billing"
	"dash/internal/credentials"
	"dash/internal/crypto"
	"dash/internal/jobs"
	"dash/internal/provider"
)

// Module implements the app.Module interface for billing.
type Module struct {
	svc     *billing.Service
	handler *Handler
}

// NewModule creates a new billing Module.
func NewModule() *Module {
	return &Module{}
}

func (m *Module) Name() string {
	return "billing"
}

func (m *Module) Register(a *app.App) error {
	masterKeyPath := "/etc/dash/master.key"
	if a.Config != nil && a.Config.Server.MasterKey != "" {
		masterKeyPath = a.Config.Server.MasterKey
	}

	masterKey, err := crypto.LoadMasterKey(masterKeyPath)
	if err != nil {
		if flag.Lookup("test.v") != nil || (a.Config != nil && a.Config.Server.DevNoAuth) || os.Getenv("DASH_ENV") == "dev" {
			masterKey = make([]byte, 32)
		} else {
			return fmt.Errorf("billing module: failed to load master key: %w", err)
		}
	}

	credStore := credentials.NewStore(a.DB, masterKey)
	pm := provider.NewManager(filepath.Join(os.TempDir(), "dash-runtime"), "bin", "/usr/local/bin")

	var jobEng *jobs.Engine
	if je, ok := a.JobEngine.(*jobs.Engine); ok {
		jobEng = je
	}

	var alertEng *alert.Engine
	if ae, ok := a.AlertEngine.(*alert.Engine); ok {
		alertEng = ae
	}

	store := billing.NewStore(a.DB)
	m.svc = billing.NewService(a.DB, store, credStore, pm, alertEng)

	if jobEng != nil {
		billing.RegisterBillingJobs(jobEng.Registry(), m.svc, alertEng)
	}

	m.handler = NewHandler(m.svc, jobEng, alertEng)
	m.registerRoutes(a)

	return nil
}

func (m *Module) registerRoutes(a *app.App) {
	// Overview & Items
	a.HandleAuthed("GET /api/v1/billing/overview", m.handler.handleGetOverview)
	a.HandleAuthed("GET /api/v1/billing/items", m.handler.handleGetItems)

	// Budgets CRUD
	a.HandleAuthed("GET /api/v1/billing/budgets", m.handler.handleListBudgets)
	a.HandleAuthed("POST /api/v1/billing/budgets", m.handler.handleCreateBudget)
	a.HandleAuthed("GET /api/v1/billing/budgets/{id}", m.handler.handleGetBudget)
	a.HandleAuthed("PUT /api/v1/billing/budgets/{id}", m.handler.handleUpdateBudget)
	a.HandleAuthed("DELETE /api/v1/billing/budgets/{id}", m.handler.handleDeleteBudget)

	// Actions
	a.HandleAuthed("POST /api/v1/billing/sync", m.handler.handleTriggerSync)
	a.HandleAuthed("POST /api/v1/billing/eval", m.handler.handleTriggerEval)
}

// RegisterRoutes registers billing routes directly onto an http.ServeMux (useful for testing).
func RegisterRoutes(mux *http.ServeMux, svc *billing.Service, je *jobs.Engine, ae *alert.Engine) {
	h := NewHandler(svc, je, ae)
	mux.HandleFunc("GET /api/v1/billing/overview", h.handleGetOverview)
	mux.HandleFunc("GET /api/v1/billing/items", h.handleGetItems)
	mux.HandleFunc("GET /api/v1/billing/budgets", h.handleListBudgets)
	mux.HandleFunc("POST /api/v1/billing/budgets", h.handleCreateBudget)
	mux.HandleFunc("GET /api/v1/billing/budgets/{id}", h.handleGetBudget)
	mux.HandleFunc("PUT /api/v1/billing/budgets/{id}", h.handleUpdateBudget)
	mux.HandleFunc("DELETE /api/v1/billing/budgets/{id}", h.handleDeleteBudget)
	mux.HandleFunc("POST /api/v1/billing/sync", h.handleTriggerSync)
	mux.HandleFunc("POST /api/v1/billing/eval", h.handleTriggerEval)
}

// Service returns the module's billing service.
func (m *Module) Service() *billing.Service {
	return m.svc
}
