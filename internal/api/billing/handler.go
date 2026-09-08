package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"dash/internal/alert"
	"dash/internal/billing"
	"dash/internal/jobs"
)

// Handler handles HTTP requests for billing overview, items, and budgets CRUD.
type Handler struct {
	svc         *billing.Service
	jobEngine   *jobs.Engine
	alertEngine *alert.Engine
}

// NewHandler creates a new billing API handler.
func NewHandler(svc *billing.Service, je *jobs.Engine, ae *alert.Engine) *Handler {
	return &Handler{
		svc:         svc,
		jobEngine:   je,
		alertEngine: ae,
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// handleGetOverview GET /api/v1/billing/overview
func (h *Handler) handleGetOverview(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "billing service not initialized")
		return
	}
	period := r.URL.Query().Get("period")
	res, err := h.svc.GetOverview(r.Context(), period)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleGetItems GET /api/v1/billing/items
func (h *Handler) handleGetItems(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "billing service not initialized")
		return
	}
	period := r.URL.Query().Get("period")
	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = "kind"
	}
	accountID := r.URL.Query().Get("account_id")

	res, err := h.svc.GetItems(r.Context(), period, groupBy, accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleListBudgets GET /api/v1/billing/budgets
func (h *Handler) handleListBudgets(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "billing service not initialized")
		return
	}
	st := h.svc.Store()
	budgets, err := st.ListBudgets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if budgets == nil {
		budgets = []*billing.BillBudget{}
	}

	// Calculate current spent and progress for each budget in current month
	currentPeriod := time.Now().UTC().Format("2006-01")
	overview, _ := h.svc.GetOverview(r.Context(), currentPeriod)
	currSpentMap := make(map[string]float64)
	if overview != nil {
		for _, t := range overview.Totals {
			currSpentMap[t.Currency] = t.TotalAmount
		}
	}

	for _, b := range budgets {
		if b.ScopeKind == "all" {
			spent := currSpentMap[b.Currency]
			b.CurrentSpent = spent
			if b.Amount > 0 {
				b.Progress = spent / b.Amount
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": budgets,
	})
}

// handleCreateBudget POST /api/v1/billing/budgets
func (h *Handler) handleCreateBudget(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "billing service not initialized")
		return
	}

	var b billing.BillBudget
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if b.Currency == "" {
		b.Currency = "CNY"
	}
	if b.ScopeKind == "" {
		b.ScopeKind = "all"
	}
	if b.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be greater than 0")
		return
	}
	if b.WarnRatio <= 0 {
		b.WarnRatio = 0.8
	}

	st := h.svc.Store()
	if err := st.CreateBudget(r.Context(), &b); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Trigger budget evaluation
	if h.alertEngine != nil {
		_ = h.alertEngine.EvaluateKind(r.Context(), alert.RuleKindBudget)
	}

	writeJSON(w, http.StatusCreated, b)
}

// handleGetBudget GET /api/v1/billing/budgets/{id}
func (h *Handler) handleGetBudget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing budget id")
		return
	}

	st := h.svc.Store()
	b, err := st.GetBudget(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if b == nil {
		writeError(w, http.StatusNotFound, "budget not found")
		return
	}

	writeJSON(w, http.StatusOK, b)
}

// handleUpdateBudget PUT /api/v1/billing/budgets/{id}
func (h *Handler) handleUpdateBudget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing budget id")
		return
	}

	st := h.svc.Store()
	existing, err := st.GetBudget(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "budget not found")
		return
	}

	var req billing.BillBudget
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.ID = id
	if req.ScopeKind == "" {
		req.ScopeKind = existing.ScopeKind
	}
	if req.Currency == "" {
		req.Currency = existing.Currency
	}
	if req.Amount <= 0 {
		req.Amount = existing.Amount
	}
	if req.WarnRatio <= 0 {
		req.WarnRatio = existing.WarnRatio
	}

	if err := st.UpdateBudget(r.Context(), &req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Trigger budget evaluation
	if h.alertEngine != nil {
		_ = h.alertEngine.EvaluateKind(r.Context(), alert.RuleKindBudget)
	}

	writeJSON(w, http.StatusOK, req)
}

// handleDeleteBudget DELETE /api/v1/billing/budgets/{id}
func (h *Handler) handleDeleteBudget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing budget id")
		return
	}

	st := h.svc.Store()
	if err := st.DeleteBudget(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTriggerSync POST /api/v1/billing/sync
func (h *Handler) handleTriggerSync(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "billing service not ready")
		return
	}

	accountID := r.URL.Query().Get("account_id")

	// If JobEngine is available, submit a background job
	if h.jobEngine != nil {
		job, err := h.jobEngine.Submit(r.Context(), jobs.SubmitRequest{
			Kind:       billing.JobKindBillSync,
			TargetKind: "billing",
			TargetID:   accountID,
			Params: map[string]any{
				"account_id":      accountID,
				"backfill_months": 12,
			},
			CreatedBy: "api",
		})
		if err == nil {
			writeJSON(w, http.StatusAccepted, map[string]any{
				"job_id": job.ID,
				"status": "pending",
			})
			return
		}
	}

	// Fallback to direct background execution
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if accountID != "" {
			_ = h.svc.SyncAccount(ctx, accountID, 12)
		} else {
			_ = h.svc.SyncAll(ctx, 12)
		}
	}()

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "sync initiated",
		"status":  "syncing",
	})
}

// handleTriggerEval POST /api/v1/billing/eval
func (h *Handler) handleTriggerEval(w http.ResponseWriter, r *http.Request) {
	if h.alertEngine == nil {
		writeError(w, http.StatusServiceUnavailable, "alert engine not available")
		return
	}

	err := h.alertEngine.EvaluateKind(r.Context(), alert.RuleKindBudget)
	if err != nil && !errors.Is(err, context.Canceled) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "budget evaluation completed",
		"ok":      true,
	})
}
