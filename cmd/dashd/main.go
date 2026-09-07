package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
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
	"dash/internal/ulid"
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
	inventory.NewModule(),
	ingest.NewModule(), // ★ 必须在 control 之前：control 依赖 app.Ingester
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

func generateSecurePassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
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

	migrator := migrate.DefaultMigrator(database, *migDir)
	if err := migrator.Up(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Migration failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Database schema migration completed successfully.")
}

func runInitDB(args []string) {
	fs := flag.NewFlagSet("init-db", flag.ExitOnError)
	configPath := fs.String("config", "/etc/dash/config.toml", "path to config.toml")
	migDir := fs.String("dir", "migrations", "path to migrations directory")
	domainFlag := fs.String("domain", "", "site domain (e.g. dash.example.com)")
	agentDomainFlag := fs.String("agent-domain", "", "agent ingress domain (e.g. agent.example.com)")
	consoleDomainFlag := fs.String("console-domain", "", "console internal domain (e.g. console.dash.internal)")
	adminUserFlag := fs.String("admin-user", "admin", "admin username")
	adminPassFlag := fs.String("admin-password", "", "admin password (auto-generated if empty and user does not exist)")
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

	// 1. Run migrations
	migrator := migrate.DefaultMigrator(database, *migDir)
	if err := migrator.Up(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Migration failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Database schema migration completed successfully.")

	// 2. Set site.domain and site.console_domain in settings if provided
	if *agentDomainFlag != "" && *domainFlag == "" {
		*domainFlag = *agentDomainFlag
	}
	if *domainFlag != "" {
		domain := strings.TrimSpace(*domainFlag)
		upsertSQL := database.Dialect().UpsertSQL("settings", []string{"setting_key"}, []string{"setting_val", "updated_at_ms"})
		nowMs := time.Now().UnixMilli()
		_, err = database.Exec(ctx, upsertSQL, "site.domain", domain, nowMs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to configure site domain: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Site domain configured: %s\n", domain)
	}

	if *consoleDomainFlag != "" {
		consoleDomain := strings.TrimSpace(*consoleDomainFlag)
		upsertSQL := database.Dialect().UpsertSQL("settings", []string{"setting_key"}, []string{"setting_val", "updated_at_ms"})
		nowMs := time.Now().UnixMilli()
		_, err = database.Exec(ctx, upsertSQL, "site.console_domain", consoleDomain, nowMs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to configure console domain: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Console domain configured: %s\n", consoleDomain)
	}

	// 3. Create or update admin user in account_users
	adminUser := strings.TrimSpace(*adminUserFlag)
	if adminUser == "" {
		adminUser = "admin"
	}

	var existingCount int
	err = database.QueryRow(ctx, "SELECT COUNT(*) FROM account_users WHERE username = ?", adminUser).Scan(&existingCount)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to check account_users: %v\n", err)
		os.Exit(1)
	}

	if existingCount > 0 {
		if *adminPassFlag != "" {
			hash, err := auth.HashPassword(*adminPassFlag)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to hash password: %v\n", err)
				os.Exit(1)
			}
			nowMs := time.Now().UnixMilli()
			_, err = database.Exec(ctx, "UPDATE account_users SET passwd_hash = ?, updated_at_ms = ? WHERE username = ?", hash, nowMs, adminUser)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to update admin user password: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Admin user %s password updated.\n", adminUser)
			fmt.Printf("ADMIN_PASSWORD: %s\n", *adminPassFlag)
		} else {
			fmt.Printf("Admin user %s already exists. Preserving existing password.\n", adminUser)
		}
	} else {
		pw := *adminPassFlag
		if pw == "" {
			pw = generateSecurePassword(16)
		}
		hash, err := auth.HashPassword(pw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to hash password: %v\n", err)
			os.Exit(1)
		}
		id := ulid.New()
		nowMs := time.Now().UnixMilli()
		_, err = database.Exec(ctx, "INSERT INTO account_users (id, username, passwd_hash, is_admin, created_at_ms, updated_at_ms) VALUES (?, ?, ?, 1, ?, ?)", id, adminUser, hash, nowMs, nowMs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create admin user: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Admin user %s created successfully.\n", adminUser)
		fmt.Printf("ADMIN_PASSWORD: %s\n", pw)
	}

	fmt.Println("Database initialization completed successfully.")
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var showVersion bool
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&showVersion, "v", false, "print version and exit")
	cliFlags := config.RegisterFlags(fs)
	_ = fs.Parse(args)

	if showVersion {
		fmt.Printf("dashd %s (commit: %s, built: %s)\n", version, gitCommit, buildTime)
		os.Exit(0)
	}

	opts := cliFlags.Options()
	cfg, err := config.LoadWithOptions(opts)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && opts.ConfigFile == "" && os.Getenv("DASH_CONFIG") == "" {
			logx.Warn("Configuration file not found at default path, using built-in defaults")
			cfg = config.DefaultConfig()
		} else {
			fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
			os.Exit(1)
		}
	}

	app := &App{
		Config:  cfg,
		Version: version,
		Mux:     http.NewServeMux(),
	}

	// Try connecting to database
	if cfg.DB.DSN != "" || cfg.DB.User != "" {
		dbCfg := cfg.DB.DBOptions()
		database, err := db.Open(&dbCfg)
		if err != nil {
			// Per 12-api-spec.md §9: server does not exit on DB error, /healthz reports db: error
			logx.Error(fmt.Sprintf("Failed to connect to database: %s", logx.Redact(err.Error())))
		} else {
			app.DB = database
			defer database.Close()
		}
	}

	if err := RegisterModules(app, modules); err != nil {
		log.Fatalf("failed to register modules: %v", err)
	}

	log.Printf("dashd %s initialized with %d modules", version, len(modules))

	// 启动 HTTP/HTTPS 服务
	shutdownServer, err := api.StartServer(app)
	if err != nil {
		log.Fatalf("failed to start server: %v", err)
	}

	// 阻塞等待信号并优雅关闭
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Printf("收到终止信号，正在优雅关闭服务...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if shutdownServer != nil {
		_ = shutdownServer(shutdownCtx)
	}

	// 优雅关闭：排空时序落库缓冲 (flush ingest)
	if flusher, ok := app.Ingester.(interface{ Stop() }); ok {
		log.Printf("正在排空时序采集缓冲区 (flush ingest)...")
		flusher.Stop()
	}

	if app.DB != nil {
		_ = app.DB.Close()
	}

	log.Printf("dashd 服务已安全退出")
}

func main() {
	// 服务端资源预算：稳态 RSS <= 1 GB
	debug.SetMemoryLimit(1 << 30)

	if len(os.Args) <= 1 {
		runServe(nil)
		return
	}

	first := os.Args[1]
	switch first {
	case "migrate":
		runMigrate(os.Args[2:])
		return
	case "init-db":
		runInitDB(os.Args[2:])
		return
	case "serve":
		runServe(os.Args[2:])
		return
	}

	// 如果以 flag 开头，提取是否有嵌入的子命令
	var filtered []string
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "serve" {
			continue
		}
		if arg == "migrate" {
			runMigrate(append(os.Args[1:i], os.Args[i+1:]...))
			return
		}
		if arg == "init-db" {
			runInitDB(append(os.Args[1:i], os.Args[i+1:]...))
			return
		}
		filtered = append(filtered, arg)
	}

	runServe(filtered)
}
