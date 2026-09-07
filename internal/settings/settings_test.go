package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dash/internal/control"
	"dash/internal/db"
	"dash/internal/protocol"

	"github.com/gorilla/websocket"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	database, err := db.Open(cfg)
	if err != nil {
		t.Skipf("Skipping DB test: cannot connect to test MySQL: %v", err)
	}
	_, _ = database.Exec(context.Background(), "DELETE FROM settings WHERE setting_key LIKE 'collect.%' OR setting_key LIKE 'retention.%'")
	return database
}

func TestSettingsCRUD(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	registry := control.NewRegistry(database, nil)
	svc := NewService(database, registry)

	ctx := context.Background()

	// 1. Get default settings
	st, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings failed: %v", err)
	}
	if st.CollectIntervalFastS != 5 {
		t.Errorf("expected default interval_fast_s 5, got %d", st.CollectIntervalFastS)
	}

	// 2. Update settings
	updates := map[string]any{
		"collect.interval_fast_s": 10,
		"collect.interval_slow_s": 120,
		"collect.enable_conns":    false,
		"retention.raw_days":      5,
	}
	updated, err := svc.UpdateSettings(ctx, updates, "user", "u1", "127.0.0.1")
	if err != nil {
		t.Fatalf("UpdateSettings failed: %v", err)
	}
	if updated.CollectIntervalFastS != 10 {
		t.Errorf("expected 10, got %d", updated.CollectIntervalFastS)
	}
	if updated.CollectIntervalSlowS != 120 {
		t.Errorf("expected 120, got %d", updated.CollectIntervalSlowS)
	}
	if updated.CollectEnableConns != false {
		t.Errorf("expected false, got %v", updated.CollectEnableConns)
	}
	if updated.RetentionRawDays != 5 {
		t.Errorf("expected 5, got %d", updated.RetentionRawDays)
	}

	// 3. Reject modifying domain
	_, err = svc.UpdateSettings(ctx, map[string]any{"site.domain": "new.com"}, "user", "u1", "127.0.0.1")
	if err == nil {
		t.Errorf("expected domain modification to fail")
	}

	// 4. Reject invalid interval
	_, err = svc.UpdateSettings(ctx, map[string]any{"collect.interval_fast_s": 0}, "user", "u1", "127.0.0.1")
	if err == nil {
		t.Errorf("expected invalid fast interval to fail")
	}
}

func TestSettingsHTTPHandlers(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	registry := control.NewRegistry(database, nil)
	svc := NewService(database, registry)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/settings", svc.HandleGet)
	mux.HandleFunc("PATCH /api/v1/settings", svc.HandlePatch)

	// GET
	req := httptest.NewRequest("GET", "/api/v1/settings", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/settings status = %d, want 200", rec.Code)
	}

	var st SystemSettings
	if err := json.NewDecoder(rec.Body).Decode(&st); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}

	// PATCH
	patchBody, _ := json.Marshal(map[string]any{
		"collect.interval_fast_s": 15,
	})
	req = httptest.NewRequest("PATCH", "/api/v1/settings", bytes.NewReader(patchBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/v1/settings status = %d, want 200", rec.Code)
	}

	var patched SystemSettings
	if err := json.NewDecoder(rec.Body).Decode(&patched); err != nil {
		t.Fatalf("decode PATCH response: %v", err)
	}
	if patched.CollectIntervalFastS != 15 {
		t.Errorf("expected 15, got %d", patched.CollectIntervalFastS)
	}
}

// TestLiveAgentConfigBroadcastNoReconnect 验证验收项 4：
// 改采集间隔后，已连接 agent 的上报频率跟着变，且没有重连。
func TestLiveAgentConfigBroadcastNoReconnect(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	registry := control.NewRegistry(database, nil)
	registry.Start(context.Background())
	defer registry.Stop()

	svc := NewService(database, registry)

	mux := http.NewServeMux()
	control.RegisterRoutes(mux, database, registry, nil)
	mux.HandleFunc("GET /api/v1/settings", svc.HandleGet)
	mux.HandleFunc("PATCH /api/v1/settings", svc.HandlePatch)

	server := httptest.NewServer(mux)
	defer server.Close()

	ctx := context.Background()

	// 1. 注册新节点以获取长期 agentToken
	plainToken, _, err := control.CreateEnrollmentToken(ctx, database, "test-node-config", "", "admin", 10*time.Minute)
	if err != nil {
		t.Fatalf("create enroll token: %v", err)
	}

	enrollHandler := control.NewEnrollHandler(database)
	enrollBody, _ := json.Marshal(control.EnrollRequest{
		EnrollToken: plainToken,
		Facts: &protocol.FactsParams{
			Arch:   "amd64",
			OSName: "linux",
		},
	})
	enrollReq := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", strings.NewReader(string(enrollBody)))
	enrollRec := httptest.NewRecorder()
	enrollHandler.ServeHTTP(enrollRec, enrollReq)
	if enrollRec.Code != http.StatusOK {
		t.Fatalf("enroll status: %d", enrollRec.Code)
	}
	var enrollResp control.EnrollResponse
	_ = json.NewDecoder(enrollRec.Body).Decode(&enrollResp)

	// 2. Agent 建立长连接
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent/v1/rpc"
	header := http.Header{
		"Authorization": []string{"Bearer " + enrollResp.AgentToken},
	}
	clientConn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial ws failed: %v (resp=%v)", err, resp)
	}
	defer clientConn.Close()

	// 3. 发送 agent.hello 握手
	var reqID int64 = 1
	helloReq := protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		ID:      &reqID,
		Method:  protocol.MethodAgentHello,
		Params:  json.RawMessage(`{"version":"0.1.0","protocol_version":1,"arch":"amd64","boot_at_ms":1000000}`),
	}
	if err := clientConn.WriteJSON(helloReq); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	var helloResp protocol.Response
	if err := clientConn.ReadJSON(&helloResp); err != nil {
		t.Fatalf("read hello response: %v", err)
	}
	if helloResp.Error != nil {
		t.Fatalf("hello returned error: %v", helloResp.Error)
	}

	// 确认节点在线
	if !registry.IsOnline(enrollResp.NodeID) {
		t.Fatalf("expected node %s to be online", enrollResp.NodeID)
	}

	// 4. 用户通过管理端 PATCH /api/v1/settings 修改 collect.interval_fast_s = 2, collect.interval_slow_s = 30
	patchBody, _ := json.Marshal(map[string]any{
		"collect.interval_fast_s": 2,
		"collect.interval_slow_s": 30,
	})
	patchReq, _ := http.NewRequest("PATCH", server.URL+"/api/v1/settings", bytes.NewReader(patchBody))
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("PATCH settings failed: %v", err)
	}
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH settings status: %d", patchResp.StatusCode)
	}

	// 5. Agent 在原长连接上即时收到 server.config 通知（没有断连）
	_ = clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var srvNotification protocol.Request
	if err := clientConn.ReadJSON(&srvNotification); err != nil {
		t.Fatalf("expected server.config notification on existing connection, but got err: %v", err)
	}

	if srvNotification.Method != protocol.MethodServerConfig {
		t.Fatalf("expected method %q, got %q", protocol.MethodServerConfig, srvNotification.Method)
	}

	var params protocol.ServerConfigParams
	if err := json.Unmarshal(srvNotification.Params, &params); err != nil {
		t.Fatalf("unmarshal server.config params: %v", err)
	}

	if params.IntervalFastS == nil || *params.IntervalFastS != 2 {
		t.Fatalf("expected interval_fast_s 2, got %v", params.IntervalFastS)
	}
	if params.IntervalSlowS == nil || *params.IntervalSlowS != 30 {
		t.Fatalf("expected interval_slow_s 30, got %v", params.IntervalSlowS)
	}

	// 确认连接依然处于保持状态（没有重连或断开）
	if !registry.IsOnline(enrollResp.NodeID) {
		t.Fatalf("node should remain online without reconnecting")
	}
}

