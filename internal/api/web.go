package api

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"dash/internal/app"
	eventsapi "dash/internal/api/events"
)

//go:embed all:dist
var distFS embed.FS

// WebModule 实现 app.Module 接口，负责挂载内嵌前端产物至 Web 服务路由。
type WebModule struct{}

// NewModule 创建 WebModule 实例。
func NewModule() app.Module {
	return &WebModule{}
}

func (m *WebModule) Name() string {
	return "web"
}

func (m *WebModule) Register(a *app.App) error {
	// 注册事件相关 API 到 App（带鉴权）
	eventsapi.RegisterAppRoutes(a, nil)

	// 免鉴权白名单：前端静态资源及 SPA 兜底路由
	a.HandlePublic("/", Handler().ServeHTTP)
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
