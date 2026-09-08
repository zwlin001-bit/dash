package notify

import (
	"encoding/json"
	"net/http"
	"strconv"

	"dash/internal/app"
	"dash/internal/notify"
)

// Handler handles HTTP API requests for notification channels, rules and deliveries.
type Handler struct {
	store      *notify.Store
	dispatcher *notify.Dispatcher
}

// NewHandler creates an API Handler.
func NewHandler(s *notify.Store, d *notify.Dispatcher) *Handler {
	return &Handler{
		store:      s,
		dispatcher: d,
	}
}

func (h *Handler) getStore() *notify.Store {
	if h.store != nil {
		return h.store
	}
	return notify.GetDefaultStore()
}

func (h *Handler) getDispatcher() *notify.Dispatcher {
	if h.dispatcher != nil {
		return h.dispatcher
	}
	return notify.GetDefaultDispatcher()
}

// RegisterAppRoutes registers notification APIs to App with authentication.
func RegisterAppRoutes(a *app.App, s *notify.Store, d *notify.Dispatcher) {
	h := NewHandler(s, d)

	// Channels
	a.HandleAuthed("GET /api/v1/notify/channels", h.handleListChannels)
	a.HandleAuthed("POST /api/v1/notify/channels", h.handleCreateChannel)
	a.HandleAuthed("GET /api/v1/notify/channels/{id}", h.handleGetChannel)
	a.HandleAuthed("PUT /api/v1/notify/channels/{id}", h.handleUpdateChannel)
	a.HandleAuthed("DELETE /api/v1/notify/channels/{id}", h.handleDeleteChannel)
	a.HandleAuthed("POST /api/v1/notify/channels/{id}/test", h.handleTestChannel)

	// Rules
	a.HandleAuthed("GET /api/v1/notify/rules", h.handleListRules)
	a.HandleAuthed("POST /api/v1/notify/rules", h.handleCreateRule)
	a.HandleAuthed("GET /api/v1/notify/rules/{id}", h.handleGetRule)
	a.HandleAuthed("PUT /api/v1/notify/rules/{id}", h.handleUpdateRule)
	a.HandleAuthed("DELETE /api/v1/notify/rules/{id}", h.handleDeleteRule)

	// Deliveries
	a.HandleAuthed("GET /api/v1/notify/deliveries", h.handleListDeliveries)
}

// RegisterRoutes registers routes directly to an http.ServeMux (useful for testing).
func RegisterRoutes(mux *http.ServeMux, s *notify.Store, d *notify.Dispatcher) {
	h := NewHandler(s, d)

	// Channels
	mux.HandleFunc("GET /api/v1/notify/channels", h.handleListChannels)
	mux.HandleFunc("POST /api/v1/notify/channels", h.handleCreateChannel)
	mux.HandleFunc("GET /api/v1/notify/channels/{id}", h.handleGetChannel)
	mux.HandleFunc("PUT /api/v1/notify/channels/{id}", h.handleUpdateChannel)
	mux.HandleFunc("DELETE /api/v1/notify/channels/{id}", h.handleDeleteChannel)
	mux.HandleFunc("POST /api/v1/notify/channels/{id}/test", h.handleTestChannel)

	// Rules
	mux.HandleFunc("GET /api/v1/notify/rules", h.handleListRules)
	mux.HandleFunc("POST /api/v1/notify/rules", h.handleCreateRule)
	mux.HandleFunc("GET /api/v1/notify/rules/{id}", h.handleGetRule)
	mux.HandleFunc("PUT /api/v1/notify/rules/{id}", h.handleUpdateRule)
	mux.HandleFunc("DELETE /api/v1/notify/rules/{id}", h.handleDeleteRule)

	// Deliveries
	mux.HandleFunc("GET /api/v1/notify/deliveries", h.handleListDeliveries)
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

// Channels

func (h *Handler) handleListChannels(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "notify store not ready")
		return
	}
	items, err := st.ListChannels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*notify.Channel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
	})
}

type createChannelReq struct {
	Name       string `json:"name"`
	Kind       string `json:"channel_kind"`
	Secret     string `json:"secret"`
	ConfigJSON string `json:"config_json"`
}

func (h *Handler) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "notify store not ready")
		return
	}
	var req createChannelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ch, err := st.CreateChannel(r.Context(), req.Name, req.Kind, req.Secret, req.ConfigJSON)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ch)
}

func (h *Handler) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "notify store not ready")
		return
	}
	id := r.PathValue("id")
	ch, err := st.GetChannel(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

type updateChannelReq struct {
	Name       string `json:"name"`
	IsEnabled  bool   `json:"is_enabled"`
	Secret     string `json:"secret"`
	ConfigJSON string `json:"config_json"`
}

func (h *Handler) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "notify store not ready")
		return
	}
	id := r.PathValue("id")
	var req updateChannelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ch, err := st.UpdateChannel(r.Context(), id, req.Name, req.IsEnabled, req.Secret, req.ConfigJSON)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (h *Handler) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "notify store not ready")
		return
	}
	id := r.PathValue("id")
	if err := st.DeleteChannel(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type testChannelReq struct {
	Text string `json:"text"`
}

func (h *Handler) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	disp := h.getDispatcher()
	if disp == nil {
		writeError(w, http.StatusServiceUnavailable, "dispatcher not initialized")
		return
	}
	id := r.PathValue("id")
	var req testChannelReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if err := disp.TestSendChannel(r.Context(), id, req.Text); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "测试消息发送成功",
	})
}

// Rules

func (h *Handler) handleListRules(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "notify store not ready")
		return
	}
	items, err := st.ListRules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*notify.Rule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
	})
}

func (h *Handler) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "notify store not ready")
		return
	}
	var rule notify.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	created, err := st.CreateRule(r.Context(), &rule)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleGetRule(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "notify store not ready")
		return
	}
	id := r.PathValue("id")
	rule, err := st.GetRule(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *Handler) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "notify store not ready")
		return
	}
	id := r.PathValue("id")
	var rule notify.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule.ID = id

	updated, err := st.UpdateRule(r.Context(), &rule)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "notify store not ready")
		return
	}
	id := r.PathValue("id")
	if err := st.DeleteRule(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Deliveries

func (h *Handler) handleListDeliveries(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "notify store not ready")
		return
	}
	q := r.URL.Query()
	filter := notify.DeliveryFilter{
		EventID:   q.Get("event_id"),
		ChannelID: q.Get("channel_id"),
		State:     q.Get("state"),
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

	items, total, err := st.ListDeliveries(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*notify.Delivery{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}
