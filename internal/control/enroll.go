package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"dash/internal/db"
	"dash/internal/protocol"
)

var (
	ErrTokenAlreadyUsed = errors.New("enrollment token already used")
)

// EnrollRequest 定义 Agent 注册请求体。
type EnrollRequest struct {
	EnrollToken string                `json:"enroll_token"`
	Facts       *protocol.FactsParams `json:"facts,omitempty"`
}

// EnrollResponse 定义 Agent 注册成功响应体。
type EnrollResponse struct {
	NodeID     string `json:"node_id"`
	AgentToken string `json:"agent_token"`
}

// IPRateLimiter 针对注册接口的基于 IP 的内存滑动窗口限流器（每分钟最多 10 次）。
type IPRateLimiter struct {
	mu      sync.Mutex
	history map[string][]int64
	limit   int
	window  time.Duration
}

// NewIPRateLimiter 创建 IP 限流器实例。
func NewIPRateLimiter(limit int, window time.Duration) *IPRateLimiter {
	if limit <= 0 {
		limit = 10
	}
	if window <= 0 {
		window = time.Minute
	}
	return &IPRateLimiter{
		history: make(map[string][]int64),
		limit:   limit,
		window:  window,
	}
}

// Allow 判断指定 IP 在滑动窗口内是否允许通行。
func (l *IPRateLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now().UnixMilli()
	windowStart := now - l.window.Milliseconds()

	times := l.history[ip]
	validIdx := 0
	for i, t := range times {
		if t >= windowStart {
			validIdx = i
			break
		}
		if i == len(times)-1 {
			validIdx = len(times)
		}
	}
	times = times[validIdx:]

	if len(times) >= l.limit {
		l.history[ip] = times
		return false
	}

	l.history[ip] = append(times, now)
	return true
}

// EnrollHandler 处理 /api/agent/v1/enroll 请求。
type EnrollHandler struct {
	database *db.DB
	limiter  *IPRateLimiter
}

// NewEnrollHandler 创建 EnrollHandler 实例。
func NewEnrollHandler(database *db.DB) *EnrollHandler {
	return &EnrollHandler{
		database: database,
		limiter:  NewIPRateLimiter(10, time.Minute),
	}
}

func (h *EnrollHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := extractClientIP(r)
	if !h.limiter.Allow(clientIP) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate_limited","message":"too many enrollment attempts, try again later"}`))
		return
	}

	var req EnrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.EnrollToken == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_enroll_token","message":"missing or invalid enroll_token"}`))
		return
	}

	tokenHash := HashToken(req.EnrollToken)
	ctx := r.Context()
	nowMs := time.Now().UnixMilli()

	// 1. 查询 enrollment token
	var (
		tokenID       string
		presetName    sql.NullString
		presetGroupID sql.NullString
		expiresAtMs   int64
		usedAtMs      sql.NullInt64
	)

	q := `SELECT id, preset_name, preset_group_id, expires_at_ms, used_at_ms
	      FROM enroll_tokens
	      WHERE token_hash = ?`
	err := h.database.QueryRow(ctx, q, tokenHash).Scan(&tokenID, &presetName, &presetGroupID, &expiresAtMs, &usedAtMs)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_enroll_token","message":"enrollment token not found"}`))
			return
		}
		http.Error(w, fmt.Sprintf("database error: %v", err), http.StatusInternalServerError)
		return
	}

	// 2. 检查已使用与过期状态
	if usedAtMs.Valid {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"error":"enroll_token_used","message":"enrollment token has already been used"}`))
		return
	}

	if nowMs > expiresAtMs {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"error":"enroll_token_expired","message":"enrollment token has expired"}`))
		return
	}

	// 3. 生成节点与长期 Token
	nodeID := NewULID()
	agentToken, err := GenerateRandomToken()
	if err != nil {
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}
	agentTokenHash := HashToken(agentToken)

	nodeName := ""
	if presetName.Valid && presetName.String != "" {
		nodeName = presetName.String
	} else if req.Facts != nil && req.Facts.OSName != "" {
		nodeName = fmt.Sprintf("%s-%s", req.Facts.OSName, nodeID[len(nodeID)-6:])
	} else {
		nodeName = fmt.Sprintf("node-%s", nodeID[len(nodeID)-6:])
	}

	var groupID any
	if presetGroupID.Valid && presetGroupID.String != "" {
		groupID = presetGroupID.String
	}

	var ipv4Val, ipv6Val any
	if clientIP != "" {
		parsedIP := net.ParseIP(clientIP)
		if parsedIP != nil {
			if parsedIP.To4() != nil {
				ipv4Val = clientIP
			} else {
				ipv6Val = clientIP
			}
		}
	}

	// 4. 事务内完成节点插入、node_facts 记录、作废 token 与写入审计日志
	txErr := h.database.WithTx(ctx, func(tx *db.Tx) error {
		// 插入 nodes
		insertNodeSQL := `INSERT INTO nodes (
			id, name, node_group_id, display_order, is_hidden,
			agent_token_hash, agent_version, conn_state, last_seen_at_ms,
			clock_skew_ms, note, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		_, err := tx.Exec(ctx, insertNodeSQL,
			nodeID, nodeName, groupID, 0, 0,
			agentTokenHash, nil, "never", nil,
			nil, nil, nowMs, nowMs,
		)
		if err != nil {
			return fmt.Errorf("insert node: %w", err)
		}

		// 插入 node_facts
		if req.Facts != nil {
			f := req.Facts
			insertFactsSQL := `INSERT INTO node_facts (
				node_id, arch, os_name, os_version, kernel, virt,
				cpu_model, cpu_cores, cpu_threads, mem_total, swap_total,
				disk_total, ipv4, ipv6, boot_at_ms, facts_hash, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
			var bootAt any
			if f.BootAtMs > 0 {
				bootAt = f.BootAtMs
			}
			_, err = tx.Exec(ctx, insertFactsSQL,
				nodeID, f.Arch, f.OSName, f.OSVersion, f.Kernel, f.Virt,
				f.CPUModel, f.CPUCores, f.CPUThreads, f.MemTotal, f.SwapTotal,
				f.DiskTotal, ipv4Val, ipv6Val, bootAt, f.FactsHash, nowMs,
			)
			if err != nil {
				return fmt.Errorf("insert node_facts: %w", err)
			}
		} else {
			insertFactsSQL := `INSERT INTO node_facts (
				node_id, arch, os_name, os_version, kernel, virt,
				cpu_model, cpu_cores, cpu_threads, mem_total, swap_total,
				disk_total, ipv4, ipv6, boot_at_ms, facts_hash, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
			_, err = tx.Exec(ctx, insertFactsSQL,
				nodeID, nil, nil, nil, nil, nil,
				nil, 0, 0, 0, 0,
				0, ipv4Val, ipv6Val, nil, nil, nowMs,
			)
			if err != nil {
				return fmt.Errorf("insert empty node_facts: %w", err)
			}
		}

		// 作废 enrollment token（乐观并发控制）
		updateTokenSQL := `UPDATE enroll_tokens
		                   SET used_at_ms = ?, used_node_id = ?
		                   WHERE id = ? AND used_at_ms IS NULL`
		res, err := tx.Exec(ctx, updateTokenSQL, nowMs, nodeID, tokenID)
		if err != nil {
			return fmt.Errorf("update enroll token: %w", err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("check rows affected: %w", err)
		}
		if rows == 0 {
			return ErrTokenAlreadyUsed
		}

		// 写入 audit_log
		auditID := NewULID()
		detailBytes, _ := json.Marshal(map[string]any{
			"node_id":         nodeID,
			"node_name":       nodeName,
			"enroll_token_id": tokenID,
		})
		auditSQL := `INSERT INTO audit_log (
			id, actor_kind, actor_id, action, target_kind,
			target_id, detail, result, ip, created_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		_, err = tx.Exec(ctx, auditSQL,
			auditID, "agent", nodeID, "node.enroll", "node",
			nodeID, string(detailBytes), "ok", clientIP, nowMs,
		)
		if err != nil {
			return fmt.Errorf("insert audit log: %w", err)
		}

		return nil
	})

	if txErr != nil {
		if errors.Is(txErr, ErrTokenAlreadyUsed) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"error":"enroll_token_used","message":"enrollment token has already been used"}`))
			return
		}
		http.Error(w, fmt.Sprintf("transaction failed: %v", txErr), http.StatusInternalServerError)
		return
	}

	// 5. 成功返回明文长期 Token
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(EnrollResponse{
		NodeID:     nodeID,
		AgentToken: agentToken,
	})
}

// CreateEnrollmentToken 签发一次性 enrollment token 并写入 enroll_tokens 表。
// 默认有效期 15 分钟。
func CreateEnrollmentToken(ctx context.Context, database *db.DB, presetName, presetGroupID, createdBy string, ttl time.Duration) (string, string, error) {
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	plainToken, err := GenerateRandomToken()
	if err != nil {
		return "", "", err
	}
	tokenID := NewULID()
	tokenHash := HashToken(plainToken)
	nowMs := time.Now().UnixMilli()
	expiresAtMs := nowMs + ttl.Milliseconds()

	q := `INSERT INTO enroll_tokens (
		id, token_hash, preset_name, preset_group_id,
		expires_at_ms, used_at_ms, used_node_id, created_by, created_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var pName, pGroup, pCreator any
	if presetName != "" {
		pName = presetName
	}
	if presetGroupID != "" {
		pGroup = presetGroupID
	}
	if createdBy != "" {
		pCreator = createdBy
	}

	_, err = database.Exec(ctx, q, tokenID, tokenHash, pName, pGroup, expiresAtMs, nil, nil, pCreator, nowMs)
	if err != nil {
		return "", "", fmt.Errorf("create enrollment token: %w", err)
	}
	return plainToken, tokenID, nil
}
