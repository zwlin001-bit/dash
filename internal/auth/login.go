package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"dash/internal/app"
	"dash/internal/audit"
	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/ulid"
)

const (
	SessionCookieName = "dash_session"
	SessionDuration   = 7 * 24 * time.Hour
	LockoutDuration   = 15 * time.Minute
	MaxFailedAttempts = 5
)

type contextKey string

const userCtxKey contextKey = "dash_auth_user"

// User 表示系统账号信息。
type User struct {
	ID            string  `json:"id"`
	Username      string  `json:"username"`
	PasswdHash    string  `json:"-"`
	IsAdmin       bool    `json:"is_admin"`
	TotpSecret    *string `json:"-"`
	LastLoginAtMs *int64  `json:"last_login_at_ms,omitempty"`
	CreatedAtMs   int64   `json:"created_at_ms"`
	UpdatedAtMs   int64   `json:"updated_at_ms"`
}

// UserFromContext 从上下文中获取当前登录用户。
func UserFromContext(ctx context.Context) *User {
	if u, ok := ctx.Value(userCtxKey).(*User); ok {
		return u
	}
	return nil
}

// ContextWithUser 将用户注入上下文。
func ContextWithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

// RateLimiter 维护失败登录计数与锁定。
type RateLimiter struct {
	mu       sync.Mutex
	failures map[string]*failureRecord
}

type failureRecord struct {
	count       int
	firstFailed time.Time
	lockedUntil time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		failures: make(map[string]*failureRecord),
	}
}

func (rl *RateLimiter) Check(keys ...string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for _, key := range keys {
		if key == "" {
			continue
		}
		rec, ok := rl.failures[key]
		if !ok {
			continue
		}
		if now.Before(rec.lockedUntil) {
			return false // Locked
		}
		// Reset window if past lockout duration
		if now.Sub(rec.firstFailed) > LockoutDuration {
			delete(rl.failures, key)
		}
	}
	return true
}

func (rl *RateLimiter) RecordFailure(keys ...string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	maxCount := 1
	for _, key := range keys {
		if key == "" {
			continue
		}
		rec, ok := rl.failures[key]
		if !ok || now.Sub(rec.firstFailed) > LockoutDuration {
			rec = &failureRecord{
				count:       1,
				firstFailed: now,
			}
			rl.failures[key] = rec
		} else {
			rec.count++
		}

		if rec.count > maxCount {
			maxCount = rec.count
		}

		if rec.count >= MaxFailedAttempts {
			rec.lockedUntil = now.Add(LockoutDuration)
		}
	}
	return maxCount
}

func (rl *RateLimiter) FailCount(key string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rec, ok := rl.failures[key]; ok {
		return rec.count
	}
	return 1
}

func (rl *RateLimiter) Reset(keys ...string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for _, key := range keys {
		delete(rl.failures, key)
	}
}

// Service 提供认证相关的业务逻辑。
type Service struct {
	db      *db.DB
	limiter *RateLimiter
}

func NewService(database *db.DB) *Service {
	return &Service{
		db:      database,
		limiter: NewRateLimiter(),
	}
}

// HashPassword 使用 bcrypt 哈希密码。
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// CheckPassword 校验明文密码与 bcrypt 哈希。
func CheckPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// CreateUser 在数据库中新建用户。
func (s *Service) CreateUser(ctx context.Context, username, password string, isAdmin bool) (*User, error) {
	if strings.TrimSpace(username) == "" {
		return nil, errors.New("username cannot be empty")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	id := ulid.New()
	now := time.Now().UnixMilli()
	adminVal := 0
	if isAdmin {
		adminVal = 1
	}

	q := `INSERT INTO account_users (id, username, passwd_hash, is_admin, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?)`

	_, err = s.db.Exec(ctx, q, id, username, hash, adminVal, now, now)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	return &User{
		ID:          id,
		Username:    username,
		PasswdHash:  hash,
		IsAdmin:     isAdmin,
		CreatedAtMs: now,
		UpdatedAtMs: now,
	}, nil
}

// GetUserByUsername 根据用户名查找用户。
func (s *Service) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	q := `SELECT id, username, passwd_hash, is_admin, totp_secret, last_login_at_ms, created_at_ms, updated_at_ms
FROM account_users WHERE username = ?`

	var u User
	var isAdminInt int
	var totp sql.NullString
	var lastLogin sql.NullInt64

	row := s.db.QueryRow(ctx, q, username)
	err := row.Scan(&u.ID, &u.Username, &u.PasswdHash, &isAdminInt, &totp, &lastLogin, &u.CreatedAtMs, &u.UpdatedAtMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}

	u.IsAdmin = (isAdminInt == 1)
	if totp.Valid {
		u.TotpSecret = &totp.String
	}
	if lastLogin.Valid {
		u.LastLoginAtMs = &lastLogin.Int64
	}
	return &u, nil
}

// Login 执行账号密码校验、限流检查、创建会话与写入审计。
func (s *Service) Login(ctx context.Context, username, password, userAgent, ip string) (*User, string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, "", errors.New("empty_credentials")
	}

	ipKey := "ip:" + ip
	userKey := "user:" + username

	if !s.limiter.Check(ipKey, userKey) {
		failCount := s.limiter.FailCount(userKey)
		events.Emit(ctx, events.Event{
			Type:       "auth.login_failed",
			Source:     "auth",
			TargetKind: "user",
			TargetID:   username,
			Title:      fmt.Sprintf("用户 %s 登录失败 (已锁定)", username),
			DedupKey:   fmt.Sprintf("auth.login_failed:%s", username),
			Payload: map[string]any{
				"username":   username,
				"ip":         ip,
				"fail_count": failCount,
			},
		})
		return nil, "", errors.New("rate_limited")
	}

	user, err := s.GetUserByUsername(ctx, username)
	if err != nil || !CheckPassword(password, user.PasswdHash) {
		failCount := s.limiter.RecordFailure(ipKey, userKey)
		_ = audit.Log(ctx, s.db, audit.Entry{
			ActorKind:  "user",
			Action:     "auth.login",
			TargetKind: "user",
			Detail:     fmt.Sprintf(`{"username":%q,"reason":"bad_credentials"}`, username),
			Result:     "failed",
			IP:         ip,
		})
		events.Emit(ctx, events.Event{
			Type:       "auth.login_failed",
			Source:     "auth",
			TargetKind: "user",
			TargetID:   username,
			Title:      fmt.Sprintf("用户 %s 登录失败", username),
			DedupKey:   fmt.Sprintf("auth.login_failed:%s", username),
			Payload: map[string]any{
				"username":   username,
				"ip":         ip,
				"fail_count": failCount,
			},
		})
		return nil, "", errors.New("bad_credentials")
	}

	// 登录成功，重置失败计数
	s.limiter.Reset(ipKey, userKey)

	// 生成会话令牌 (32 字节安全随机数)
	plainToken, tokenHash, err := generateSessionToken()
	if err != nil {
		return nil, "", fmt.Errorf("generate session token: %w", err)
	}

	now := time.Now().UnixMilli()
	expiresAt := now + SessionDuration.Milliseconds()
	sessionID := ulid.New()

	var uaVal, ipVal any
	if userAgent != "" {
		uaVal = userAgent
	}
	if ip != "" {
		ipVal = ip
	}

	qSession := `INSERT INTO user_sessions (id, user_id, token_hash, user_agent, ip, expires_at_ms, created_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.Exec(ctx, qSession, sessionID, user.ID, tokenHash, uaVal, ipVal, expiresAt, now)
	if err != nil {
		return nil, "", fmt.Errorf("save session: %w", err)
	}

	// 更新最后登录时间
	qUpdateUser := `UPDATE account_users SET last_login_at_ms = ?, updated_at_ms = ? WHERE id = ?`
	_, _ = s.db.Exec(ctx, qUpdateUser, now, now, user.ID)
	user.LastLoginAtMs = &now

	// 记录审计日志
	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  "user",
		ActorID:    user.ID,
		Action:     "auth.login",
		TargetKind: "user",
		TargetID:   user.ID,
		Detail:     fmt.Sprintf(`{"username":%q}`, user.Username),
		Result:     "ok",
		IP:         ip,
	})

	return user, plainToken, nil
}

// Logout 作废会话并写审计日志。
func (s *Service) Logout(ctx context.Context, token string, user *User, ip string) error {
	if token == "" {
		return nil
	}
	tokenHash := hashToken(token)
	q := `DELETE FROM user_sessions WHERE token_hash = ?`
	_, _ = s.db.Exec(ctx, q, tokenHash)

	actorID := ""
	if user != nil {
		actorID = user.ID
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  "user",
		ActorID:    actorID,
		Action:     "auth.logout",
		TargetKind: "user",
		TargetID:   actorID,
		Result:     "ok",
		IP:         ip,
	})
	return nil
}

// AuthenticateToken 根据令牌哈希验证会话并返回用户。
func (s *Service) AuthenticateToken(ctx context.Context, token string) (*User, error) {
	if strings.TrimSpace(token) == "" {
		return nil, db.ErrNotFound
	}
	tokenHash := hashToken(token)
	now := time.Now().UnixMilli()

	q := `SELECT s.expires_at_ms, u.id, u.username, u.passwd_hash, u.is_admin, u.totp_secret, u.last_login_at_ms, u.created_at_ms, u.updated_at_ms
FROM user_sessions s
JOIN account_users u ON s.user_id = u.id
WHERE s.token_hash = ?`

	var expiresAt int64
	var u User
	var isAdminInt int
	var totp sql.NullString
	var lastLogin sql.NullInt64

	row := s.db.QueryRow(ctx, q, tokenHash)
	err := row.Scan(&expiresAt, &u.ID, &u.Username, &u.PasswdHash, &isAdminInt, &totp, &lastLogin, &u.CreatedAtMs, &u.UpdatedAtMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}

	// 检查会话是否已过期
	if expiresAt <= now {
		// 清理过期会话
		_, _ = s.db.Exec(ctx, `DELETE FROM user_sessions WHERE token_hash = ?`, tokenHash)
		return nil, errors.New("session_expired")
	}

	u.IsAdmin = (isAdminInt == 1)
	if totp.Valid {
		u.TotpSecret = &totp.String
	}
	if lastLogin.Valid {
		u.LastLoginAtMs = &lastLogin.Int64
	}
	return &u, nil
}

// ChangePassword 修改用户密码，验证旧密码并更新为新密码哈希。
func (s *Service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword, ip string) error {
	if strings.TrimSpace(oldPassword) == "" || strings.TrimSpace(newPassword) == "" {
		return errors.New("empty_password")
	}
	if len(newPassword) < 6 {
		return errors.New("password_too_short")
	}

	var passwdHash string
	err := s.db.QueryRow(ctx, `SELECT passwd_hash FROM account_users WHERE id = ?`, userID).Scan(&passwdHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.ErrNotFound
		}
		return err
	}

	if !CheckPassword(oldPassword, passwdHash) {
		return errors.New("bad_old_password")
	}

	newHash, err := HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	now := time.Now().UnixMilli()
	_, err = s.db.Exec(ctx, `UPDATE account_users SET passwd_hash = ?, updated_at_ms = ? WHERE id = ?`, newHash, now, userID)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  "user",
		ActorID:    userID,
		Action:     "auth.change_password",
		TargetKind: "user",
		TargetID:   userID,
		Result:     "ok",
		IP:         ip,
	})

	return nil
}

func generateSessionToken() (plainToken string, tokenHash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plainToken = hex.EncodeToString(b)
	tokenHash = hashToken(plainToken)
	return plainToken, tokenHash, nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// ClientIP 提取客户端真实 IP 地址。
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

// ExtractSessionToken 从 Cookie 或 Authorization 头中提取会话令牌。
func ExtractSessionToken(r *http.Request) string {
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	authHdr := r.Header.Get("Authorization")
	if strings.HasPrefix(authHdr, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authHdr, "Bearer "))
	}
	return ""
}

// JSONError 返回标准错误响应：{"error":{"code":"...","message":"...","detail":null}}
func JSONError(w http.ResponseWriter, status int, code, message string, detail any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"detail":  detail,
		},
	})
}

// JSONSuccess 返回标准 JSON 响应。
func JSONSuccess(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

// AuthModule 实现 app.Module，装配认证相关的 HTTP 路由与中间件。
type AuthModule struct {
	service *Service
}

func NewModule() app.Module {
	return &AuthModule{}
}

func (m *AuthModule) Name() string {
	return "auth"
}

func (m *AuthModule) Register(a *app.App) error {
	if a.Mux == nil {
		a.Mux = http.NewServeMux()
	}
	if a.DB != nil {
		m.service = NewService(a.DB)
	}
	m.RegisterRoutes(a.Mux)
	return nil
}

func (m *AuthModule) Service() *Service {
	return m.service
}

func (m *AuthModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/login", m.HandleLogin)
	mux.HandleFunc("POST /api/v1/logout", m.HandleLogout)
	mux.HandleFunc("GET /api/v1/me", m.HandleMe)
	mux.HandleFunc("POST /api/v1/me/password", m.HandleChangePassword)
}

// AuthMiddleware 负责解析会话令牌并将 User 注入上下文。
func (m *AuthModule) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ExtractSessionToken(r)
		if token != "" && m.service != nil {
			user, err := m.service.AuthenticateToken(r.Context(), token)
			if err == nil && user != nil {
				r = r.WithContext(ContextWithUser(r.Context(), user))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth 中间件保证只有已登录用户才能访问。
func (m *AuthModule) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil {
			// Try parsing token directly if middleware was not wrapped
			token := ExtractSessionToken(r)
			if token != "" && m.service != nil {
				var err error
				u, err = m.service.AuthenticateToken(r.Context(), token)
				if err == nil && u != nil {
					r = r.WithContext(ContextWithUser(r.Context(), u))
				}
			}
		}

		if u == nil {
			JSONError(w, http.StatusUnauthorized, "unauthorized", "请先登录", nil)
			return
		}
		next(w, r)
	}
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (m *AuthModule) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if m.service == nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "服务未就绪", nil)
		return
	}

	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}

	ip := ClientIP(r)
	ua := r.UserAgent()

	user, token, err := m.service.Login(r.Context(), req.Username, req.Password, ua, ip)
	if err != nil {
		if err.Error() == "rate_limited" {
			JSONError(w, http.StatusTooManyRequests, "rate_limited", "登录失败次数过多，已被锁定 15 分钟", nil)
			return
		}
		JSONError(w, http.StatusUnauthorized, "bad_credentials", "用户名或密码错误", nil)
		return
	}

	// 下发会话 Cookie (HttpOnly + SameSite=Lax + Secure)
	cookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(SessionDuration.Seconds()),
	}
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)

	JSONSuccess(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"is_admin": user.IsAdmin,
		},
	})
}

func (m *AuthModule) HandleLogout(w http.ResponseWriter, r *http.Request) {
	token := ExtractSessionToken(r)
	user := UserFromContext(r.Context())
	ip := ClientIP(r)

	if m.service != nil && token != "" {
		_ = m.service.Logout(r.Context(), token, user, ip)
	}

	// 清除 Cookie
	cookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
	http.SetCookie(w, cookie)

	JSONSuccess(w, http.StatusOK, map[string]any{"ok": true})
}

func (m *AuthModule) HandleMe(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	if u == nil {
		token := ExtractSessionToken(r)
		if token != "" && m.service != nil {
			u, _ = m.service.AuthenticateToken(r.Context(), token)
		}
	}

	if u == nil {
		JSONError(w, http.StatusUnauthorized, "unauthorized", "未登录或会话已过期", nil)
		return
	}

	JSONSuccess(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":       u.ID,
			"username": u.Username,
			"is_admin": u.IsAdmin,
		},
	})
}

type changePasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (m *AuthModule) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	if u == nil {
		token := ExtractSessionToken(r)
		if token != "" && m.service != nil {
			u, _ = m.service.AuthenticateToken(r.Context(), token)
		}
	}
	if u == nil {
		JSONError(w, http.StatusUnauthorized, "unauthorized", "未登录或会话已过期", nil)
		return
	}

	var req changePasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}

	ip := ClientIP(r)
	err := m.service.ChangePassword(r.Context(), u.ID, req.OldPassword, req.NewPassword, ip)
	if err != nil {
		if err.Error() == "bad_old_password" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "旧密码错误", "old_password")
			return
		}
		if err.Error() == "password_too_short" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "新密码长度不能少于 6 位", "new_password")
			return
		}
		if err.Error() == "empty_password" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "密码不能为空", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "修改密码失败", nil)
		return
	}

	JSONSuccess(w, http.StatusOK, map[string]any{"ok": true})
}

