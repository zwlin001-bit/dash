package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dash/internal/app"
	"dash/internal/auth"
	"dash/internal/config"
	"dash/internal/db"
	"dash/internal/migrate"
	"dash/internal/ulid"
)

// TestConsoleAPIRoutesAntiRegression 遍历整个服务端的路由注册表，
// 断言除免鉴权白名单外的每个 /api/v1/* 路由都被鉴权中间件包裹 (P1-22)。
func TestConsoleAPIRoutesAntiRegression(t *testing.T) {
	cfg := config.DefaultConfig()
	a := app.NewApp(nil, cfg, "test-version")

	if err := RegisterModules(a, modules); err != nil {
		t.Fatalf("failed to register modules: %v", err)
	}
	// 注册 healthz 探活端点（与 main.go 一致）
	a.HandlePublic("/healthz", a.HealthzHandler())

	routes := a.Routes()
	if len(routes) == 0 {
		t.Fatal("expected registered routes, got 0")
	}

	// 允许公开的免鉴权白名单 (docs/agy/tasks/P1-22-控制台API鉴权.md)
	isWhitelisted := func(pattern string) bool {
		switch {
		case pattern == "POST /api/v1/login":
			return true
		case strings.Contains(pattern, "/api/agent/v1/"):
			return true
		case pattern == "/healthz":
			return true
		case pattern == "GET /install.sh":
			return true
		case strings.HasPrefix(pattern, "GET /dl/"):
			return true
		case pattern == "/":
			return true
		default:
			return false
		}
	}

	apiV1Count := 0
	for _, r := range routes {
		isApiV1 := strings.Contains(r.Pattern, "/api/v1/")
		if isApiV1 {
			apiV1Count++
		}

		if !r.Authed {
			// 未开启鉴权的路由必须属于显式白名单
			if !isWhitelisted(r.Pattern) {
				t.Fatalf("❌ 路由 %q 未开启鉴权且不在免鉴权白名单中！", r.Pattern)
			}
		} else {
			// 开启鉴权的路由绝不应该属于公开白名单端点（如 login、install.sh 等）
			if r.Pattern == "POST /api/v1/login" {
				t.Fatalf("❌ 登录端点 %q 不应被标记为 Authed", r.Pattern)
			}
		}
	}

	if apiV1Count < 10 {
		t.Fatalf("expected at least 10 /api/v1/* routes, found %d", apiV1Count)
	}
}

// TestUnauthenticatedRequestsBlocked 验证未经认证的请求访问控制台核心 API 全部返回 401
func TestUnauthenticatedRequestsBlocked(t *testing.T) {
	cfg := config.DefaultConfig()
	a := app.NewApp(nil, cfg, "test-version")

	if err := RegisterModules(a, modules); err != nil {
		t.Fatalf("failed to register modules: %v", err)
	}
	a.HandlePublic("/healthz", a.HealthzHandler())

	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		// 1. 节点与分组
		{"GET", "/api/v1/nodes", ""},
		{"POST", "/api/v1/nodes", `{"name":"evil"}`},
		{"GET", "/api/v1/nodes/someid", ""},
		{"PATCH", "/api/v1/nodes/someid", `{"name":"evil"}`},
		{"DELETE", "/api/v1/nodes/someid", ""},
		{"GET", "/api/v1/node-groups", ""},
		{"POST", "/api/v1/node-groups", `{"name":"evil"}`},
		{"GET", "/api/v1/tags", ""},
		{"POST", "/api/v1/tags", `{"name":"evil"}`},
		// 2. 装机令牌铸造（安全高危）
		{"GET", "/api/v1/enroll-tokens", ""},
		{"POST", "/api/v1/enroll-tokens", `{}`},
		{"DELETE", "/api/v1/enroll-tokens/anyid", ""},
		// 3. 设置与事件
		{"GET", "/api/v1/settings", ""},
		{"PATCH", "/api/v1/settings", `{}`},
		{"GET", "/api/v1/events", ""},
		{"POST", "/api/v1/events/read", `{}`},
		{"GET", "/api/v1/events/unread-count", ""},
		// 4. 用户自身
		{"GET", "/api/v1/me", ""},
		{"POST", "/api/v1/logout", ""},
	}

	for _, ep := range endpoints {
		// a. 完全不带凭据
		req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
		rec := httptest.NewRecorder()
		a.Mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for %s %s without credentials, got %d (body: %s)",
				ep.method, ep.path, rec.Code, rec.Body.String())
		}

		// b. 伪造 Cookie 必须返回 401
		reqBogus := httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
		reqBogus.Header.Set("Cookie", "dash_session=bogus_session_value")
		recBogus := httptest.NewRecorder()
		a.Mux.ServeHTTP(recBogus, reqBogus)

		if recBogus.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for %s %s with bogus cookie, got %d",
				ep.method, ep.path, recBogus.Code)
		}
	}
}

// TestPublicWhitelistEndpoints 验证免鉴权白名单端点未被 401 拦截
func TestPublicWhitelistEndpoints(t *testing.T) {
	cfg := config.DefaultConfig()
	a := app.NewApp(nil, cfg, "test-version")

	if err := RegisterModules(a, modules); err != nil {
		t.Fatalf("failed to register modules: %v", err)
	}
	a.HandlePublic("/healthz", a.HealthzHandler())

	publicEndpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/healthz"},
		{"GET", "/install.sh"},
		{"GET", "/dl/sha256sums.txt"},
		{"GET", "/"},
	}

	for _, ep := range publicEndpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		a.Mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusUnauthorized {
			t.Errorf("expected %s %s to NOT be 401, got 401", ep.method, ep.path)
		}
	}
}

func setupFullTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("Skipping test: MySQL test DB not accessible: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	mig := migrate.New(d, "../../migrations")
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("migration up failed: %v", err)
	}

	_, _ = d.Exec(ctx, "DELETE FROM job_steps")
	_, _ = d.Exec(ctx, "DELETE FROM jobs")
	_, _ = d.Exec(ctx, "DELETE FROM user_sessions")
	_, _ = d.Exec(ctx, "DELETE FROM account_users")

	return d
}

// TestAuthEndToEndIntegration 端到端验证登录、鉴权放行、Bearer token与防爆破限流 (P1-22)
func TestAuthEndToEndIntegration(t *testing.T) {
	database := setupFullTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg := config.DefaultConfig()
	a := app.NewApp(database, cfg, "v1.0.0-test")
	defer a.Close()

	if err := RegisterModules(a, modules); err != nil {
		t.Fatalf("failed to register modules: %v", err)
	}
	a.HandlePublic("/healthz", a.HealthzHandler())

	authSvc := auth.NewService(database)
	testUser := "admin_" + ulid.New()
	testPass := "SecurePass123!"

	_, err := authSvc.CreateUser(ctx, testUser, testPass, true)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	// 1. 未登录访问 /api/v1/nodes -> 401
	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	rec := httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", rec.Code)
	}

	// 2. 正常登录 -> 200 + Cookie
	loginBody, _ := json.Marshal(map[string]string{
		"username": testUser,
		"password": testPass,
	})
	req = httptest.NewRequest("POST", "/api/v1/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for login, got %d, body: %s", rec.Code, rec.Body.String())
	}

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("expected dash_session cookie, got %v", cookies)
	}

	// 3. 携带 Cookie 访问 -> 200
	reqAuthed := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	reqAuthed.AddCookie(sessionCookie)
	recAuthed := httptest.NewRecorder()
	a.Mux.ServeHTTP(recAuthed, reqAuthed)
	if recAuthed.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid session cookie, got %d, body: %s",
			recAuthed.Code, recAuthed.Body.String())
	}

	// 4. 携带 Authorization: Bearer <token> 访问 -> 200
	reqBearer := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	reqBearer.Header.Set("Authorization", "Bearer "+sessionCookie.Value)
	recBearer := httptest.NewRecorder()
	a.Mux.ServeHTTP(recBearer, reqBearer)
	if recBearer.Code != http.StatusOK {
		t.Fatalf("expected 200 with Bearer token, got %d, body: %s",
			recBearer.Code, recBearer.Body.String())
	}

	// 5. 测试登录失败限流：同一用户连续失败 5 次后锁定 15 分钟返回 429
	bruteUser := "brute_" + ulid.New()
	_, _ = authSvc.CreateUser(ctx, bruteUser, "RightPass123!", false)

	badLoginBody, _ := json.Marshal(map[string]string{
		"username": bruteUser,
		"password": "WrongPassword!",
	})

	for i := 1; i <= 5; i++ {
		reqBad := httptest.NewRequest("POST", "/api/v1/login", bytes.NewReader(badLoginBody))
		reqBad.Header.Set("Content-Type", "application/json")
		reqBad.RemoteAddr = "192.0.2.100:1234"
		recBad := httptest.NewRecorder()
		a.Mux.ServeHTTP(recBad, reqBad)
		if recBad.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401 bad credentials, got %d", i, recBad.Code)
		}
	}

	// 第 6 次应当被锁定并返回 429
	reqLocked := httptest.NewRequest("POST", "/api/v1/login", bytes.NewReader(badLoginBody))
	reqLocked.Header.Set("Content-Type", "application/json")
	reqLocked.RemoteAddr = "192.0.2.100:1234"
	recLocked := httptest.NewRecorder()
	a.Mux.ServeHTTP(recLocked, reqLocked)
	if recLocked.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt 6: expected 429 rate limited, got %d (body: %s)",
			recLocked.Code, recLocked.Body.String())
	}
}
