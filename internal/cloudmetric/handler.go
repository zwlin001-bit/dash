package cloudmetric

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dash/internal/db"
	"dash/internal/logx"
)

// Handler handles HTTP requests for cloud metrics.
type Handler struct {
	db     *db.DB
	store  *Store
	syncer *Syncer
}

// NewHandler creates a new Handler.
func NewHandler(d *db.DB, store *Store, syncer *Syncer) *Handler {
	return &Handler{
		db:     d,
		store:  store,
		syncer: syncer,
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": logx.Redact(msg),
		},
	})
}

// HandleResourceMetrics handles GET /api/v1/cloud-resources/{id}/metrics.
func (h *Handler) HandleResourceMetrics(w http.ResponseWriter, r *http.Request) {
	resID := r.PathValue("id")
	if resID == "" {
		// Fallback path parsing
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		for i, part := range parts {
			if part == "cloud-resources" && i+1 < len(parts) {
				resID = parts[i+1]
				break
			}
		}
	}
	if resID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "resource id is required")
		return
	}

	var accountID, resRef string
	err := h.db.QueryRow(r.Context(), "SELECT cloud_account_id, res_ref FROM cloud_resources WHERE id = ?", resID).Scan(&accountID, &resRef)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "cloud resource not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	q := r.URL.Query()
	now := time.Now().UnixMilli()

	fromMs := now - 6*3600*1000 // default 6h
	if fStr := q.Get("from_ms"); fStr != "" {
		if v, err := strconv.ParseInt(fStr, 10, 64); err == nil {
			fromMs = v
		}
	}

	toMs := now
	if tStr := q.Get("to_ms"); tStr != "" {
		if v, err := strconv.ParseInt(tStr, 10, 64); err == nil {
			toMs = v
		}
	}

	var metricCodes []string
	if mStr := q.Get("metric_code"); mStr != "" {
		for _, part := range strings.Split(mStr, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				metricCodes = append(metricCodes, trimmed)
			}
		}
	}
	if len(metricCodes) == 0 {
		metricCodes = []string{"cpu_pct", "net_up_bps", "net_down_bps"}
	}

	resp, err := h.store.QuerySeries(r.Context(), accountID, resRef, metricCodes, fromMs, toMs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleAccountMetrics handles GET /api/v1/cloud-metrics/accounts/{id} or GET /api/v1/cloud-metrics?account_id=...
func (h *Handler) HandleAccountMetrics(w http.ResponseWriter, r *http.Request) {
	accID := r.PathValue("id")
	if accID == "" {
		accID = r.URL.Query().Get("account_id")
	}
	if accID == "" {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		for i, part := range parts {
			if (part == "accounts" || part == "cloud-accounts") && i+1 < len(parts) {
				accID = parts[i+1]
				break
			}
		}
	}
	if accID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "account id is required")
		return
	}

	var dummy string
	err := h.db.QueryRow(r.Context(), "SELECT id FROM cloud_accounts WHERE id = ?", accID).Scan(&dummy)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "cloud account not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	q := r.URL.Query()
	now := time.Now().UnixMilli()

	// Default to start of current month UTC
	nowTime := time.Now().UTC()
	startOfMonth := time.Date(nowTime.Year(), nowTime.Month(), 1, 0, 0, 0, 0, time.UTC).UnixMilli()

	fromMs := startOfMonth
	if fStr := q.Get("from_ms"); fStr != "" {
		if v, err := strconv.ParseInt(fStr, 10, 64); err == nil {
			fromMs = v
		}
	}

	toMs := now
	if tStr := q.Get("to_ms"); tStr != "" {
		if v, err := strconv.ParseInt(tStr, 10, 64); err == nil {
			toMs = v
		}
	}

	resRef := q.Get("res_ref")
	if resRef == "" {
		resRef = "_account"
	}

	var metricCodes []string
	if mStr := q.Get("metric_code"); mStr != "" {
		for _, part := range strings.Split(mStr, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				metricCodes = append(metricCodes, trimmed)
			}
		}
	}
	if len(metricCodes) == 0 {
		metricCodes = []string{"traffic_month_up"}
	}

	resp, err := h.store.QuerySeries(r.Context(), accID, resRef, metricCodes, fromMs, toMs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleSync handles POST /api/v1/cloud-metrics/sync.
func (h *Handler) HandleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported")
		return
	}

	report, err := h.syncer.SyncOnce(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sync_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"report": report,
	})
}
