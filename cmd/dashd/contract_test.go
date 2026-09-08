package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dash/internal/app"
	"dash/internal/auth"
	"dash/internal/config"
	"dash/internal/events"
	"dash/internal/ulid"
)

// TestAPIContractFixturesAndValidation (P1-27 机制 ❷ Option A):
// 服务端起真实 handler，产出真实 JSON 响应并验证/持久化为契约 Fixture。
// 前端契约测试将消费完全相同的 Fixture 进行断言，任何字段或信封不匹配直接阻断构建/测试。
func TestAPIContractFixturesAndValidation(t *testing.T) {
	database := setupFullTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cfg := config.DefaultConfig()
	a := app.NewApp(database, cfg, "v1.0.0-contract-test")
	defer a.Close()

	if err := RegisterModules(a, modules); err != nil {
		t.Fatalf("failed to register modules: %v", err)
	}
	a.HandlePublic("/healthz", a.HealthzHandler())

	// 1. 创建测试用户并登录获取会话 Cookie
	authSvc := auth.NewService(database)
	testUser := "contract_admin_" + ulid.New()
	testPass := "ContractPass123!"
	_, err := authSvc.CreateUser(ctx, testUser, testPass, true)
	if err != nil {
		t.Fatalf("failed to create admin: %v", err)
	}

	loginBody, _ := json.Marshal(map[string]string{
		"username": testUser,
		"password": testPass,
	})
	reqLogin := httptest.NewRequest("POST", "/api/v1/login", bytes.NewReader(loginBody))
	reqLogin.Header.Set("Content-Type", "application/json")
	recLogin := httptest.NewRecorder()
	a.Mux.ServeHTTP(recLogin, reqLogin)
	if recLogin.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", recLogin.Code, recLogin.Body.String())
	}
	cookies := recLogin.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("session cookie not found")
	}

	// 2. 种子数据准备（确保各个列表端点有真实项）
	nodeID := ulid.New()
	groupID := ulid.New()
	tagID := ulid.New()
	tokenID := ulid.New()
	eventID := ulid.New()
	now := time.Now().UnixMilli()

	// 清理并注入种子
	_, _ = database.Exec(ctx, `DELETE FROM node_tags WHERE node_id = ?`, nodeID)
	_, _ = database.Exec(ctx, `DELETE FROM nodes WHERE id = ?`, nodeID)
	_, _ = database.Exec(ctx, `DELETE FROM node_groups WHERE id = ?`, groupID)
	_, _ = database.Exec(ctx, `DELETE FROM tags WHERE id = ?`, tagID)
	_, _ = database.Exec(ctx, `DELETE FROM enroll_tokens WHERE id = ?`, tokenID)
	_, _ = database.Exec(ctx, `DELETE FROM events WHERE id = ?`, eventID)

	_, err = database.Exec(ctx, `INSERT INTO node_groups (id, name, display_order, created_at_ms, updated_at_ms) VALUES (?, ?, 10, ?, ?)`,
		groupID, "grp-"+ulid.New(), now, now)
	if err != nil {
		t.Fatalf("seed group failed: %v", err)
	}

	_, err = database.Exec(ctx, `INSERT INTO tags (id, name, color, created_at_ms, updated_at_ms) VALUES (?, ?, '#3b82f6', ?, ?)`,
		tagID, "tag-"+ulid.New(), now, now)
	if err != nil {
		t.Fatalf("seed tag failed: %v", err)
	}

	_, err = database.Exec(ctx, `INSERT INTO nodes (id, name, node_group_id, display_order, is_hidden, conn_state, last_seen_at_ms, created_at_ms, updated_at_ms) VALUES (?, ?, ?, 0, 0, 'online', ?, ?, ?)`,
		nodeID, "node-"+ulid.New(), groupID, now, now, now)
	if err != nil {
		t.Fatalf("seed node failed: %v", err)
	}

	_, err = database.Exec(ctx, `INSERT INTO node_tags (node_id, tag_id) VALUES (?, ?)`,
		nodeID, tagID)
	if err != nil {
		t.Fatalf("seed node_tag failed: %v", err)
	}

	_, err = database.Exec(ctx, `INSERT INTO enroll_tokens (id, token_hash, preset_name, preset_group_id, expires_at_ms, created_by, created_at_ms) VALUES (?, ?, 'token-preset', ?, ?, 'admin', ?)`,
		tokenID, "hash-"+ulid.New(), groupID, now+3600000, now)
	if err != nil {
		t.Fatalf("seed enroll_token failed: %v", err)
	}

	_, err = database.Exec(ctx, `INSERT INTO events (id, event_type, severity, source_module, target_kind, target_id, title, is_read, occurred_at_ms, created_at_ms) VALUES (?, 'node.online', 'info', 'inventory', 'node', ?, 'Node Online', 0, ?, ?)`,
		eventID, nodeID, now, now)
	if err != nil {
		t.Fatalf("seed event failed: %v", err)
	}

	// 3. 执行端点请求并保存/比对 Fixture
	fixturesDir := filepath.Join("..", "..", "web", "test", "fixtures", "contracts")
	if err := os.MkdirAll(fixturesDir, 0755); err != nil {
		t.Fatalf("failed to create fixtures dir: %v", err)
	}

	type endpointTest struct {
		name        string
		method      string
		url         string
		reqBody     any
		fixtureFile string
		validate    func(t *testing.T, body []byte)
	}

	tests := []endpointTest{
		{
			name:        "GET /api/v1/nodes (populated)",
			method:      "GET",
			url:         "/api/v1/nodes",
			fixtureFile: "nodes.json",
			validate: func(t *testing.T, body []byte) {
				var res struct {
					Items    []map[string]any `json:"items"`
					Total    int64            `json:"total"`
					Page     int              `json:"page"`
					PageSize int              `json:"page_size"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse nodes error: %v", err)
				}
				if len(res.Items) == 0 {
					t.Fatal("expected non-empty items in nodes response")
				}
				if res.Items[0]["id"] == "" || res.Items[0]["name"] == "" {
					t.Fatalf("missing id/name in node item: %+v", res.Items[0])
				}
			},
		},
		{
			name:        "GET /api/v1/nodes (empty)",
			method:      "GET",
			url:         "/api/v1/nodes?q=non_existent_node_xyz",
			fixtureFile: "nodes_empty.json",
			validate: func(t *testing.T, body []byte) {
				var res struct {
					Items []map[string]any `json:"items"`
					Total int64            `json:"total"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse nodes error: %v", err)
				}
				if res.Items == nil {
					t.Fatal("items must be [] not null")
				}
				if len(res.Items) != 0 {
					t.Fatalf("expected 0 items, got %d", len(res.Items))
				}
			},
		},
		{
			name:        "GET /api/v1/node-groups (populated)",
			method:      "GET",
			url:         "/api/v1/node-groups",
			fixtureFile: "groups.json",
			validate: func(t *testing.T, body []byte) {
				var res struct {
					Items    []map[string]any `json:"items"`
					Total    int64            `json:"total"`
					Page     int              `json:"page"`
					PageSize int              `json:"page_size"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse groups error: %v", err)
				}
				if len(res.Items) == 0 {
					t.Fatal("expected non-empty items in groups response")
				}
			},
		},
		{
			name:        "GET /api/v1/node-groups (empty)",
			method:      "GET",
			url:         "/api/v1/node-groups?page=9999",
			fixtureFile: "groups_empty.json",
			validate: func(t *testing.T, body []byte) {
				var res struct {
					Items []map[string]any `json:"items"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse groups error: %v", err)
				}
				if res.Items == nil {
					t.Fatal("items must be [] not null")
				}
			},
		},
		{
			name:        "GET /api/v1/tags (populated)",
			method:      "GET",
			url:         "/api/v1/tags",
			fixtureFile: "tags.json",
			validate: func(t *testing.T, body []byte) {
				var res struct {
					Items    []map[string]any `json:"items"`
					Total    int64            `json:"total"`
					Page     int              `json:"page"`
					PageSize int              `json:"page_size"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse tags error: %v", err)
				}
				if len(res.Items) == 0 {
					t.Fatal("expected non-empty items in tags response")
				}
			},
		},
		{
			name:        "GET /api/v1/tags (empty)",
			method:      "GET",
			url:         "/api/v1/tags?page=9999",
			fixtureFile: "tags_empty.json",
			validate: func(t *testing.T, body []byte) {
				var res struct {
					Items []map[string]any `json:"items"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse tags error: %v", err)
				}
				if res.Items == nil {
					t.Fatal("items must be [] not null")
				}
			},
		},
		{
			name:        "GET /api/v1/enroll-tokens (populated)",
			method:      "GET",
			url:         "/api/v1/enroll-tokens",
			fixtureFile: "enroll_tokens.json",
			validate: func(t *testing.T, body []byte) {
				var res struct {
					Items    []map[string]any `json:"items"`
					Total    int64            `json:"total"`
					Page     int              `json:"page"`
					PageSize int              `json:"page_size"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse enroll tokens error: %v", err)
				}
				if len(res.Items) == 0 {
					t.Fatal("expected non-empty items in enroll tokens response")
				}
			},
		},
		{
			name:        "GET /api/v1/enroll-tokens (empty)",
			method:      "GET",
			url:         "/api/v1/enroll-tokens?page=9999",
			fixtureFile: "enroll_tokens_empty.json",
			validate: func(t *testing.T, body []byte) {
				var res struct {
					Items []map[string]any `json:"items"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse enroll tokens error: %v", err)
				}
				if res.Items == nil {
					t.Fatal("items must be [] not null")
				}
			},
		},
		{
			name:        "GET /api/v1/events (populated)",
			method:      "GET",
			url:         "/api/v1/events",
			fixtureFile: "events.json",
			validate: func(t *testing.T, body []byte) {
				var res struct {
					Items  []map[string]any `json:"items"`
					Total  int64            `json:"total"`
					Limit  int              `json:"limit"`
					Offset int              `json:"offset"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse events error: %v", err)
				}
				if len(res.Items) == 0 {
					t.Fatal("expected non-empty items in events response")
				}
			},
		},
		{
			name:        "GET /api/v1/events (empty)",
			method:      "GET",
			url:         "/api/v1/events?target_id=non_existent_target",
			fixtureFile: "events_empty.json",
			validate: func(t *testing.T, body []byte) {
				var res struct {
					Items  []map[string]any `json:"items"`
					Total  int64            `json:"total"`
					Limit  int              `json:"limit"`
					Offset int              `json:"offset"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse events error: %v", err)
				}
				if res.Items == nil {
					t.Fatal("items must be [] not null")
				}
			},
		},
		{
			name:        "GET /api/v1/event-types",
			method:      "GET",
			url:         "/api/v1/event-types",
			fixtureFile: "event_types.json",
			validate: func(t *testing.T, body []byte) {
				var res struct {
					Items []events.TypeDef `json:"items"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse event types error: %v", err)
				}
				if len(res.Items) == 0 {
					t.Fatal("expected non-empty items in event types response")
				}
			},
		},
		{
			name:        "GET /api/v1/settings",
			method:      "GET",
			url:         "/api/v1/settings",
			fixtureFile: "settings.json",
			validate: func(t *testing.T, body []byte) {
				var res map[string]any
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse settings error: %v", err)
				}
				if len(res) == 0 {
					t.Fatal("expected non-empty settings response")
				}
			},
		},
		{
			name:        "POST /api/v1/nodes/{id}/tags",
			method:      "POST",
			url:         fmt.Sprintf("/api/v1/nodes/%s/tags", nodeID),
			reqBody:     map[string]any{"tag_ids": []string{tagID}},
			fixtureFile: "replace_tags.json",
			validate: func(t *testing.T, body []byte) {
				var res map[string]any
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse replace tags response error: %v", err)
				}
				if res["ok"] != true {
					t.Fatalf("expected ok=true, got %+v", res)
				}
			},
		},
		{
			name:        "GET /healthz",
			method:      "GET",
			url:         "/healthz",
			fixtureFile: "healthz.json",
			validate: func(t *testing.T, body []byte) {
				var res map[string]any
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("parse healthz error: %v", err)
				}
				if res["status"] != "ok" {
					t.Fatalf("expected status=ok, got %+v", res)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.reqBody != nil {
				b, _ := json.Marshal(tc.reqBody)
				req = httptest.NewRequest(tc.method, tc.url, bytes.NewReader(b))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(tc.method, tc.url, nil)
			}
			req.AddCookie(sessionCookie)

			rec := httptest.NewRecorder()
			a.Mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("%s returned status %d, body: %s", tc.name, rec.Code, rec.Body.String())
			}

			rawBytes := rec.Body.Bytes()
			tc.validate(t, rawBytes)

			// 仅在显式指定 DASH_UPDATE_FIXTURES=1 时覆写 Fixture 文件（P2-07 机制已移交给 cmd/gen-fixtures）
			if os.Getenv("DASH_UPDATE_FIXTURES") == "1" {
				var pretty bytes.Buffer
				if err := json.Indent(&pretty, rawBytes, "", "  "); err != nil {
					t.Fatalf("json indent error: %v", err)
				}
				pretty.WriteString("\n")

				fixturePath := filepath.Join(fixturesDir, tc.fixtureFile)
				if err := os.WriteFile(fixturePath, pretty.Bytes(), 0644); err != nil {
					t.Fatalf("failed to write fixture %s: %v", fixturePath, err)
				}
			}
		})
	}
}
