package app

import (
	"net/http"

	"dash/internal/config"
	"dash/internal/db"
)

// App 包含 dashd 运行时的核心组件（配置、数据库、路由、后台任务等）。
// 供各模块在 Register 时进行依赖注入和路由装配。
type App struct {
	Mux    *http.ServeMux
	DB      *db.DB
	Config  *config.Config
	Version string
	// 后续任务按需扩充字段
}

// Module 定义服务端子模块的注册契约。
type Module interface {
	Name() string
	Register(*App) error // 注册路由、后台任务、依赖注入
}
