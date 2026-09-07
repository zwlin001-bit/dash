package control_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dash/internal/control"
	"dash/internal/protocol"
)

// TestEnrollment_SuccessAndReplay 验证验收项 1 和 2：
// 1. 生成 enrollment token → agent 注册成功 → 建出 nodes 行 → 返回长期 token
// 2. 同一个 enrollment token 再用一次被拒 (410 enroll_token_used)
func TestEnrollment_SuccessAndReplay(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()
	cleanTables(t, database)

	ctx := context.Background()

	// 1. 生成 enrollment token (预绑定节点名)
	presetName := "test-node-alpha"
	plainToken, tokenID, err := control.CreateEnrollmentToken(ctx, database, presetName, "", "admin_usr", 15*time.Minute)
	if err != nil {
		t.Fatalf("CreateEnrollmentToken failed: %v", err)
	}

	handler := control.NewEnrollHandler(database)

	facts := &protocol.FactsParams{
		Arch:      "amd64",
		OSName:    "debian",
		OSVersion: "12",
		Kernel:    "6.1.0-21-amd64",
		CPUModel:  "Intel Xeon",
		CPUCores:  2,
		FactsHash: "factshash123",
	}

	reqBody, _ := json.Marshal(control.EnrollRequest{
		EnrollToken: plainToken,
		Facts:       facts,
	})

	// 2. 发起注册请求
	req := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", bytes.NewReader(reqBody))
	req.RemoteAddr = "192.0.2.100:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp control.EnrollResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.NodeID == "" || resp.AgentToken == "" {
		t.Fatalf("expected non-empty node_id and agent_token, got: %+v", resp)
	}

	// 3. 校验 nodes 表中正确落库
	var (
		name           string
		agentTokenHash string
		connState      string
	)
	err = database.QueryRow(ctx, "SELECT name, agent_token_hash, conn_state FROM nodes WHERE id = ?", resp.NodeID).
		Scan(&name, &agentTokenHash, &connState)
	if err != nil {
		t.Fatalf("query nodes table failed: %v", err)
	}

	if name != presetName {
		t.Fatalf("expected node name %q, got %q", presetName, name)
	}
	if agentTokenHash != control.HashToken(resp.AgentToken) {
		t.Fatalf("expected agent_token_hash to match sha256(token)")
	}
	if connState != "never" {
		t.Fatalf("expected initial conn_state 'never', got %q", connState)
	}

	// 4. 校验 node_facts 正确落库，包含公网 IP 提取
	var (
		arch, osName, ipv4 string
	)
	err = database.QueryRow(ctx, "SELECT arch, os_name, ipv4 FROM node_facts WHERE node_id = ?", resp.NodeID).
		Scan(&arch, &osName, &ipv4)
	if err != nil {
		t.Fatalf("query node_facts failed: %v", err)
	}
	if arch != "amd64" || osName != "debian" || ipv4 != "192.0.2.100" {
		t.Fatalf("unexpected node_facts: arch=%s, os=%s, ip=%s", arch, osName, ipv4)
	}

	// 5. 校验 enroll_tokens 表已标记为使用
	var usedNodeID string
	var usedAtMs int64
	err = database.QueryRow(ctx, "SELECT used_node_id, used_at_ms FROM enroll_tokens WHERE id = ?", tokenID).
		Scan(&usedNodeID, &usedAtMs)
	if err != nil {
		t.Fatalf("query enroll_tokens failed: %v", err)
	}
	if usedNodeID != resp.NodeID || usedAtMs <= 0 {
		t.Fatalf("expected token used by %s with used_at_ms > 0, got %s, %d", resp.NodeID, usedNodeID, usedAtMs)
	}

	// 6. 校验 audit_log 表写入审计日志
	var auditAction, auditTargetID, auditResult string
	err = database.QueryRow(ctx, "SELECT action, target_id, result FROM audit_log WHERE target_id = ?", resp.NodeID).
		Scan(&auditAction, &auditTargetID, &auditResult)
	if err != nil {
		t.Fatalf("query audit_log failed: %v", err)
	}
	if auditAction != "node.enroll" || auditTargetID != resp.NodeID || auditResult != "ok" {
		t.Fatalf("unexpected audit log: action=%s, target=%s, result=%s", auditAction, auditTargetID, auditResult)
	}

	// 7. 同一个 enrollment token 再用一次，必须被拒 (410 enroll_token_used)
	req2 := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", bytes.NewReader(reqBody))
	req2.RemoteAddr = "192.0.2.101:12345"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone for reused token, got %d: %s", rr2.Code, rr2.Body.String())
	}
	if !bytes.Contains(rr2.Body.Bytes(), []byte("enroll_token_used")) {
		t.Fatalf("expected enroll_token_used error message, got %s", rr2.Body.String())
	}
}

// TestEnrollment_ExpiredToken 验证过期 Token 被拒 (410 enroll_token_expired)
func TestEnrollment_ExpiredToken(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()
	cleanTables(t, database)

	ctx := context.Background()

	// 签发一个已过期的 token (ttl = -1s)
	plainToken, _, err := control.CreateEnrollmentToken(ctx, database, "expired-node", "", "admin", -time.Second)
	if err != nil {
		t.Fatalf("create expired token failed: %v", err)
	}

	handler := control.NewEnrollHandler(database)
	reqBody, _ := json.Marshal(control.EnrollRequest{
		EnrollToken: plainToken,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone, got %d: %s", rr.Code, rr.Body.String())
	}
	bodyBytes := rr.Body.Bytes()
	if !bytes.Contains(bodyBytes, []byte("enroll_token_expired")) {
		t.Fatalf("expected enroll_token_expired, got %s", string(bodyBytes))
	}
}

// TestEnrollment_InvalidToken 验证无效 Token 返回 400
func TestEnrollment_InvalidToken(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()
	cleanTables(t, database)

	handler := control.NewEnrollHandler(database)
	reqBody, _ := json.Marshal(control.EnrollRequest{
		EnrollToken: "non_existent_token_123",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestEnrollment_RateLimit 验证 IP 限流（每分钟最多 10 次，第 11 次返回 429）
func TestEnrollment_RateLimit(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()
	cleanTables(t, database)

	handler := control.NewEnrollHandler(database)
	ip := "198.51.100.5"

	// 前 10 次即使 token 无效返回 400
	for i := 0; i < 10; i++ {
		reqBody, _ := json.Marshal(control.EnrollRequest{EnrollToken: "fake_token"})
		req := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", bytes.NewReader(reqBody))
		req.RemoteAddr = ip + ":54321"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("iteration %d: expected 400, got %d", i, rr.Code)
		}
	}

	// 第 11 次必须返回 429 Too Many Requests
	reqBody, _ := json.Marshal(control.EnrollRequest{EnrollToken: "fake_token"})
	req := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", bytes.NewReader(reqBody))
	req.RemoteAddr = ip + ":54321"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests on 11th call, got %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("rate_limited")) {
		t.Fatalf("expected rate_limited in body, got %s", rr.Body.String())
	}
}
