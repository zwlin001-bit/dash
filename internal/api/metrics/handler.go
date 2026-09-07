package metrics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dash/internal/db"
)

// Handler 封装时序查询与最新值/SSE 相关的 HTTP 端点。
type Handler struct {
	engine *QueryEngine
	store  *LatestStore
}

// NewHandler 创建 Handler 实例。
func NewHandler(database *db.DB, store *LatestStore) *Handler {
	if store == nil {
		store = NewLatestStore()
	}
	return &Handler{
		engine: NewQueryEngine(database),
		store:  store,
	}
}

// Store 返回关联的内存最新值缓存。
func (h *Handler) Store() *LatestStore {
	return h.store
}

// Engine 返回关联的时序查询引擎。
func (h *Handler) Engine() *QueryEngine {
	return h.engine
}

// RegisterRoutes 将时序查询与 SSE 路由注册到指定 ServeMux。
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/nodes/{id}/metrics", h.HandleMetrics)
	mux.HandleFunc("GET /api/v1/nodes/{id}/latest", h.HandleLatest)
	mux.HandleFunc("GET /api/v1/metrics/stream", h.HandleStream)
	// 同时兼容 docs/12-api-spec.md §6 路径
	mux.HandleFunc("GET /api/v1/stream", h.HandleStream)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ApiErrorResponse{
		Error: ApiError{
			Code:    code,
			Message: msg,
		},
	})
}

func extractNodeID(r *http.Request) string {
	if id := r.PathValue("id"); id != "" {
		return id
	}
	// 针对未通过 Go 1.22 模式匹配（如自建测试）时的 URL 路径提取兜底
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	for i, part := range parts {
		if part == "nodes" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// HandleMetrics 处理 GET /api/v1/nodes/{id}/metrics。
func (h *Handler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	nodeID := extractNodeID(r)
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "missing node id in path")
		return
	}

	q := r.URL.Query()

	// 兼容 from_ms 与 from
	fromStr := q.Get("from_ms")
	if fromStr == "" {
		fromStr = q.Get("from")
	}
	toStr := q.Get("to_ms")
	if toStr == "" {
		toStr = q.Get("to")
	}

	if fromStr == "" || toStr == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "from_ms and to_ms are required query parameters")
		return
	}

	fromMs, err := strconv.ParseInt(fromStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid from_ms integer")
		return
	}

	toMs, err := strconv.ParseInt(toStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid to_ms integer")
		return
	}

	if toMs <= fromMs {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "to_ms must be greater than from_ms")
		return
	}

	maxPoints := 400
	if mpStr := q.Get("max_points"); mpStr != "" {
		if mp, err := strconv.Atoi(mpStr); err == nil && mp > 0 {
			maxPoints = mp
		}
	}

	fieldsParam := q.Get("fields")

	resp, err := h.engine.QueryMetrics(r.Context(), nodeID, fromMs, toMs, fieldsParam, maxPoints)
	if err != nil {
		writeError(w, http.StatusBadRequest, "QUERY_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleLatest 处理 GET /api/v1/nodes/{id}/latest（读内存最新值缓存）。
func (h *Handler) HandleLatest(w http.ResponseWriter, r *http.Request) {
	nodeID := extractNodeID(r)
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "missing node id in path")
		return
	}

	latest, ok := h.store.GetLatest(nodeID)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("node %q latest metrics not found in memory cache", nodeID))
		return
	}

	writeJSON(w, http.StatusOK, latest)
}

// HandleStream 处理 GET /api/v1/metrics/stream 及 /api/v1/stream（SSE 实时流推送）。
func (h *Handler) HandleStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	subCh, unsubscribe := h.store.Subscribe()
	defer unsubscribe()

	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return
	}

	// 25 秒保活 ping（P1-13 规格）
	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case <-pingTicker.C:
			_, err := fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
			if err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}

		case ev, ok := <-subCh:
			if !ok {
				return
			}
			_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, ev.Data)
			if err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
