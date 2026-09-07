package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
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
	"strings"
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
// 模块注册顺序有依赖，严格保持并加注释：
// auth → inventory → ingest → control → metrics → events → settings → api
// 1. auth: 用户认证与当前用户接口 (GET /api/v1/me, POST /api/v1/login 等)
// 2. inventory: 机器资产管理、标签、分组及注册令牌 CRUD
// 3. ingest: 指标接收落库服务（★ 必须在 control 之前：control 依赖 app.Ingester）
// 4. control: Agent 长连接、RPC 调用与在线状态维护 (依赖 Ingester 落库指标)
// 5. metrics: 指标时序查询与 SSE 实时广播 (监听 Ingester 最新值推送)
// 6. events: 事件发布与订阅总线
// 7. settings: 系统全局配置管理
// 8. api: 静态资源与 SPA 路由（★ 必须最后：挂 "/" 作为 SPA 兜底路由）
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

func runInitDB(args []string) {
	fs := flag.NewFlagSet("init-db", flag.ExitOnError)
	configPath := fs.String("config", "/etc/dash/config.toml", "path to config.toml")
	migDir := fs.String("dir", "migrations", "path to migrations directory")
	agentDomainFlag := fs.String("agent-domain", "", "agent ingress domain (e.g. agent.example.com)")
	consoleDomainFlag := fs.String("console-domain", "", "console internal domain (e.g. console.dash.internal)")
	domainFlag := fs.String("domain", "", "site domain (e.g. dash.example.com)")
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

	if *agentDomainFlag != "" && *domainFlag == "" {
		*domainFlag = *agentDomainFlag
	}

	// 2. Set site.domain in settings if provided
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
		if _, err := database.Exec(ctx, upsertSQL, "site.console_domain", consoleDomain, nowMs); err != nil {
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
	// ★ 服务端资源预算：设置 1 GB 内存上限，让 GC 在接近上限时更积极
	debug.SetMemoryLimit(1 << 30)

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cliFlags := config.RegisterFlags(fs)
	var showVersion bool
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&showVersion, "v", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
	}

	if showVersion {
		fmt.Printf("dashd %s (commit: %s, built: %s)\n", version, gitCommit, buildTime)
		os.Exit(0)
	}

	// 1. 加载配置（优先级：命令行 > 环境变量 > 配置文件 > 默认值）
	cfg, err := config.LoadWithOptions(cliFlags.Options())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	// 2. 初始化日志 (logx)
	logLevel := "info"
	if lvl := os.Getenv("DASH_LOG_LEVEL"); lvl != "" {
		logLevel = lvl
	}
	logFormat := "text"
	if fmtEnv := os.Getenv("DASH_LOG_FORMAT"); fmtEnv != "" {
		logFormat = fmtEnv
	}
	logx.Init(logLevel, logFormat)

	// 3. 连数据库（失败即退出并给可读错误）
	dbCfg := cfg.DB.DBOptions()
	database, err := db.Open(&dbCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %s\n", logx.Redact(err.Error()))
		os.Exit(1)
	}

	// 4. 构造 App{Mux, DB, Config, Version}
	a := app.NewApp(database, cfg, version)

	// 5. RegisterModules 按序注册子模块
	if err := RegisterModules(a, modules); err != nil {
		logx.Error(fmt.Sprintf("failed to register modules: %v", err))
		_ = database.Close()
		os.Exit(1)
	}

	// 免鉴权白名单：注册统一健康检查探活端点 (12-api-spec.md §9)
	a.HandlePublic("/healthz", a.HealthzHandler())

	// 6. 启动 HTTP 服务，监听地址从 config.Server.Listen 读取
	listenAddr := cfg.Server.Listen
	if listenAddr == "" {
		listenAddr = ":443"
	}

	server := &http.Server{
		Addr:    listenAddr,
		Handler: a.Mux,
	}

	serverErrCh := make(chan error, 1)
	go func() {
		logx.Info(fmt.Sprintf("dashd %s listening on %s (loaded %d modules)", version, listenAddr, len(modules)))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
	}()

	// 7. 阻塞等待退出信号或启动异常
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logx.Info(fmt.Sprintf("received signal %v, initiating graceful shutdown...", sig))
	case err := <-serverErrCh:
		logx.Error(fmt.Sprintf("http server failed: %v", err))
		_ = a.Close()
		os.Exit(1)
	}

	// 优雅退出顺序：
	// a. 先停接新请求 (Shutdown)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logx.Warn(fmt.Sprintf("http server shutdown error: %v", err))
	}

	// b. 再 flush ingest 的缓冲（否则最后一批指标会丢）
	logx.Info("flushing ingest buffer...")
	a.FlushIngest()
	logx.Info("ingest flush completed")

	// c. 关闭注册表长连接会话与事件总线
	if reg, ok := a.Registry.(interface{ Stop() }); ok && reg != nil {
		reg.Stop()
	}
	if eventsStore := events.GetDefaultStore(); eventsStore != nil {
		eventsStore.Stop()
	}

	// d. 最后关数据库
	if a.DB != nil {
		logx.Info("closing database...")
		if err := a.DB.Close(); err != nil {
			logx.Warn(fmt.Sprintf("database close error: %v", err))
		}
	}

	logx.Info("dashd exited gracefully")
}

// parseCLIArgs 解析命令行参数，提取子命令（serve 或 migrate）并返回剩余参数。
// 支持:
//
//	dashd
//	dashd -config <path>
//	dashd -config <path> serve
//	dashd serve -config <path>
//	dashd -config <path> migrate
//	dashd migrate -config <path>
func parseCLIArgs(args []string) (subcmd string, cleanArgs []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		prevIsValFlag := i > 0 && (args[i-1] == "-config" || args[i-1] == "--config")
		if subcmd == "" && !prevIsValFlag && (arg == "migrate" || arg == "serve" || arg == "init-db") {
			subcmd = arg
			continue
		}
		cleanArgs = append(cleanArgs, arg)
	}
	return subcmd, cleanArgs
}

func main() {
	subcmd, cleanArgs := parseCLIArgs(os.Args[1:])
	if subcmd == "migrate" {
		runMigrate(cleanArgs)
		return
	}
	if subcmd == "init-db" {
		runInitDB(cleanArgs)
		return
	}

	// 默认运行 serve 模式（支持无子命令或显式 serve）
	runServe(cleanArgs)
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
