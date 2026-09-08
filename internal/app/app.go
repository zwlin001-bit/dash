package app

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"dash/internal/config"
	"dash/internal/db"
	"dash/internal/logx"
)

// RouteInfo 记录注册在 App 上的路由及鉴权属性。
type RouteInfo struct {
	Pattern string // 路由匹配表达式，如 "GET /api/v1/nodes"
	Authed  bool   // 是否需要认证
}

// AuthMiddlewareFunc 定义认证中间件函数类型。
type AuthMiddlewareFunc func(http.HandlerFunc) http.HandlerFunc

// App 包含 dashd 运行时的核心组件（配置、数据库、路由、后台任务等）。
// 供各模块在 Register 时进行依赖注入和路由装配。
type App struct {
	Mux             *http.ServeMux
	DB              *db.DB
	Config          *config.Config
	Registry        any    // 供 control / settings 等模块共享的长连接注册表
	Ingester        any    // 供 control / ingest 共享的指标落库器
	JobEngine       any    // 供各业务模块调用的通用 Job 引擎 (P2-03)
	CloudService    any    // 供各业务模块调用的云资产服务 (P2-02)
	GuardEngine     any    // 供各业务模块调用的 ECS 保活与流量守卫引擎 (P2-04)
	Version         string // 服务端当前运行版本
	DistFingerprint string // 前端内嵌静态产物内容指纹 (P1-27)
	HealthCheckPing func(ctx context.Context) error // 可选探活重载 (测试与 Fixture 生成时跳过真库 Ping)

	mu             sync.RWMutex
	routes         []RouteInfo
	authMiddleware AuthMiddlewareFunc
	// 后续任务按需扩充字段
}

// Module 定义服务端子模块的注册契约。
type Module interface {
	Name() string
	Register(*App) error // 注册路由、后台任务、依赖注入
}

// NewApp 创建并初始化 App 运行时实例。
func NewApp(database *db.DB, cfg *config.Config, ver string) *App {
	return &App{
		Mux:     http.NewServeMux(),
		DB:      database,
		Config:  cfg,
		Version: ver,
	}
}

// SetAuthMiddleware 注入全局认证中间件（通常由 auth 模块在注册时注入）。
func (a *App) SetAuthMiddleware(mw AuthMiddlewareFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authMiddleware = mw
}

func (a *App) getAuthMiddleware() AuthMiddlewareFunc {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.authMiddleware
}

// Routes 返回所有注册在 App 上的路由信息快照。
func (a *App) Routes() []RouteInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	res := make([]RouteInfo, len(a.routes))
	copy(res, a.routes)
	return res
}

// HandleAuthed 注册需要鉴权的 HTTP API 路由（控制台 API 默认使用该方法）。
// 未携带合法会话或令牌的请求一律返回 401 统一错误体。
func (a *App) HandleAuthed(pattern string, h http.HandlerFunc) {
	a.mu.Lock()
	a.routes = append(a.routes, RouteInfo{Pattern: pattern, Authed: true})
	a.mu.Unlock()

	if a.Mux == nil {
		a.Mux = http.NewServeMux()
	}

	a.Mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		mw := a.getAuthMiddleware()
		if mw != nil {
			mw(h)(w, r)
			return
		}
		if a.Config != nil && a.Config.Server.DevNoAuth {
			logx.Warn("DEV_NO_AUTH request allowed", "method", r.Method, "path", r.URL.Path, "dev_no_auth", true)
			h(w, r)
			return
		}
		// 默认拒绝：若认证中间件未就绪，直接返回 401
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "unauthorized",
				"message": "请先登录",
				"detail":  nil,
			},
		})
	})
}

// HandlePublic 注册免鉴权的 HTTP 路由（仅限白名单端点使用，调用时须注释说明理由）。
func (a *App) HandlePublic(pattern string, h http.HandlerFunc) {
	a.mu.Lock()
	a.routes = append(a.routes, RouteInfo{Pattern: pattern, Authed: false})
	a.mu.Unlock()

	if a.Mux == nil {
		a.Mux = http.NewServeMux()
	}

	a.Mux.HandleFunc(pattern, h)
}

// HandlePublicHandler 注册免鉴权的 http.Handler 路由。
func (a *App) HandlePublicHandler(pattern string, h http.Handler) {
	a.HandlePublic(pattern, h.ServeHTTP)
}

// HandleAuthedHandler 注册需要鉴权的 http.Handler 路由。
func (a *App) HandleAuthedHandler(pattern string, h http.Handler) {
	a.HandleAuthed(pattern, h.ServeHTTP)
}

// FlushIngest 触发指标落库缓冲区刷写与优雅关闭。
func (a *App) FlushIngest() {
	if a.Ingester != nil {
		if stopper, ok := a.Ingester.(interface{ Stop() }); ok {
			stopper.Stop()
		}
	}
}

// Close 释放 App 关联的资源：先刷写 ingest，再停止长连接与后台任务，最后关闭数据库连接池。
func (a *App) Close() error {
	a.FlushIngest()

	if reg, ok := a.Registry.(interface{ Stop() }); ok && reg != nil {
		reg.Stop()
	}

	if ge, ok := a.GuardEngine.(interface{ Stop() }); ok && ge != nil {
		ge.Stop()
	}

	if je, ok := a.JobEngine.(interface{ Stop() }); ok && je != nil {
		je.Stop()
	}

	if a.DB != nil {
		return a.DB.Close()
	}
	return nil
}

// HealthzHandler 构造统一的健康检查 HTTP Handler (依据 12-api-spec.md §9)。
// 数据库不可用时返回 503 但进程不退出，恢复后自动变回 200。
func (a *App) HealthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		dbStatus := "ok"
		httpStatus := http.StatusOK
		status := "ok"

		if a.HealthCheckPing != nil {
			if err := a.HealthCheckPing(r.Context()); err != nil {
				dbStatus = "error"
				status = "error"
				httpStatus = http.StatusServiceUnavailable
			}
		} else if a.DB == nil {
			dbStatus = "error"
			status = "error"
			httpStatus = http.StatusServiceUnavailable
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := a.DB.Ping(ctx); err != nil {
				dbStatus = "error"
				status = "error"
				httpStatus = http.StatusServiceUnavailable
			}
		}

		agentsOnline := 0
		if reg, ok := a.Registry.(interface{ OnlineCount() int }); ok && reg != nil {
			agentsOnline = reg.OnlineCount()
		}

		var droppedBatches, droppedRows uint64
		if ing, ok := a.Ingester.(interface {
			DroppedBatches() uint64
			DroppedRows() uint64
		}); ok && ing != nil {
			droppedBatches = ing.DroppedBatches()
			droppedRows = ing.DroppedRows()
		}

		resp := map[string]any{
			"status":           status,
			"version":          a.Version,
			"dist_fingerprint": a.DistFingerprint,
			"db":               dbStatus,
			"agents_online":    agentsOnline,
			"dropped_batches":  droppedBatches,
			"dropped_rows":     droppedRows,
		}
		if a.Config != nil && a.Config.Server.DevNoAuth {
			resp["dev_no_auth"] = true
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(httpStatus)
		_ = json.NewEncoder(w).Encode(resp)
	}
}
