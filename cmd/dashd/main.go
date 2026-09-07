package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"dash/internal/api"
	"dash/internal/api/metrics"
	"dash/internal/app"
	"dash/internal/auth"
	"dash/internal/config"
	"dash/internal/control"
	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/ingest"
	"dash/internal/inventory"
	"dash/internal/logx"
	"dash/internal/migrate"
	"dash/internal/settings"
)

var (
	version   = "dev"
	gitCommit = "none"
	buildTime = "unknown"
)

// App 包含 dashd 运行时的核心组件（配置、数据库、路由、后台任务等）。
// 供各模块在 Register 时进行依赖注入和路由装配。
type App = app.App

// Module 定义服务端子模块的注册契约。
type Module = app.Module

// modules 为所有需要装配进 dashd 的模块列表。
// 约束：后续任务只允许向此列表追加模块，不改动装配与启动逻辑。
var modules = []Module{
	auth.NewModule(),
	control.NewModule(),
	inventory.NewModule(),
	ingest.NewModule(),
	control.NewModule(),
	metrics.NewModule(),
	events.NewModule(),
	settings.NewModule(),
	api.NewModule(), // ★ 必须最后：它挂 "/" 作为 SPA 兜底路由
}

// RegisterModules 按序执行各模块的注册逻辑。
func RegisterModules(app *App, mods []Module) error {
	for _, m := range mods {
		if err := m.Register(app); err != nil {
			return fmt.Errorf("module %s: %w", m.Name(), err)
		}
	}
	return nil
}

func runMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	configPath := fs.String("config", "/etc/dash/config.toml", "path to config.toml")
	migDir := fs.String("dir", "migrations", "path to migrations directory")
	driverFlag := fs.String("driver", "", "override db driver (oracle | mysql)")
	dsnFlag := fs.String("dsn", "", "override db dsn")
	userFlag := fs.String("user", "", "override db user")
	passFlag := fs.String("password", "", "override db password")

	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	if *driverFlag != "" {
		cfg.DB.Driver = *driverFlag
	}
	if *dsnFlag != "" {
		cfg.DB.DSN = *dsnFlag
	}
	if *userFlag != "" {
		cfg.DB.User = *userFlag
	}
	if *passFlag != "" {
		cfg.DB.Password = *passFlag
	}

	dbCfg := cfg.DB.DBOptions()
	database, err := db.Open(&dbCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %s\n", logx.Redact(err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	migrator := migrate.New(database, *migDir)
	if err := migrator.Up(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Migration failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Database schema migration completed successfully.")
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrate(os.Args[2:])
		return
	}

	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&showVersion, "v", false, "print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("dashd %s (commit: %s, built: %s)\n", version, gitCommit, buildTime)
		os.Exit(0)
	}

	app := &App{}
	if err := RegisterModules(app, modules); err != nil {
		log.Fatalf("failed to register modules: %v", err)
	}

	log.Printf("dashd %s initialized with %d modules", version, len(modules))
}
