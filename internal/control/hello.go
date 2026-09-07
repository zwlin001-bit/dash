package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"dash/internal/config"
	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/protocol"
)

// HelloHandler 处理 Agent 连接后的第一条请求 agent.hello。
type HelloHandler struct {
	database *db.DB
	config   *config.Config
}

// NewHelloHandler 创建 HelloHandler 实例。
func NewHelloHandler(database *db.DB, cfg *config.Config) *HelloHandler {
	return &HelloHandler{
		database: database,
		config:   cfg,
	}
}

// HandleHello 处理 agent.hello 请求并返回 HelloResult 或 RPCError。
func (h *HelloHandler) HandleHello(ctx context.Context, nodeID string, remoteIP string, req *protocol.Request) (*protocol.HelloResult, *protocol.RPCError) {
	if req.Params == nil {
		return nil, &protocol.RPCError{
			Code:    protocol.ErrCodeInvalidParams,
			Message: "missing hello params",
		}
	}

	var params protocol.HelloParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, &protocol.RPCError{
			Code:    protocol.ErrCodeInvalidParams,
			Message: fmt.Sprintf("invalid hello params: %v", err),
		}
	}

	// 1. 协议版本兼容性检查（协议不兼容返回 -32001）
	if params.ProtocolVersion != protocol.ProtocolVersion {
		return nil, &protocol.RPCError{
			Code:    protocol.ErrCodeVersionMismatch,
			Message: fmt.Sprintf("protocol version mismatch: client=%d, server=%d", params.ProtocolVersion, protocol.ProtocolVersion),
		}
	}

	nowMs := time.Now().UnixMilli()

	// 2. 比对 facts_hash
	var dbFactsHash sql.NullString
	err := h.database.QueryRow(ctx, "SELECT facts_hash FROM node_facts WHERE node_id = ?", nodeID).Scan(&dbFactsHash)
	needFacts := false
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			needFacts = true
		} else {
			// 数据库异常降级为要求上报
			needFacts = true
		}
	} else if !dbFactsHash.Valid || dbFactsHash.String == "" || dbFactsHash.String != params.FactsHash {
		needFacts = true
	}

	// 3. 服务端采集参数权威下发
	fastInterval := 5
	slowInterval := 60
	factsMaxInterval := 1800
	collectConns := true

	result := &protocol.HelloResult{
		NodeID:            nodeID,
		ServerTimeMs:      nowMs,
		IntervalFastS:     fastInterval,
		IntervalSlowS:     slowInterval,
		FactsMaxIntervalS: factsMaxInterval,
		CollectConns:      collectConns,
		NeedFacts:         needFacts,
	}

	// 4. 更新 nodes 状态为 online，记录版本与 last_seen 并发出 node.online 事件
	var nodeName string
	var prevLastSeen sql.NullInt64
	var groupName sql.NullString
	queryNodeSQL := `SELECT n.name, n.last_seen_at_ms, g.name
		FROM nodes n
		LEFT JOIN node_groups g ON n.node_group_id = g.id
		WHERE n.id = ?`
	_ = h.database.QueryRow(ctx, queryNodeSQL, nodeID).Scan(&nodeName, &prevLastSeen, &groupName)

	updateNodeSQL := `UPDATE nodes
	                  SET agent_version = ?, conn_state = 'online', last_seen_at_ms = ?, updated_at_ms = ?
	                  WHERE id = ?`
	_, _ = h.database.Exec(ctx, updateNodeSQL, params.AgentVersion, nowMs, nowMs, nodeID)

	var offlineSeconds int64
	if prevLastSeen.Valid && prevLastSeen.Int64 > 0 {
		offlineSeconds = (nowMs - prevLastSeen.Int64) / 1000
		if offlineSeconds < 0 {
			offlineSeconds = 0
		}
	}
	if nodeName == "" {
		nodeName = nodeID
	}
	gName := ""
	if groupName.Valid {
		gName = groupName.String
	}

	events.Emit(ctx, events.Event{
		Type:       "node.online",
		Source:     "control",
		TargetKind: "node",
		TargetID:   nodeID,
		Title:      fmt.Sprintf("节点 %s 上线", nodeName),
		DedupKey:   fmt.Sprintf("node.online:%s", nodeID),
		Payload: map[string]any{
			"node_name":       nodeName,
			"group_name":      gName,
			"public_ip":       remoteIP,
			"offline_seconds": offlineSeconds,
		},
	})

	// 5. 更新 node_facts 中的客户端连接来源 IP
	if remoteIP != "" {
		parsed := net.ParseIP(remoteIP)
		if parsed != nil {
			if parsed.To4() != nil {
				_, _ = h.database.Exec(ctx,
					`UPDATE node_facts SET ipv4 = ?, updated_at_ms = ? WHERE node_id = ?`,
					remoteIP, nowMs, nodeID)
			} else {
				_, _ = h.database.Exec(ctx,
					`UPDATE node_facts SET ipv6 = ?, updated_at_ms = ? WHERE node_id = ?`,
					remoteIP, nowMs, nodeID)
			}
		}
	}

	return result, nil
}
