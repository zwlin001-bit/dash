package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"dash/internal/app"
	"dash/internal/jobs"
	"dash/internal/logx"
)

// Store defines the storage methods needed by Handler.
type Store interface {
	ListJobs(ctx context.Context, f jobs.Filter) ([]*jobs.Job, int, error)
	GetJob(ctx context.Context, id string) (*jobs.Job, error)
}

// Registry defines the registry methods needed by Handler.
type Registry interface {
	List() []jobs.JobDefinition
}

// Handler 提供 Job 相关的 HTTP API 处理器。
type Handler struct {
	engine   *jobs.Engine
	store    Store
	registry Registry
}

// NewHandler 创建 Handler 实例。
func NewHandler(engine *jobs.Engine) *Handler {
	return &Handler{engine: engine}
}

// NewHandlerWithStore 创建带独立 Store 与 Registry 的 Handler 实例（供测试与 Fixture 生成）。
func NewHandlerWithStore(store Store, registry Registry) *Handler {
	return &Handler{store: store, registry: registry}
}

func (h *Handler) RegisterAppRoutes(a *app.App) {
	a.HandleAuthed("GET /api/v1/jobs", h.HandleListJobs)
	a.HandleAuthed("POST /api/v1/jobs", h.HandleSubmitJob)
	a.HandleAuthed("GET /api/v1/jobs/kinds", h.HandleListKinds)
	a.HandleAuthed("GET /api/v1/jobs/{id}", h.HandleGetJob)
	a.HandleAuthed("POST /api/v1/jobs/{id}/cancel", h.HandleCancelJob)
	a.HandleAuthed("POST /api/v1/jobs/{id}/retry", h.HandleRetryJob)
	a.HandleAuthed("GET /api/v1/jobs/{id}/stream", h.HandleStream)
}

func (h *Handler) getStore() Store {
	if h.store != nil {
		return h.store
	}
	if h.engine != nil {
		return h.engine.Store()
	}
	return nil
}

func (h *Handler) getRegistry() Registry {
	if h.registry != nil {
		return h.registry
	}
	if h.engine != nil {
		return h.engine.Registry()
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

// HandleListJobs GET /api/v1/jobs
func (h *Handler) HandleListJobs(w http.ResponseWriter, r *http.Request) {
	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "jobs store not ready")
		return
	}

	q := r.URL.Query()
	filter := jobs.Filter{
		Kind:       q.Get("kind"),
		State:      jobs.JobState(q.Get("state")),
		TargetKind: q.Get("target_kind"),
		TargetID:   q.Get("target_id"),
	}
	if limitStr := q.Get("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			filter.Limit = n
		}
	}
	if offsetStr := q.Get("offset"); offsetStr != "" {
		if n, err := strconv.Atoi(offsetStr); err == nil && n >= 0 {
			filter.Offset = n
		}
	}

	items, total, err := st.ListJobs(r.Context(), filter)
	if err != nil {
		logx.Error("failed to list jobs", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to query jobs")
		return
	}

	if items == nil {
		items = []*jobs.Job{}
	}

	page := 1
	pageSize := filter.Limit
	if pageSize <= 0 {
		pageSize = 50
	}
	if pStr := q.Get("page"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			page = p
		}
	} else if filter.Offset > 0 && pageSize > 0 {
		page = (filter.Offset / pageSize) + 1
	}
	if psStr := q.Get("page_size"); psStr != "" {
		if ps, err := strconv.Atoi(psStr); err == nil && ps > 0 {
			pageSize = ps
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// HandleSubmitJob POST /api/v1/jobs
func (h *Handler) HandleSubmitJob(w http.ResponseWriter, r *http.Request) {
	var req jobs.SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Kind == "" {
		writeError(w, http.StatusBadRequest, "field 'kind' is required")
		return
	}

	job, err := h.engine.Submit(r.Context(), req)
	if err != nil {
		if errors.Is(err, jobs.ErrTargetBusy) {
			writeError(w, http.StatusConflict, "target is already busy with another job")
			return
		}
		if errors.Is(err, jobs.ErrUnknownJobKind) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		logx.Error("failed to submit job", "kind", req.Kind, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to submit job")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"job": job,
	})
}

// HandleListKinds GET /api/v1/jobs/kinds
func (h *Handler) HandleListKinds(w http.ResponseWriter, r *http.Request) {
	reg := h.getRegistry()
	if reg == nil {
		writeError(w, http.StatusServiceUnavailable, "jobs registry not ready")
		return
	}
	defs := reg.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"kinds": defs,
	})
}

// HandleGetJob GET /api/v1/jobs/{id}
func (h *Handler) HandleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "job id required")
		return
	}

	st := h.getStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "jobs store not ready")
		return
	}

	job, err := st.GetJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		logx.Error("failed to get job", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to get job")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job": job,
	})
}

// HandleCancelJob POST /api/v1/jobs/{id}/cancel
func (h *Handler) HandleCancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "job id required")
		return
	}

	if err := h.engine.Cancel(r.Context(), id); err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		logx.Error("failed to cancel job", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to cancel job: "+err.Error())
		return
	}

	job, _ := h.engine.Store().GetJob(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{
		"job": job,
	})
}

// HandleRetryJob POST /api/v1/jobs/{id}/retry
func (h *Handler) HandleRetryJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "job id required")
		return
	}

	job, err := h.engine.Retry(r.Context(), id)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		if errors.Is(err, jobs.ErrInvalidState) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		logx.Error("failed to retry job", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to retry job: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job": job,
	})
}

// HandleStream GET /api/v1/jobs/{id}/stream
func (h *Handler) HandleStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "job id required")
		return
	}

	initialJob, err := h.engine.Store().GetJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get job")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return
	}

	// 1. 发送初始完整状态快照
	snapJSON, _ := json.Marshal(initialJob)
	_, _ = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", snapJSON)
	if err := rc.Flush(); err != nil {
		return
	}

	// 若任务已处于终态且没有后续流产生，可继续维持或等待客户端主动断开
	subCh, unsubscribe := h.engine.Broadcaster().Subscribe(id)
	defer unsubscribe()

	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-pingTicker.C:
			if _, err := fmt.Fprintf(w, "event: ping\ndata: {}\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case ev, ok := <-subCh:
			if !ok {
				return
			}
			dataJSON, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, dataJSON); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
