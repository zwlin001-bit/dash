package api

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"dash/internal/app"
	eventsapi "dash/internal/api/events"
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

func (m *WebModule) Register(a *app.App) error {
	if a.Mux == nil {
		a.Mux = http.NewServeMux()
	}

	RegisterRoutes(a.Mux)

	// 若在单测或不需要监听端口的场景下，由环境变量控制不阻塞
	if os.Getenv("DASH_TEST_NO_SERVE") == "1" {
		return nil
	}

	addr := os.Getenv("DASH_SERVER_LISTEN")
	if addr == "" {
		addr = os.Getenv("PORT")
	}
	if addr == "" {
		addr = ":8080"
	}

	server := &http.Server{
		Addr:    addr,
		Handler: a.Mux,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		log.Printf("收到终止信号，正在关闭 Web 控制台...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	log.Printf("dashd 控制台已启动: http://localhost%s (监听地址 %s)", addr, addr)
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
