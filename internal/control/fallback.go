package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"dash/internal/db"
	"dash/internal/protocol"
)

// FallbackReportResponse 定义 POST /api/agent/v1/report 的服务端响应体。
type FallbackReportResponse struct {
	ServerTimeMs int64               `json:"server_time_ms"`
	Commands     []*protocol.Request `json:"commands"`
}

// FallbackHandler 处理 /api/agent/v1/report 的 HTTP 回退上报请求。
type FallbackHandler struct {
	database *db.DB
	registry *Registry
}

// NewFallbackHandler 创建 FallbackHandler 实例。
func NewFallbackHandler(database *db.DB, registry *Registry) *FallbackHandler {
	return &FallbackHandler{
		database: database,
		registry: registry,
	}
}

func (h *FallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Bearer Token 认证
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":-32000,"message":"missing or invalid authorization header"}`))
		return
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":-32000,"message":"empty token"}`))
		return
	}

	tokenHash := HashToken(token)
	ctx := r.Context()

	var nodeID string
	q := `SELECT id FROM nodes WHERE agent_token_hash = ?`
	err := h.database.QueryRow(ctx, q, tokenHash).Scan(&nodeID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":-32000,"message":"token invalid or revoked"}`))
			return
		}
		http.Error(w, fmt.Sprintf("database query failed: %v", err), http.StatusInternalServerError)
		return
	}

	// 2. 读取批量请求体（限制 1 MB）
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var batch []*protocol.Request
	if len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, &batch); err != nil {
			http.Error(w, fmt.Sprintf("invalid json batch: %v", err), http.StatusBadRequest)
			return
		}
	}

	nowMs := time.Now().UnixMilli()
	clientIP := extractClientIP(r)

	// 3. 处理批量上报通知
	for _, req := range batch {
		if req == nil {
			continue
		}
		switch req.Method {
		case protocol.MethodAgentMetrics:
			if req.Params != nil {
				var m protocol.MetricsParams
				if err := json.Unmarshal(req.Params, &m); err == nil {
					serverNow := time.Now().UnixMilli()
					skew := serverNow - m.TsMs
					effectiveTs := m.TsMs
					if math.Abs(float64(skew)) > 60000 {
						effectiveTs = serverNow
					}

					updateSQL := `UPDATE nodes
					              SET clock_skew_ms = ?, last_seen_at_ms = ?, conn_state = 'online', updated_at_ms = ?
					              WHERE id = ?`
					_, _ = h.database.Exec(ctx, updateSQL, skew, effectiveTs, serverNow, nodeID)
				}
			}

		case protocol.MethodAgentFacts:
			if req.Params != nil {
				var f protocol.FactsParams
				if err := json.Unmarshal(req.Params, &f); err == nil {
					serverNow := time.Now().UnixMilli()
					h.saveNodeFacts(ctx, nodeID, &f, serverNow)
				}
			}
		}
	}

	// 4. 更新在线状态与客户端 IP
	updateStateSQL := `UPDATE nodes
	                   SET conn_state = 'online', last_seen_at_ms = ?, updated_at_ms = ?
	                   WHERE id = ?`
	_, _ = h.database.Exec(ctx, updateStateSQL, nowMs, nowMs, nodeID)

	if clientIP != "" {
		parsed := net.ParseIP(clientIP)
		if parsed != nil {
			if parsed.To4() != nil {
				_, _ = h.database.Exec(ctx,
					`UPDATE node_facts SET ipv4 = ?, updated_at_ms = ? WHERE node_id = ?`,
					clientIP, nowMs, nodeID)
			} else {
				_, _ = h.database.Exec(ctx,
					`UPDATE node_facts SET ipv6 = ?, updated_at_ms = ? WHERE node_id = ?`,
					clientIP, nowMs, nodeID)
			}
		}
	}

	// 5. 取出待下发回带指令
	commands := h.registry.PopFallbackCommands(nodeID)
	if commands == nil {
		commands = []*protocol.Request{}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(FallbackReportResponse{
		ServerTimeMs: nowMs,
		Commands:     commands,
	})
}

func (h *FallbackHandler) saveNodeFacts(ctx context.Context, nodeID string, f *protocol.FactsParams, nowMs int64) {
	var bootAt any
	if f.BootAtMs > 0 {
		bootAt = f.BootAtMs
	}

	updateSQL := `UPDATE node_facts SET
		arch = ?, os_name = ?, os_version = ?, kernel = ?, virt = ?,
		cpu_model = ?, cpu_cores = ?, cpu_threads = ?, mem_total = ?,
		swap_total = ?, disk_total = ?, boot_at_ms = ?, facts_hash = ?,
		updated_at_ms = ?
		WHERE node_id = ?`
	res, err := h.database.Exec(ctx, updateSQL,
		f.Arch, f.OSName, f.OSVersion, f.Kernel, f.Virt,
		f.CPUModel, f.CPUCores, f.CPUThreads, f.MemTotal,
		f.SwapTotal, f.DiskTotal, bootAt, f.FactsHash,
		nowMs, nodeID,
	)
	if err == nil {
		rows, _ := res.RowsAffected()
		if rows > 0 {
			return
		}
	}

	insertSQL := `INSERT INTO node_facts (
		node_id, arch, os_name, os_version, kernel, virt,
		cpu_model, cpu_cores, cpu_threads, mem_total, swap_total,
		disk_total, boot_at_ms, facts_hash, updated_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, _ = h.database.Exec(ctx, insertSQL,
		nodeID, f.Arch, f.OSName, f.OSVersion, f.Kernel, f.Virt,
		f.CPUModel, f.CPUCores, f.CPUThreads, f.MemTotal, f.SwapTotal,
		f.DiskTotal, bootAt, f.FactsHash, nowMs,
	)
}
