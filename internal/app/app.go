package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"dash/internal/config"
	"dash/internal/db"
)

// App 包含 dashd 运行时的核心组件（配置、数据库、路由、后台任务等）。
// 供各模块在 Register 时进行依赖注入和路由装配。
type App struct {
	Mux      *http.ServeMux
	DB       *db.DB
	Config   *config.Config
	Registry any    // 供 control / settings 等模块共享的长连接注册表
	Ingester any    // 供 control / ingest 共享的指标落库器
	Version  string // 服务端当前运行版本
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

		if a.DB == nil {
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
			"status":          status,
			"version":         a.Version,
			"db":              dbStatus,
			"agents_online":   agentsOnline,
			"dropped_batches": droppedBatches,
			"dropped_rows":    droppedRows,
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(httpStatus)
		_ = json.NewEncoder(w).Encode(resp)
	}
}
