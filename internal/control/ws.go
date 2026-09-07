package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"dash/internal/config"
	"dash/internal/db"
	"dash/internal/protocol"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:    4096,
	WriteBufferSize:   4096,
	EnableCompression: true,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// WSHandler 处理 /api/agent/v1/rpc 的 WebSocket 长连接请求。
type WSHandler struct {
	database     *db.DB
	registry     *Registry
	helloHandler *HelloHandler
	config       *config.Config
	ingester     MetricsIngester
}

// MetricsIngester 接收时序采集数据。
type MetricsIngester interface {
	Ingest(nodeID string, effectiveTs int64, m *protocol.MetricsParams)
}

// NewWSHandler 创建 WSHandler 实例。
func NewWSHandler(database *db.DB, registry *Registry, cfg *config.Config) *WSHandler {
	return &WSHandler{
		database:     database,
		registry:     registry,
		helloHandler: NewHelloHandler(database, cfg),
		config:       cfg,
	}
}

// SetIngester 设置指标落库注入器。
func (h *WSHandler) SetIngester(ingester MetricsIngester) {
	h.ingester = ingester
}

func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	var nodeID, nodeName string
	q := `SELECT id, name FROM nodes WHERE agent_token_hash = ?`
	err := h.database.QueryRow(ctx, q, tokenHash).Scan(&nodeID, &nodeName)
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

	// 2. 升级为 WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	clientIP := extractClientIP(r)
	sess := NewSession(nodeID, clientIP, conn)

	// 3. 注册新连接（若同一 node 已有旧连接则将其踢出）
	h.registry.Register(sess)

	// 4. 设置心跳检测与超时保活（90 秒读超时）
	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPingHandler(func(appData string) error {
		sess.UpdateLastSeen(time.Now().UnixMilli())
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})

	// 5. 握手第一条必须为 agent.hello
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	msgType, firstPayload, err := conn.ReadMessage()
	if err != nil {
		sess.Close()
		h.cleanupSession(sess)
		return
	}

	if msgType != websocket.TextMessage {
		sess.Close()
		h.cleanupSession(sess)
		return
	}

	var helloReq protocol.Request
	if err := json.Unmarshal(firstPayload, &helloReq); err != nil || helloReq.Method != protocol.MethodAgentHello || helloReq.ID == nil {
		errResp := &protocol.Response{
			JSONRPC: protocol.JSONRPCVersion,
			ID:      helloReq.ID,
			Error: &protocol.RPCError{
				Code:    protocol.ErrCodeInvalidRequest,
				Message: "expected agent.hello request as first message",
			},
		}
		_ = sess.SendResponse(errResp)
		sess.Close()
		h.cleanupSession(sess)
		return
	}

	helloResult, rpcErr := h.helloHandler.HandleHello(ctx, nodeID, clientIP, &helloReq)
	if rpcErr != nil {
		errResp := &protocol.Response{
			JSONRPC: protocol.JSONRPCVersion,
			ID:      helloReq.ID,
			Error:   rpcErr,
		}
		_ = sess.SendResponse(errResp)
		sess.Close()
		h.cleanupSession(sess)
		return
	}

	rawResult, _ := json.Marshal(helloResult)
	resp := &protocol.Response{
		JSONRPC: protocol.JSONRPCVersion,
		ID:      helloReq.ID,
		Result:  rawResult,
	}
	if err := sess.SendResponse(resp); err != nil {
		sess.Close()
		h.cleanupSession(sess)
		return
	}

	// 6. 恢复 90 秒读超时，进入主消息循环
	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	sess.UpdateLastSeen(time.Now().UnixMilli())

	for {
		mType, payload, rErr := conn.ReadMessage()
		if rErr != nil {
			break
		}
		if mType != websocket.TextMessage {
			continue
		}

		nowMs := time.Now().UnixMilli()
		sess.UpdateLastSeen(nowMs)
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))

		h.handleIncomingMessage(ctx, sess, payload)
	}

	sess.Close()
	h.cleanupSession(sess)
}

func (h *WSHandler) handleIncomingMessage(ctx context.Context, sess *Session, payload []byte) {
	var req protocol.Request
	if err := json.Unmarshal(payload, &req); err != nil {
		return
	}

	switch req.Method {
	case protocol.MethodAgentMetrics:
		if req.Params != nil {
			var m protocol.MetricsParams
			if err := json.Unmarshal(req.Params, &m); err == nil {
				serverNow := time.Now().UnixMilli()
				skew := serverNow - m.TsMs
				effectiveTs := m.TsMs
				// 时钟偏差：server_ts_ms - ts_ms 超过 60 秒时以服务端时间入库，并在节点上打标记
				if math.Abs(float64(skew)) > 60000 {
					effectiveTs = serverNow
				}

				updateSQL := `UPDATE nodes
				              SET clock_skew_ms = ?, last_seen_at_ms = ?, updated_at_ms = ?
				              WHERE id = ?`
				_, _ = h.database.Exec(ctx, updateSQL, skew, effectiveTs, serverNow, sess.NodeID)

				if h.ingester != nil {
					h.ingester.Ingest(sess.NodeID, effectiveTs, &m)
				}
			}
		}

	case protocol.MethodAgentFacts:
		if req.Params != nil {
			var f protocol.FactsParams
			if err := json.Unmarshal(req.Params, &f); err == nil {
				serverNow := time.Now().UnixMilli()
				h.saveNodeFacts(ctx, sess.NodeID, &f, serverNow)
			}
		}

	default:
		// 未知方法：若是 request 则返回 Method not found
		if req.ID != nil {
			errResp := &protocol.Response{
				JSONRPC: protocol.JSONRPCVersion,
				ID:      req.ID,
				Error: &protocol.RPCError{
					Code:    protocol.ErrCodeMethodNotFound,
					Message: fmt.Sprintf("method %s not found", req.Method),
				},
			}
			_ = sess.SendResponse(errResp)
		}
	}
}

func (h *WSHandler) saveNodeFacts(ctx context.Context, nodeID string, f *protocol.FactsParams, nowMs int64) {
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

func (h *WSHandler) cleanupSession(sess *Session) {
	h.registry.Unregister(sess)
	// 若该连接是因为被新连接踢出，则不标记为离线
	if !sess.Kicked.Load() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		nowMs := time.Now().UnixMilli()
		if h.registry != nil {
			h.registry.emitOffline(ctx, sess.NodeID, sess.LastSeenAtMs.Load(), sess.RemoteIP)
		}
		_, _ = h.database.Exec(ctx,
			`UPDATE nodes SET conn_state = 'offline', updated_at_ms = ? WHERE id = ?`,
			nowMs, sess.NodeID)
	}
}
