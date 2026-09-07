package control_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"dash/agent/transport"
	"dash/internal/protocol"
)

// TestFallback_ReportAndCommands 验证 HTTP 回退端点 /api/agent/v1/report
func TestFallback_ReportAndCommands(t *testing.T) {
	srv, registry, nodeID, token := setupTestServerAndNode(t)
	defer srv.Close()
	defer registry.Stop()

	// 1. 无认证请求被拒 (401)
	reportURL := srv.URL + "/api/agent/v1/report"
	req, _ := http.NewRequest(http.MethodPost, reportURL, strings.NewReader("[]"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http post failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized on missing token, got %d", resp.StatusCode)
	}

	// 2. 准备回退下行指令
	cfgFast := 10
	cfgSlow := 120
	cmdReq := &protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		Method:  protocol.MethodServerConfig,
	}
	cmdParams, _ := json.Marshal(protocol.ServerConfigParams{
		IntervalFastS: &cfgFast,
		IntervalSlowS: &cfgSlow,
	})
	cmdReq.Params = cmdParams
	registry.EnqueueFallbackCommand(nodeID, cmdReq)

	// 3. 使用 agent transport 提供的 HTTPFallbackClient 发起上报
	fbClient := transport.NewHTTPFallbackClient(reportURL, token, nil)

	cpuVal := 5.5
	batch := []*protocol.Request{
		{
			JSONRPC: protocol.JSONRPCVersion,
			Method:  protocol.MethodAgentMetrics,
		},
		{
			JSONRPC: protocol.JSONRPCVersion,
			Method:  protocol.MethodAgentFacts,
		},
	}
	mParams, _ := json.Marshal(protocol.MetricsParams{
		TsMs:   time.Now().UnixMilli(),
		CPUPct: &cpuVal,
	})
	batch[0].Params = mParams

	fParams, _ := json.Marshal(protocol.FactsParams{
		Arch:      "arm64",
		OSName:    "alpine",
		FactsHash: "fallback_facts_hash",
	})
	batch[1].Params = fParams

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fbResp, err := fbClient.PostReport(ctx, batch)
	if err != nil {
		t.Fatalf("PostReport failed: %v", err)
	}

	// 4. 验证响应与回带指令
	if fbResp.ServerTimeMs <= 0 {
		t.Fatalf("expected positive server_time_ms, got %d", fbResp.ServerTimeMs)
	}
	if len(fbResp.Commands) != 1 {
		t.Fatalf("expected 1 command returned, got %d", len(fbResp.Commands))
	}
	if fbResp.Commands[0].Method != protocol.MethodServerConfig {
		t.Fatalf("expected command method %s, got %s", protocol.MethodServerConfig, fbResp.Commands[0].Method)
	}

	// 5. 校验数据库中 node 状态已刷新为 online，facts 已落库
	db := getTestDB(t)
	defer db.Close()

	var state string
	_ = db.QueryRow(context.Background(), "SELECT conn_state FROM nodes WHERE id = ?", nodeID).Scan(&state)
	if state != "online" {
		t.Fatalf("expected conn_state 'online', got %q", state)
	}

	var arch string
	_ = db.QueryRow(context.Background(), "SELECT arch FROM node_facts WHERE node_id = ?", nodeID).Scan(&arch)
	if arch != "arm64" {
		t.Fatalf("expected node_facts arch 'arm64', got %q", arch)
	}

	// 6. 再次上报，指令已被消费清空
	fbResp2, err := fbClient.PostReport(ctx, []*protocol.Request{})
	if err != nil {
		t.Fatalf("second PostReport failed: %v", err)
	}
	if len(fbResp2.Commands) != 0 {
		t.Fatalf("expected 0 commands on second report, got %d", len(fbResp2.Commands))
	}
}

// TestFallback_RevokedToken 验证吊销长期 Token 后 HTTP 回退返回 401 (-32000)
func TestFallback_RevokedToken(t *testing.T) {
	srv, registry, nodeID, token := setupTestServerAndNode(t)
	defer srv.Close()
	defer registry.Stop()

	reportURL := srv.URL + "/api/agent/v1/report"
	fbClient := transport.NewHTTPFallbackClient(reportURL, token, nil)

	// 吊销 token
	_ = registry.RevokeToken(context.Background(), nodeID, "user", "admin", "127.0.0.1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := fbClient.PostReport(ctx, []*protocol.Request{
		{JSONRPC: protocol.JSONRPCVersion, Method: protocol.MethodAgentMetrics},
	})
	if err == nil {
		t.Fatalf("expected error on revoked token report")
	}

	rpcErr, ok := err.(*protocol.RPCError)
	if !ok || rpcErr.Code != protocol.ErrCodeTokenInvalid {
		t.Fatalf("expected ErrCodeTokenInvalid (-32000), got %v", err)
	}
}
