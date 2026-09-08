package alert

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"dash/internal/alert"
	"dash/internal/db"
)

// Handler handles HTTP API requests for alert rules and alert events.
type Handler struct {
	engine *alert.Engine
	store  *alert.Store
}

// NewHandler creates a new alert API Handler.
func NewHandler(eng *alert.Engine, st *alert.Store) *Handler {
	return &Handler{
		engine: eng,
		store:  st,
	}
}

func (h *Handler) getStore() *alert.Store {
	if h.store != nil {
		return h.store
	}
	if h.engine != nil {
		return h.engine.Store()
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": message,
	})
}

// Rules CRUD

func (h *Handler) handleListRules(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "alert store not ready")
		return
	}

	rules, err := st.ListRules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rules == nil {
		rules = []*alert.AlertRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": rules,
	})
}

func (h *Handler) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "alert store not ready")
		return
	}

	var rule alert.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if rule.Name == "" {
		writeError(w, http.StatusBadRequest, "rule name is required")
		return
	}
	if rule.RuleKind == "" {
		writeError(w, http.StatusBadRequest, "rule_kind is required")
		return
	}

	if err := st.CreateRule(r.Context(), &rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, rule)
}

func (h *Handler) handleGetRule(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "alert store not ready")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	rule, err := st.GetRule(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, rule)
}

func (h *Handler) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "alert store not ready")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	var rule alert.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule.ID = id

	if err := st.UpdateRule(r.Context(), &rule); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, rule)
}

func (h *Handler) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "alert store not ready")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	if err := st.DeleteRule(r.Context(), id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Events Query

func (h *Handler) handleListEvents(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "alert store not ready")
		return
	}

	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	fromMs, _ := strconv.ParseInt(q.Get("from_ms"), 10, 64)
	toMs, _ := strconv.ParseInt(q.Get("to_ms"), 10, 64)

	filter := alert.EventFilter{
		RuleID: q.Get("rule_id"),
		NodeID: q.Get("node_id"),
		State:  q.Get("state"),
		FromMs: fromMs,
		ToMs:   toMs,
		Limit:  limit,
		Offset: offset,
	}

	items, total, err := st.ListEvents(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*alert.AlertEvent{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": total,
	})
}

func (h *Handler) handleGetActiveAlerts(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "alert store not ready")
		return
	}

	items, err := st.GetActiveFiringEvents(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*alert.AlertEvent{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

func (h *Handler) handleTriggerEval(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "alert engine not running")
		return
	}

	if err := h.engine.EvaluateAll(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "告警评估已执行",
	})
}
