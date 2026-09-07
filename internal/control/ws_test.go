package control_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dash/agent/transport"
	"dash/internal/control"
	"dash/internal/protocol"

	"github.com/gorilla/websocket"
)

// helper to setup server and enrolled node
func setupTestServerAndNode(t *testing.T) (*httptest.Server, *control.Registry, string, string) {
	database := getTestDB(t)
	cleanTables(t, database)

	registry := control.NewRegistry(database, nil)
	registry.Start(context.Background())

	mux := http.NewServeMux()
	control.RegisterRoutes(mux, database, registry, nil)

	srv := httptest.NewServer(mux)

	// Enroll a node
	ctx := context.Background()
	plainEnrollToken, _, err := control.CreateEnrollmentToken(ctx, database, "test-vps-1", "", "admin", 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to create enroll token: %v", err)
	}

	enrollHandler := control.NewEnrollHandler(database)
	enrollBody, _ := json.Marshal(control.EnrollRequest{
		EnrollToken: plainEnrollToken,
		Facts: &protocol.FactsParams{
			Arch:      "amd64",
			OSName:    "debian",
			FactsHash: "factshash_original",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", strings.NewReader(string(enrollBody)))
	rr := httptest.NewRecorder()
	enrollHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("enroll failed: %d", rr.Code)
	}
	var enrollResp control.EnrollResponse
	_ = json.NewDecoder(rr.Body).Decode(&enrollResp)

	return srv, registry, enrollResp.NodeID, enrollResp.AgentToken
}

// TestWS_HelloAndOnlineState 验证建立长连接、agent.hello 协商与恢复在线状态 (验收项 5)
func TestWS_HelloAndOnlineState(t *testing.T) {
	srv, registry, nodeID, token := setupTestServerAndNode(t)
	defer srv.Close()
	defer registry.Stop()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agent/v1/rpc"

	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("websocket dial failed: %v (resp: %v)", err, resp)
	}
	defer conn.Close()

	// 1. 发送 agent.hello
	reqID := int64(1)
	helloReq := protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		ID:      &reqID,
		Method:  protocol.MethodAgentHello,
	}
	helloParams, _ := json.Marshal(protocol.HelloParams{
		ProtocolVersion: protocol.ProtocolVersion,
		AgentVersion:    "0.1.0",
		BootAtMs:        time.Now().UnixMilli(),
		Capabilities:    []string{"metrics", "facts"},
		FactsHash:       "factshash_original", // 相同 hash，预期 need_facts = false
	})
	helloReq.Params = helloParams

	if err := conn.WriteJSON(helloReq); err != nil {
		t.Fatalf("write agent.hello failed: %v", err)
	}

	// 2. 接收 HelloResult
	var helloResp protocol.Response
	if err := conn.ReadJSON(&helloResp); err != nil {
		t.Fatalf("read agent.hello resp failed: %v", err)
	}
	if helloResp.Error != nil {
		t.Fatalf("unexpected hello error: %v", helloResp.Error)
	}

	var helloRes protocol.HelloResult
	if err := json.Unmarshal(helloResp.Result, &helloRes); err != nil {
		t.Fatalf("unmarshal HelloResult failed: %v", err)
	}

	if helloRes.NodeID != nodeID {
		t.Fatalf("expected node_id %s, got %s", nodeID, helloRes.NodeID)
	}
	if helloRes.NeedFacts != false {
		t.Fatalf("expected need_facts = false when facts_hash matches, got true")
	}
	if helloRes.IntervalFastS != 5 || helloRes.IntervalSlowS != 60 {
		t.Fatalf("unexpected intervals: %d, %d", helloRes.IntervalFastS, helloRes.IntervalSlowS)
	}

	// 3. 验证内存注册表与数据库状态均变为 online
	if !registry.IsOnline(nodeID) {
		t.Fatalf("expected node %s to be online in registry", nodeID)
	}

	db := getTestDB(t)
	defer db.Close()
	var connState string
	err = db.QueryRow(context.Background(), "SELECT conn_state FROM nodes WHERE id = ?", nodeID).Scan(&connState)
	if err != nil {
		t.Fatalf("query conn_state failed: %v", err)
	}
	if connState != "online" {
		t.Fatalf("expected conn_state 'online', got %q", connState)
	}
}

// TestWS_FactsHashChanged 验证 facts_hash 不一致时 need_facts = true
func TestWS_FactsHashChanged(t *testing.T) {
	srv, registry, nodeID, token := setupTestServerAndNode(t)
	defer srv.Close()
	defer registry.Stop()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agent/v1/rpc"
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()

	reqID := int64(1)
	helloReq := protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		ID:      &reqID,
		Method:  protocol.MethodAgentHello,
	}
	helloParams, _ := json.Marshal(protocol.HelloParams{
		ProtocolVersion: protocol.ProtocolVersion,
		AgentVersion:    "0.1.0",
		FactsHash:       "different_hash_xxx",
	})
	helloReq.Params = helloParams
	_ = conn.WriteJSON(helloReq)

	var helloResp protocol.Response
	_ = conn.ReadJSON(&helloResp)
	var helloRes protocol.HelloResult
	_ = json.Unmarshal(helloResp.Result, &helloRes)

	if helloRes.NodeID != nodeID || helloRes.NeedFacts != true {
		t.Fatalf("expected need_facts = true for changed facts_hash, got %+v", helloRes)
	}
}

// TestWS_ProtocolMismatch 验证协议版本不兼容返回 -32001
func TestWS_ProtocolMismatch(t *testing.T) {
	srv, registry, _, token := setupTestServerAndNode(t)
	defer srv.Close()
	defer registry.Stop()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agent/v1/rpc"
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()

	reqID := int64(1)
	helloReq := protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		ID:      &reqID,
		Method:  protocol.MethodAgentHello,
	}
	helloParams, _ := json.Marshal(protocol.HelloParams{
		ProtocolVersion: 999, // 不兼容的版本
		AgentVersion:    "0.1.0",
	})
	helloReq.Params = helloParams
	_ = conn.WriteJSON(helloReq)

	var helloResp protocol.Response
	if err := conn.ReadJSON(&helloResp); err != nil {
		t.Fatalf("read response failed: %v", err)
	}

	if helloResp.Error == nil || helloResp.Error.Code != protocol.ErrCodeVersionMismatch {
		t.Fatalf("expected ErrCodeVersionMismatch (-32001), got %+v", helloResp.Error)
	}
}

// TestWS_KickOldConnection 验证同一节点重复连接踢掉旧连接 (验收项 6)
func TestWS_KickOldConnection(t *testing.T) {
	srv, registry, nodeID, token := setupTestServerAndNode(t)
	defer srv.Close()
	defer registry.Stop()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agent/v1/rpc"
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)

	// 连接 1
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("conn1 dial failed: %v", err)
	}
	defer conn1.Close()

	reqID := int64(1)
	helloReq := protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		ID:      &reqID,
		Method:  protocol.MethodAgentHello,
	}
	helloParams, _ := json.Marshal(protocol.HelloParams{
		ProtocolVersion: protocol.ProtocolVersion,
		AgentVersion:    "0.1.0",
	})
	helloReq.Params = helloParams
	_ = conn1.WriteJSON(helloReq)
	var resp1 protocol.Response
	_ = conn1.ReadJSON(&resp1)

	if !registry.IsOnline(nodeID) {
		t.Fatalf("expected node online after conn1")
	}

	// 连接 2 连接同一个 node
	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("conn2 dial failed: %v", err)
	}
	defer conn2.Close()

	_ = conn2.WriteJSON(helloReq)
	var resp2 protocol.Response
	_ = conn2.ReadJSON(&resp2)

	// 此时连接 1 应当被踢出（读取发生错误）
	_ = conn1.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, _, err = conn1.ReadMessage()
	if err == nil {
		t.Fatalf("expected conn1 to be closed after conn2 connected, but read succeeded")
	}

	// 连接 2 仍然在线
	if !registry.IsOnline(nodeID) {
		t.Fatalf("expected node to remain online on conn2")
	}

	// 验证数据库状态仍为 online (旧连接退出未将节点置为 offline)
	db := getTestDB(t)
	defer db.Close()
	var state string
	_ = db.QueryRow(context.Background(), "SELECT conn_state FROM nodes WHERE id = ?", nodeID).Scan(&state)
	if state != "online" {
		t.Fatalf("expected conn_state 'online', got %q", state)
	}
}

// TestWS_RevokeToken 验证吊销长期 Token 后主动断开连接与返回 -32000 (验收项 3)
func TestWS_RevokeToken(t *testing.T) {
	srv, registry, nodeID, token := setupTestServerAndNode(t)
	defer srv.Close()
	defer registry.Stop()

	// 用真正的 agent transport 运行
	tr, err := transport.New(transport.Config{
		ServerURL: srv.URL,
		Token:     token,
	})
	if err != nil {
		t.Fatalf("new transport failed: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("transport start failed: %v", err)
	}

	// 等待 WS 连接建立
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tr.State() == transport.StateWSConnected {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if tr.State() != transport.StateWSConnected {
		t.Fatalf("expected StateWSConnected, got %s", tr.State())
	}

	// 服务端执行吊销
	db := getTestDB(t)
	defer db.Close()
	err = registry.RevokeToken(context.Background(), nodeID, "user", "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("RevokeToken failed: %v", err)
	}

	// 校验数据库中 agent_token_hash 已置 NULL
	var tokenHash *string
	_ = db.QueryRow(context.Background(), "SELECT agent_token_hash FROM nodes WHERE id = ?", nodeID).Scan(&tokenHash)
	if tokenHash != nil {
		t.Fatalf("expected agent_token_hash to be NULL after revocation")
	}

	// 校验 audit_log 写入吊销审计
	var auditAction string
	_ = db.QueryRow(context.Background(), "SELECT action FROM audit_log WHERE target_id = ? AND action = 'node.revoke_token'", nodeID).Scan(&auditAction)
	if auditAction != "node.revoke_token" {
		t.Fatalf("expected audit log for node.revoke_token")
	}

	// Agent 侧收到 -32000，必须停止重连 (进入 StateStopped)
	stopDeadline := time.Now().Add(3 * time.Second)
	stopped := false
	for time.Now().Before(stopDeadline) {
		if tr.State() == transport.StateStopped {
			stopped = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !stopped {
		t.Fatalf("expected agent transport to transition to StateStopped after token revoked, got %s", tr.State())
	}

	// 再次发起 WS 握手，HTTP 升级前应直接返回 401
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agent/v1/rpc"
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	_, dialResp, dialErr := websocket.DefaultDialer.Dial(wsURL, headers)
	if dialErr == nil {
		t.Fatalf("expected dial to fail on revoked token")
	}
	if dialResp != nil && dialResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", dialResp.StatusCode)
	}
}

// TestWS_DisconnectAndOffline 验证 Agent 断开连接后标记为离线 (验收项 4)
func TestWS_DisconnectAndOffline(t *testing.T) {
	srv, registry, nodeID, token := setupTestServerAndNode(t)
	defer srv.Close()
	defer registry.Stop()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agent/v1/rpc"
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	// 发送 hello 使其上线
	reqID := int64(1)
	helloReq := protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		ID:      &reqID,
		Method:  protocol.MethodAgentHello,
	}
	helloParams, _ := json.Marshal(protocol.HelloParams{
		ProtocolVersion: protocol.ProtocolVersion,
		AgentVersion:    "0.1.0",
	})
	helloReq.Params = helloParams
	_ = conn.WriteJSON(helloReq)
	var helloResp protocol.Response
	_ = conn.ReadJSON(&helloResp)

	if !registry.IsOnline(nodeID) {
		t.Fatalf("expected node online")
	}

	// 模拟 agent 异常退出 (kill -9): 直接断开底层网络连接
	_ = conn.Close()

	// 等待服务端识别断开并更新数据库
	db := getTestDB(t)
	defer db.Close()

	offline := false
	checkDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(checkDeadline) {
		var state string
		_ = db.QueryRow(context.Background(), "SELECT conn_state FROM nodes WHERE id = ?", nodeID).Scan(&state)
		if state == "offline" {
			offline = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !offline {
		t.Fatalf("expected node to be marked 'offline' after connection closed")
	}
	if registry.IsOnline(nodeID) {
		t.Fatalf("expected node to be removed from registry")
	}
}

// TestWS_ClockSkewDetection 验证时钟调快 10 分钟入库时间仍然正确且打标记 (验收项 7)
func TestWS_ClockSkewDetection(t *testing.T) {
	srv, registry, nodeID, token := setupTestServerAndNode(t)
	defer srv.Close()
	defer registry.Stop()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agent/v1/rpc"
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// 握手
	reqID := int64(1)
	helloReq := protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		ID:      &reqID,
		Method:  protocol.MethodAgentHello,
	}
	helloParams, _ := json.Marshal(protocol.HelloParams{
		ProtocolVersion: protocol.ProtocolVersion,
		AgentVersion:    "0.1.0",
	})
	helloReq.Params = helloParams
	_ = conn.WriteJSON(helloReq)
	var helloResp protocol.Response
	_ = conn.ReadJSON(&helloResp)

	serverBeforeMs := time.Now().UnixMilli()

	// Agent 机器时间调快 10 分钟 (600,000 毫秒)
	fastAgentTime := serverBeforeMs + 600*1000

	metricsReq := protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		Method:  protocol.MethodAgentMetrics,
	}
	cpuVal := 12.5
	mParams, _ := json.Marshal(protocol.MetricsParams{
		TsMs:   fastAgentTime,
		CPUPct: &cpuVal,
	})
	metricsReq.Params = mParams

	if err := conn.WriteJSON(metricsReq); err != nil {
		t.Fatalf("send metrics failed: %v", err)
	}

	// 允许服务端处理
	time.Sleep(100 * time.Millisecond)

	serverAfterMs := time.Now().UnixMilli()

	// 校验数据库
	db := getTestDB(t)
	defer db.Close()

	var (
		lastSeenMs  int64
		clockSkewMs int64
	)
	err = db.QueryRow(context.Background(), "SELECT last_seen_at_ms, clock_skew_ms FROM nodes WHERE id = ?", nodeID).
		Scan(&lastSeenMs, &clockSkewMs)
	if err != nil {
		t.Fatalf("query nodes clock info failed: %v", err)
	}

	// 1. last_seen_at_ms 必须落在服务端真实时间区间内，绝不能来自未来 fastAgentTime
	if lastSeenMs < serverBeforeMs || lastSeenMs > serverAfterMs+500 {
		t.Fatalf("expected last_seen_at_ms in server time [%d, %d], got %d (agent reported %d)",
			serverBeforeMs, serverAfterMs, lastSeenMs, fastAgentTime)
	}

	// 2. clockSkewMs 应当记录明显的负偏差（约 -600,000 ms）
	if clockSkewMs > -550000 || clockSkewMs < -650000 {
		t.Fatalf("expected clock_skew_ms around -600000, got %d", clockSkewMs)
	}
}
