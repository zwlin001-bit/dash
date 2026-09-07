package api

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"dash/internal/app"
	eventsapi "dash/internal/api/events"
	"dash/internal/config"
	"dash/internal/db"
	"dash/internal/logx"
	"golang.org/x/crypto/acme/autocert"
)

//go:embed all:dist
var distFS embed.FS

// WebModule 实现 app.Module 接口，负责挂载内嵌前端产物与启动 Web 服务。
type WebModule struct{}

// NewModule 创建 WebModule 实例。
func NewModule() app.Module {
	return &WebModule{}
}

func (m *WebModule) Name() string {
	return "web"
}

// HealthzHandler 返回符合 12-api-spec.md §9 规范的健康检查处理器。
func HealthzHandler(database *db.DB, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		dbStatus := "ok"
		statusCode := http.StatusOK
		agentsOnline := 0

		if database == nil {
			dbStatus = "error"
			statusCode = http.StatusServiceUnavailable
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()

			var count int
			err := database.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count)
			if err != nil {
				dbStatus = "error"
				statusCode = http.StatusServiceUnavailable
			} else {
				_ = database.QueryRow(ctx, "SELECT COUNT(*) FROM nodes WHERE conn_state = 'online'").Scan(&agentsOnline)
			}
		}

		status := "ok"
		if statusCode != http.StatusOK {
			status = "error"
		}

		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":        status,
			"version":       version,
			"db":            dbStatus,
			"agents_online": agentsOnline,
		})
	}
}

func (m *WebModule) Register(a *app.App) error {
	if a.Mux == nil {
		a.Mux = http.NewServeMux()
	}

	// 注册 /healthz 和 SPA/静态资源
	a.Mux.HandleFunc("GET /healthz", HealthzHandler(a.DB, a.Version))
	RegisterRoutes(a.Mux)

	// 若在单测或不需要监听端口的场景下，由环境变量控制不阻塞
	if os.Getenv("DASH_TEST_NO_SERVE") == "1" {
		return nil
	}

	cfg := a.Config
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	listenAddr := cfg.Server.Listen
	if envAddr := os.Getenv("DASH_SERVER_LISTEN"); envAddr != "" {
		listenAddr = envAddr
	} else if port := os.Getenv("PORT"); port != "" {
		listenAddr = ":" + port
	}

	listenACME := cfg.Server.ListenACME
	if envACME := os.Getenv("DASH_SERVER_LISTEN_ACME"); envACME != "" {
		listenACME = envACME
	}
	if listenACME == "" {
		listenACME = ":80"
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// 若监听 443 端口，启动自动 ACME 证书与 80 端口 HTTP-01 挑战/跳转 (05-deployment.md)
	if listenAddr == ":443" {
		certDir := filepath.Join(cfg.Server.DataDir, "certs")
		if err := os.MkdirAll(certDir, 0700); err != nil {
			logx.Warn(fmt.Sprintf("failed to create cert directory %s: %v", certDir, err))
		}

		hostPolicy := func(ctx context.Context, host string) error {
			cleanHost := strings.ToLower(strings.Split(host, ":")[0])
			var domain string
			if a.DB != nil {
				_ = a.DB.QueryRow(ctx, "SELECT setting_val FROM settings WHERE setting_key = 'site.domain'").Scan(&domain)
			}
			domain = strings.TrimSpace(domain)
			if domain != "" && strings.EqualFold(cleanHost, domain) {
				return nil
			}
			return fmt.Errorf("host %q not permitted by host policy (configured domain: %q)", host, domain)
		}

		certManager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(certDir),
			HostPolicy: hostPolicy,
		}

		// 80 端口 ACME HTTP-01 挑战 + /healthz 放行 + 其余 301 跳转 443
		healthzHandler := HealthzHandler(a.DB, a.Version)
		acmeFallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				healthzHandler.ServeHTTP(w, r)
				return
			}
			target := "https://" + r.Host + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		})

		acmeServer := &http.Server{
			Addr:    listenACME,
			Handler: certManager.HTTPHandler(acmeFallback),
		}

		go func() {
			log.Printf("ACME HTTP-01 挑战服务监听中: %s", listenACME)
			if err := acmeServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("ACME HTTP-01 挑战服务异常退出: %v", err)
			}
		}()

		httpsServer := &http.Server{
			Addr:      listenAddr,
			Handler:   a.Mux,
			TLSConfig: certManager.TLSConfig(),
		}

		go func() {
			<-stop
			log.Printf("收到终止信号，正在关闭服务...")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = acmeServer.Shutdown(ctx)
			_ = httpsServer.Shutdown(ctx)
		}()

		log.Printf("dashd HTTPS 控制台已启动: 监听 %s, ACME 监听 %s", listenAddr, listenACME)
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("https server failed: %w", err)
		}

		return nil
	}

	// 非 443 端口：普通 HTTP 服务（供开发/单测环境使用）
	server := &http.Server{
		Addr:    listenAddr,
		Handler: a.Mux,
	}

	go func() {
		<-stop
		log.Printf("收到终止信号，正在关闭 Web 控制台...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	log.Printf("dashd 控制台已启动: http://localhost%s (监听地址 %s)", listenAddr, listenAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("web server failed: %w", err)
	}

	return nil
}

// Handler 返回内嵌 SPA 与静态资源的处理 Handler。
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(fmt.Sprintf("failed to get sub FS for dist: %v", err))
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API 路由未匹配时返回 404 JSON，不回退到 index.html
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":-32004,"message":"endpoint not found"}`))
			return
		}

		cleanPath := path.Clean(r.URL.Path)
		trimmedPath := strings.TrimPrefix(cleanPath, "/")

		// 根路径直接返回 index.html
		if trimmedPath == "" || trimmedPath == "." {
			serveIndexHTML(w, sub)
			return
		}

		// 检查静态资源是否存在
		f, err := sub.Open(trimmedPath)
		if err == nil {
			stat, err := f.Stat()
			_ = f.Close()
			if err == nil && !stat.IsDir() {
				// 静态文件存在且非目录，正常托管
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// 其它所有 SPA 前端页面路由回退到 index.html
		serveIndexHTML(w, sub)
	})
}

func serveIndexHTML(w http.ResponseWriter, fsys fs.FS) {
	indexFile, err := fsys.Open("index.html")
	if err != nil {
		http.Error(w, "index.html not found in embedded dist", http.StatusInternalServerError)
		return
	}
	defer indexFile.Close()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, indexFile)
}

// RegisterRoutes 注册静态资源及 SPA 前端路由至 mux。
func RegisterRoutes(mux *http.ServeMux) {
	eventsapi.RegisterRoutes(mux, nil)
	mux.Handle("/", Handler())
}
