package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/logx"
	"dash/internal/protocol"

	"github.com/gorilla/websocket"
)

// Session 代表一个已建立 WebSocket 长连接的 Agent 会话。
type Session struct {
	NodeID        string
	RemoteIP      string
	AgentVersion  string
	ConnectedAtMs int64
	LastSeenAtMs  atomic.Int64
	Conn          *websocket.Conn
	WriteMu       sync.Mutex
	Closed        atomic.Bool
	CloseCh       chan struct{}
	Kicked        atomic.Bool
}

// NewSession 创建新的 Session 实例。
func NewSession(nodeID, remoteIP string, conn *websocket.Conn) *Session {
	now := time.Now().UnixMilli()
	s := &Session{
		NodeID:        nodeID,
		RemoteIP:      remoteIP,
		ConnectedAtMs: now,
		Conn:          conn,
		CloseCh:       make(chan struct{}),
	}
	s.LastSeenAtMs.Store(now)
	return s
}

// SendResponse 向 Agent 发送 JSON-RPC 响应消息。
func (s *Session) SendResponse(resp *protocol.Response) error {
	if s.Closed.Load() {
		return fmt.Errorf("session closed")
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	s.WriteMu.Lock()
	defer s.WriteMu.Unlock()
	if s.Closed.Load() {
		return fmt.Errorf("session closed")
	}

	_ = s.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.Conn.WriteMessage(websocket.TextMessage, payload)
}

// SendRequest 向 Agent 发送 JSON-RPC 请求/通知消息。
func (s *Session) SendRequest(req *protocol.Request) error {
	if s.Closed.Load() {
		return fmt.Errorf("session closed")
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	s.WriteMu.Lock()
	defer s.WriteMu.Unlock()
	if s.Closed.Load() {
		return fmt.Errorf("session closed")
	}

	_ = s.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.Conn.WriteMessage(websocket.TextMessage, payload)
}

// Close 关闭长连接会话。
func (s *Session) Close() error {
	if s.Closed.CompareAndSwap(false, true) {
		close(s.CloseCh)
		if s.Conn != nil {
			return s.Conn.Close()
		}
	}
	return nil
}

// UpdateLastSeen 更新最近活动时间戳（毫秒）。
func (s *Session) UpdateLastSeen(ts int64) {
	s.LastSeenAtMs.Store(ts)
}

// Registry 维护 Agent 在线长连接注册表及离线检测状态。
type Registry struct {
	mu           sync.RWMutex
	sessions     map[string]*Session
	fallbackCmds map[string][]*protocol.Request
	database     *db.DB
	logger       *logx.Logger
	stopCh       chan struct{}
	wg           sync.WaitGroup
	stopped      atomic.Bool
}

// NewRegistry 创建 Registry 实例。
func NewRegistry(database *db.DB, logger *logx.Logger) *Registry {
	return &Registry{
		sessions:     make(map[string]*Session),
		fallbackCmds: make(map[string][]*protocol.Request),
		database:     database,
		logger:       logger,
		stopCh:       make(chan struct{}),
	}
}

// Register 注册新的长连接会话。
// 若同一 NodeID 已存在旧连接，则标记并踢掉旧连接，避免连接堆积。
func (r *Registry) Register(sess *Session) (kickedOld *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if old, exists := r.sessions[sess.NodeID]; exists {
		old.Kicked.Store(true)
		_ = old.Close()
		kickedOld = old
	}
	r.sessions[sess.NodeID] = sess
	return kickedOld
}

// Unregister 注销会话。
// 仅当当前注册表中的活跃会话与传入会话一致时才移除。
func (r *Registry) Unregister(sess *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cur, exists := r.sessions[sess.NodeID]; exists && cur == sess {
		delete(r.sessions, sess.NodeID)
	}
}

// Get 获取指定节点的在线长连接会话。
func (r *Registry) Get(nodeID string) (*Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.sessions[nodeID]
	return s, ok
}

// IsOnline 查询节点是否在长连接注册表中在线。
func (r *Registry) IsOnline(nodeID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.sessions[nodeID]
	return ok
}

// OnlineCount 返回当前在线节点总数。
func (r *Registry) OnlineCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.sessions)
}

// OnlineNodes 返回所有当前在线节点的 ID 列表。
func (r *Registry) OnlineNodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		list = append(list, id)
	}
	return list
}

// Kick 踢掉指定节点的在线连接。
func (r *Registry) Kick(nodeID string) bool {
	r.mu.Lock()
	sess, exists := r.sessions[nodeID]
	if exists {
		sess.Kicked.Store(true)
		delete(r.sessions, nodeID)
	}
	r.mu.Unlock()

	if exists && sess != nil {
		_ = sess.Close()
		return true
	}
	return false
}

// RevokeToken 吊销节点的长期 Token，断开当前在线连接并写审计日志。
func (r *Registry) RevokeToken(ctx context.Context, nodeID string, actorKind, actorID, clientIP string) error {
	nowMs := time.Now().UnixMilli()

	// 1. 置空 nodes.agent_token_hash
	q := `UPDATE nodes SET agent_token_hash = NULL, updated_at_ms = ? WHERE id = ?`
	res, err := r.database.Exec(ctx, q, nowMs, nodeID)
	if err != nil {
		return fmt.Errorf("revoke token in db: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return db.ErrNotFound
	}

	// 2. 写入审计日志
	auditID := NewULID()
	if actorKind == "" {
		actorKind = "system"
	}
	var actID any
	if actorID != "" {
		actID = actorID
	}
	var ipVal any
	if clientIP != "" {
		ipVal = clientIP
	}

	auditQ := `INSERT INTO audit_log (
		id, actor_kind, actor_id, action, target_kind,
		target_id, detail, result, ip, created_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, _ = r.database.Exec(ctx, auditQ, auditID, actorKind, actID, "node.revoke_token", "node", nodeID, nil, "ok", ipVal, nowMs)

	// 3. 若有当前连接，发送 -32000 RPC 错误并主动踢下线
	r.mu.Lock()
	sess, ok := r.sessions[nodeID]
	if ok {
		delete(r.sessions, nodeID)
	}
	r.mu.Unlock()

	if ok && sess != nil {
		sess.Kicked.Store(true)
		errResp := &protocol.Response{
			JSONRPC: protocol.JSONRPCVersion,
			Error: &protocol.RPCError{
				Code:    protocol.ErrCodeTokenInvalid,
				Message: "token invalid or revoked",
			},
		}
		_ = sess.SendResponse(errResp)
		_ = sess.Close()
	}

	return nil
}

// EnqueueFallbackCommand 将指令放入 HTTP 回退队列。
func (r *Registry) EnqueueFallbackCommand(nodeID string, cmd *protocol.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.fallbackCmds[nodeID] = append(r.fallbackCmds[nodeID], cmd)
}

// PopFallbackCommands 取出并清空指定节点的全部回退指令。
func (r *Registry) PopFallbackCommands(nodeID string) []*protocol.Request {
	r.mu.Lock()
	defer r.mu.Unlock()

	cmds := r.fallbackCmds[nodeID]
	delete(r.fallbackCmds, nodeID)
	if cmds == nil {
		return []*protocol.Request{}
	}
	return cmds
}

// BroadcastServerConfig 向所有当前在线的长连接 Agent 下发 server.config 通知。
func (r *Registry) BroadcastServerConfig(params *protocol.ServerConfigParams) error {
	if r == nil || params == nil {
		return nil
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal server.config params: %w", err)
	}

	req := &protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		Method:  protocol.MethodServerConfig,
		Params:  payload,
	}

	r.mu.RLock()
	sessions := make([]*Session, 0, len(r.sessions))
	for _, sess := range r.sessions {
		sessions = append(sessions, sess)
	}
	r.mu.RUnlock()

	for _, sess := range sessions {
		_ = sess.SendRequest(req)
	}
	return nil
}

// Start 启动后台保活与离线超时扫描任务（每 15 秒检查一次）。
func (r *Registry) Start(ctx context.Context) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				r.sweepInactive(ctx)
			case <-r.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop 停止 Registry 后台任务并释放资源。
func (r *Registry) Stop() {
	if r.stopped.CompareAndSwap(false, true) {
		close(r.stopCh)
		r.wg.Wait()

		r.mu.Lock()
		for _, sess := range r.sessions {
			sess.Kicked.Store(true)
			_ = sess.Close()
		}
		r.sessions = make(map[string]*Session)
		r.mu.Unlock()
	}
}

// sweepInactive 检查超过 90 秒未收到任何消息（含 ping）的节点并判为离线。
func (r *Registry) sweepInactive(ctx context.Context) {
	nowMs := time.Now().UnixMilli()
	cutoffMs := nowMs - 90000 // 90 秒超时

	var timedOut []*Session
	r.mu.Lock()
	for id, sess := range r.sessions {
		if sess.LastSeenAtMs.Load() < cutoffMs {
			timedOut = append(timedOut, sess)
			delete(r.sessions, id)
		}
	}
	r.mu.Unlock()

	for _, sess := range timedOut {
		_ = sess.Close()
		if r.database != nil {
			r.emitOffline(ctx, sess.NodeID, sess.LastSeenAtMs.Load(), sess.RemoteIP)
			_, _ = r.database.Exec(ctx,
				`UPDATE nodes SET conn_state = 'offline', updated_at_ms = ? WHERE id = ?`,
				nowMs, sess.NodeID)
		}
	}

	// 同步检查数据库中标记为 online 但已超时的僵尸记录
	if r.database != nil {
		qZombie := `SELECT n.id, n.last_seen_at_ms, COALESCE(f.ipv4, '')
			FROM nodes n
			LEFT JOIN node_facts f ON n.id = f.node_id
			WHERE n.conn_state = 'online' AND (n.last_seen_at_ms IS NULL OR n.last_seen_at_ms < ?)`
		rows, err := r.database.Query(ctx, qZombie, cutoffMs)
		if err == nil {
			type zombieInfo struct {
				id       string
				lastSeen int64
				ip       string
			}
			var zombieList []zombieInfo
			for rows.Next() {
				var zid string
				var zls sql.NullInt64
				var zip string
				if err := rows.Scan(&zid, &zls, &zip); err == nil {
					var ls int64
					if zls.Valid {
						ls = zls.Int64
					}
					zombieList = append(zombieList, zombieInfo{id: zid, lastSeen: ls, ip: zip})
				}
			}
			_ = rows.Close()

			for _, z := range zombieList {
				r.emitOffline(ctx, z.id, z.lastSeen, z.ip)
			}
		}

		_, _ = r.database.Exec(ctx,
			`UPDATE nodes SET conn_state = 'offline', updated_at_ms = ? WHERE conn_state = 'online' AND (last_seen_at_ms IS NULL OR last_seen_at_ms < ?)`,
			nowMs, cutoffMs)
	}
}

// emitOffline 发送节点离线事件 (P1-20)。
func (r *Registry) emitOffline(ctx context.Context, nodeID string, lastSeenAtMs int64, defaultIP string) {
	if r.database == nil {
		return
	}
	var nodeName string
	var groupName, factsIP sql.NullString
	querySQL := `SELECT n.name, g.name, f.ipv4
		FROM nodes n
		LEFT JOIN node_groups g ON n.node_group_id = g.id
		LEFT JOIN node_facts f ON n.id = f.node_id
		WHERE n.id = ?`
	_ = r.database.QueryRow(ctx, querySQL, nodeID).Scan(&nodeName, &groupName, &factsIP)
	if nodeName == "" {
		nodeName = nodeID
	}
	gName := ""
	if groupName.Valid {
		gName = groupName.String
	}
	publicIP := defaultIP
	if factsIP.Valid && factsIP.String != "" {
		publicIP = factsIP.String
	}

	events.Emit(ctx, events.Event{
		Type:       "node.offline",
		Source:     "control",
		TargetKind: "node",
		TargetID:   nodeID,
		Title:      fmt.Sprintf("节点 %s 离线", nodeName),
		DedupKey:   fmt.Sprintf("node.offline:%s", nodeID),
		Payload: map[string]any{
			"node_name":       nodeName,
			"group_name":      gName,
			"public_ip":       publicIP,
			"last_seen_at_ms": lastSeenAtMs,
		},
	})
}
