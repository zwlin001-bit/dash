package events

import (
	"encoding/json"
	"net/http"
	"strconv"

	"dash/internal/app"
	"dash/internal/events"
)

// Handler 封装事件相关的 HTTP API 处理器。
type Handler struct {
	store *events.Store
}

// NewHandler 创建事件 API Handler。
func NewHandler(s *events.Store) *Handler {
	return &Handler{store: s}
}

// RegisterAppRoutes 注册事件相关 API 到 App（以鉴权方式注册）。
func RegisterAppRoutes(a *app.App, s *events.Store) {
	h := NewHandler(s)

	a.HandleAuthed("GET /api/v1/events", h.handleListEvents)
	a.HandleAuthed("POST /api/v1/events/read", h.handleMarkRead)
	a.HandleAuthed("GET /api/v1/events/unread-count", h.handleUnreadCount)
	a.HandleAuthed("GET /api/v1/event-types", h.handleListTypes)
	a.HandleAuthed("PATCH /api/v1/event-types/{event_type}", h.handleUpdateType)
}

// RegisterRoutes 注册事件相关 API 到 http.ServeMux (12-api-spec.md §7)。
func RegisterRoutes(mux *http.ServeMux, s *events.Store) {
	h := NewHandler(s)

	mux.HandleFunc("GET /api/v1/events", h.handleListEvents)
	mux.HandleFunc("POST /api/v1/events/read", h.handleMarkRead)
	mux.HandleFunc("GET /api/v1/events/unread-count", h.handleUnreadCount)
	mux.HandleFunc("GET /api/v1/event-types", h.handleListTypes)
	mux.HandleFunc("PATCH /api/v1/event-types/{event_type}", h.handleUpdateType)
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

// handleListEvents: GET /api/v1/events
func (h *Handler) handleListEvents(w http.ResponseWriter, r *http.Request) {
	st := h.store
	if st == nil {
		st = events.GetDefaultStore()
	}
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "events store not ready")
		return
	}

	q := r.URL.Query()
	filter := events.Filter{
		EventType: q.Get("event_type"),
		Severity:  q.Get("severity"),
		TargetID:  q.Get("target_id"),
	}

	if isReadStr := q.Get("is_read"); isReadStr != "" {
		isRead := (isReadStr == "true" || isReadStr == "1")
		filter.IsRead = &isRead
	}

	if fromStr := q.Get("from_ms"); fromStr != "" {
		if v, err := strconv.ParseInt(fromStr, 10, 64); err == nil {
			filter.FromMs = v
		}
	}
	if toStr := q.Get("to_ms"); toStr != "" {
		if v, err := strconv.ParseInt(toStr, 10, 64); err == nil {
			filter.ToMs = v
		}
	}

	limit := 50
	if limitStr := q.Get("limit"); limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}
	filter.Limit = limit

	offset := 0
	if offsetStr := q.Get("offset"); offsetStr != "" {
		if v, err := strconv.Atoi(offsetStr); err == nil && v >= 0 {
			offset = v
		}
	}
	filter.Offset = offset

	items, total, err := st.ListEvents(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if items == nil {
		items = []events.EventRecord{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// handleMarkRead: POST /api/v1/events/read
func (h *Handler) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	st := h.store
	if st == nil {
		st = events.GetDefaultStore()
	}
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "events store not ready")
		return
	}

	var req events.MarkReadRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	affected, err := st.MarkRead(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"affected": affected,
	})
}

// handleUnreadCount: GET /api/v1/events/unread-count
func (h *Handler) handleUnreadCount(w http.ResponseWriter, r *http.Request) {
	st := h.store
	if st == nil {
		st = events.GetDefaultStore()
	}
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "events store not ready")
		return
	}

	count, err := st.GetUnreadCount(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"unread_count": count,
	})
}

// handleListTypes: GET /api/v1/event-types
func (h *Handler) handleListTypes(w http.ResponseWriter, r *http.Request) {
	st := h.store
	if st == nil {
		st = events.GetDefaultStore()
	}
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "events store not ready")
		return
	}

	types, err := st.ListEventTypes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if types == nil {
		types = []events.TypeDef{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": types,
	})
}

// handleUpdateType: PATCH /api/v1/event-types/{event_type}
func (h *Handler) handleUpdateType(w http.ResponseWriter, r *http.Request) {
	st := h.store
	if st == nil {
		st = events.GetDefaultStore()
	}
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "events store not ready")
		return
	}

	eventType := r.PathValue("event_type")
	if eventType == "" {
		writeError(w, http.StatusBadRequest, "event_type is required")
		return
	}

	var req struct {
		Severity    string `json:"severity"`
		Disposition string `json:"disposition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updated, err := st.UpdateEventType(r.Context(), eventType, req.Severity, req.Disposition)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, updated)
}
