package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/ulid"
)

func TestPasswordHashing(t *testing.T) {
	pwd := "MySecretPassword123!"
	hash, err := HashPassword(pwd)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}

	if hash == pwd {
		t.Fatal("hash should not equal plain password")
	}

	if !CheckPassword(pwd, hash) {
		t.Fatal("password verification failed for correct password")
	}

	if CheckPassword("WrongPassword", hash) {
		t.Fatal("password verification succeeded for wrong password")
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter()
	userKey := "user:testuser"
	ipKey := "ip:192.168.1.100"

	// Initial check should pass
	if !rl.Check(userKey, ipKey) {
		t.Fatal("expected initial check to pass")
	}

	// 4 failures should still pass
	for i := 0; i < 4; i++ {
		rl.RecordFailure(userKey, ipKey)
		if !rl.Check(userKey, ipKey) {
			t.Fatalf("expected check to pass after %d failures", i+1)
		}
	}

	// 5th failure triggers lockout
	rl.RecordFailure(userKey, ipKey)
	if rl.Check(userKey, ipKey) {
		t.Fatal("expected check to fail after 5 failures")
	}

	// Another user on same IP should also be locked
	if rl.Check("user:other", ipKey) {
		t.Fatal("expected same IP to be locked")
	}

	// Reset for user & IP
	rl.Reset(userKey, ipKey)
	if !rl.Check(userKey, ipKey) {
		t.Fatal("expected check to pass after reset")
	}
}

func TestSessionTokenGeneration(t *testing.T) {
	token1, hash1, err := generateSessionToken()
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	if len(token1) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(token1))
	}
	if len(hash1) != 64 {
		t.Fatalf("expected 64 hex chars for hash, got %d", len(hash1))
	}

	token2, hash2, _ := generateSessionToken()
	if token1 == token2 || hash1 == hash2 {
		t.Fatal("generated tokens must be unique")
	}

	// Verifying hash function
	if hashToken(token1) != hash1 {
		t.Fatal("hash mismatch")
	}
}

func TestExtractSessionToken(t *testing.T) {
	// From Cookie
	req, _ := http.NewRequest("GET", "/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "cookie_token_123"})
	if token := ExtractSessionToken(req); token != "cookie_token_123" {
		t.Fatalf("expected cookie_token_123, got %s", token)
	}

	// From Bearer header
	req2, _ := http.NewRequest("GET", "/api/v1/me", nil)
	req2.Header.Set("Authorization", "Bearer header_token_456")
	if token := ExtractSessionToken(req2); token != "header_token_456" {
		t.Fatalf("expected header_token_456, got %s", token)
	}
}

func TestClientIP(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	if ip := ClientIP(req); ip != "203.0.113.195" {
		t.Fatalf("expected 203.0.113.195, got %s", ip)
	}

	req2, _ := http.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Real-IP", "198.51.100.1")
	if ip := ClientIP(req2); ip != "198.51.100.1" {
		t.Fatalf("expected 198.51.100.1, got %s", ip)
	}

	req3, _ := http.NewRequest("GET", "/", nil)
	req3.RemoteAddr = "192.0.2.1:12345"
	if ip := ClientIP(req3); ip != "192.0.2.1" {
		t.Fatalf("expected 192.0.2.1, got %s", ip)
	}
}

func TestUserContext(t *testing.T) {
	ctx := context.Background()
	if u := UserFromContext(ctx); u != nil {
		t.Fatal("expected nil user from empty context")
	}

	testUser := &User{ID: "01TEST", Username: "admin", IsAdmin: true}
	ctx = ContextWithUser(ctx, testUser)
	u := UserFromContext(ctx)
	if u == nil || u.Username != "admin" || !u.IsAdmin {
		t.Fatalf("unexpected user from context: %+v", u)
	}
}

func TestRequireAuthMiddleware(t *testing.T) {
	mod := &AuthModule{}

	called := false
	handler := mod.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Without auth
	req, _ := http.NewRequest("GET", "/api/v1/nodes", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("inner handler should not have been called")
	}

	// With user context
	reqWithUser := req.WithContext(ContextWithUser(req.Context(), &User{ID: "01USER"}))
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, reqWithUser)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec2.Code)
	}
	if !called {
		t.Fatal("inner handler should have been called")
	}
}

func getTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("Skipping DB test: cannot connect to test MySQL: %v", err)
	}
	return d
}

func TestAuthFullFlowWithDB(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	ctx := context.Background()
	svc := NewService(d)

	testUsername := "test_auth_user_" + ulid.New()[:8]
	testPassword := "ValidPass123!"

	// 1. Create User
	u, err := svc.CreateUser(ctx, testUsername, testPassword, true)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if u.Username != testUsername || !u.IsAdmin {
		t.Fatalf("unexpected user: %+v", u)
	}

	// 2. Failed login (wrong password)
	_, _, err = svc.Login(ctx, testUsername, "WrongPass", "test-agent", "127.0.0.1")
	if err == nil || err.Error() != "bad_credentials" {
		t.Fatalf("expected bad_credentials error, got: %v", err)
	}

	// Verify failed login logged in audit_log
	var auditCount int
	err = d.QueryRow(ctx, "SELECT count(1) FROM audit_log WHERE action = 'auth.login' AND result = 'failed'").Scan(&auditCount)
	if err != nil || auditCount == 0 {
		t.Fatalf("expected audit_log for failed login, count=%d, err=%v", auditCount, err)
	}

	// 3. Successful login
	loggedInUser, token, err := svc.Login(ctx, testUsername, testPassword, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if loggedInUser.ID != u.ID {
		t.Fatalf("expected user ID %s, got %s", u.ID, loggedInUser.ID)
	}
	if token == "" {
		t.Fatal("expected non-empty session token")
	}

	// Verify successful login in audit_log
	var okAuditCount int
	err = d.QueryRow(ctx, "SELECT count(1) FROM audit_log WHERE action = 'auth.login' AND result = 'ok' AND target_id = ?", u.ID).Scan(&okAuditCount)
	if err != nil || okAuditCount == 0 {
		t.Fatalf("expected audit_log for ok login, count=%d, err=%v", okAuditCount, err)
	}

	// 4. AuthenticateToken with valid token
	authUser, err := svc.AuthenticateToken(ctx, token)
	if err != nil {
		t.Fatalf("authenticate token failed: %v", err)
	}
	if authUser.ID != u.ID {
		t.Fatalf("expected user ID %s, got %s", u.ID, authUser.ID)
	}

	// 5. Test session expiration
	// Manually set session expires_at_ms to the past
	tokenHash := hashToken(token)
	pastMs := time.Now().UnixMilli() - 1000
	_, err = d.Exec(ctx, "UPDATE user_sessions SET expires_at_ms = ? WHERE token_hash = ?", pastMs, tokenHash)
	if err != nil {
		t.Fatalf("failed to expire session: %v", err)
	}

	// Authenticate should now report session_expired
	_, err = svc.AuthenticateToken(ctx, token)
	if err == nil || err.Error() != "session_expired" {
		t.Fatalf("expected session_expired, got: %v", err)
	}

	// 6. Test Logout
	// Login again to get new session
	_, newToken, err := svc.Login(ctx, testUsername, testPassword, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("re-login failed: %v", err)
	}

	err = svc.Logout(ctx, newToken, u, "127.0.0.1")
	if err != nil {
		t.Fatalf("logout failed: %v", err)
	}

	// Session should be deleted
	_, err = svc.AuthenticateToken(ctx, newToken)
	if err == nil {
		t.Fatal("expected error authenticating logged out token")
	}

	// Verify logout audit log
	var logoutAuditCount int
	err = d.QueryRow(ctx, "SELECT count(1) FROM audit_log WHERE action = 'auth.logout' AND result = 'ok' AND actor_id = ?", u.ID).Scan(&logoutAuditCount)
	if err != nil || logoutAuditCount == 0 {
		t.Fatalf("expected audit_log for logout, count=%d, err=%v", logoutAuditCount, err)
	}
}

func TestChangePassword(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	svc := NewService(d)
	ctx := context.Background()

	username := "pwduser_" + ulid.New()[:8]
	oldPwd := "InitialPassword123"
	newPwd := "UpdatedSecret456"

	user, err := svc.CreateUser(ctx, username, oldPwd, false)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// 1. Wrong old password
	err = svc.ChangePassword(ctx, user.ID, "WrongPassword", newPwd, "127.0.0.1")
	if err == nil || err.Error() != "bad_old_password" {
		t.Fatalf("expected bad_old_password, got: %v", err)
	}

	// 2. Short new password
	err = svc.ChangePassword(ctx, user.ID, oldPwd, "123", "127.0.0.1")
	if err == nil || err.Error() != "password_too_short" {
		t.Fatalf("expected password_too_short, got: %v", err)
	}

	// 3. Successful change
	err = svc.ChangePassword(ctx, user.ID, oldPwd, newPwd, "127.0.0.1")
	if err != nil {
		t.Fatalf("ChangePassword failed: %v", err)
	}

	// 4. Verify login with new password succeeds and old password fails
	_, _, err = svc.Login(ctx, username, oldPwd, "test-agent", "127.0.0.1")
	if err == nil {
		t.Fatal("expected login with old password to fail")
	}

	_, _, err = svc.Login(ctx, username, newPwd, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("login with new password failed: %v", err)
	}
}

func TestLoginFailedEvent(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	store := events.NewStore(database, 50)
	store.Start()
	defer store.Stop()
	oldStore := events.GetDefaultStore()
	defer events.SetDefaultStore(oldStore)
	events.SetDefaultStore(store)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_ = store.SyncBuiltinTypes(ctx)

	svc := NewService(database)
	username := "failed_user_" + ulid.New()[:8]
	_, _ = svc.CreateUser(ctx, username, "CorrectPass123!", true)

	// Attempt login with bad password
	_, _, err := svc.Login(ctx, username, "WrongPass!", "test-agent", "192.168.1.50")
	if err == nil {
		t.Fatal("expected login to fail")
	}

	// Verify auth.login_failed event was emitted
	deadline := time.Now().Add(3 * time.Second)
	var records []events.EventRecord
	found := false
	for time.Now().Before(deadline) {
		records, _, err = store.ListEvents(ctx, events.Filter{EventType: "auth.login_failed"})
		if err == nil {
			for _, r := range records {
				if r.Payload["username"] == username {
					found = true
					if r.Type != "auth.login_failed" || r.SourceModule != "auth" {
						t.Fatalf("unexpected event: %+v", r)
					}
					if r.Payload["ip"] != "192.168.1.50" {
						t.Fatalf("unexpected payload: %+v", r.Payload)
					}
					break
				}
			}
			if found {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatalf("auth.login_failed event for user %s not found in %v", username, records)
	}
}

func TestDevNoAuth_RequireAuthAndMe(t *testing.T) {
	// 1. 常规模式 (DevNoAuth = false)
	svcNormal := &Service{DevNoAuth: false}
	handlerCalled := false
	protectedHandler := RequireAuth(svcNormal)(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	rec := httptest.NewRecorder()
	protectedHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without DevNoAuth, got %d", rec.Code)
	}
	if handlerCalled {
		t.Fatalf("protected handler should NOT be called when unauthorized")
	}

	// 2. 开启 DevNoAuth = true
	svcDev := &Service{DevNoAuth: true}
	var capturedUser *User
	protectedHandlerDev := RequireAuth(svcDev)(func(w http.ResponseWriter, r *http.Request) {
		capturedUser = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	reqDev := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	recDev := httptest.NewRecorder()
	protectedHandlerDev(recDev, reqDev)

	if recDev.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with DevNoAuth=true, got %d", recDev.Code)
	}
	if capturedUser == nil {
		t.Fatalf("expected user in context when DevNoAuth=true, got nil")
	}
	if !capturedUser.IsAdmin {
		t.Fatalf("expected DevNoAuth user to be admin, got false")
	}

	// 3. HandleMe 在 DevNoAuth 下返回 200 与管理员信息
	authMod := &AuthModule{service: svcDev}
	reqMe := httptest.NewRequest("GET", "/api/v1/me", nil)
	recMe := httptest.NewRecorder()
	authMod.HandleMe(recMe, reqMe)

	if recMe.Code != http.StatusOK {
		t.Fatalf("expected HandleMe to return 200 under DevNoAuth, got %d", recMe.Code)
	}
	var meResp struct {
		User struct {
			Username string `json:"username"`
			IsAdmin  bool   `json:"is_admin"`
		} `json:"user"`
	}
	if err := json.NewDecoder(recMe.Body).Decode(&meResp); err != nil {
		t.Fatalf("failed to decode HandleMe response: %v", err)
	}
	if !meResp.User.IsAdmin {
		t.Fatalf("expected is_admin=true in HandleMe response, got false")
	}
}



