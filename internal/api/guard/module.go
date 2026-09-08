package guard

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dash/internal/app"
	"dash/internal/cloud"
	"dash/internal/credentials"
	"dash/internal/crypto"
	"dash/internal/guard"
	"dash/internal/jobs"
	"dash/internal/provider"
)

type Module struct {
	engine  *guard.Engine
	store   *guard.Store
	handler *Handler
}

func NewModule() *Module {
	return &Module{}
}

func (m *Module) Name() string {
	return "guard"
}

func (m *Module) Register(a *app.App) error {
	masterKeyPath := "/etc/dash/master.key"
	if a.Config != nil && a.Config.Server.MasterKey != "" {
		masterKeyPath = a.Config.Server.MasterKey
	}

	masterKey, err := crypto.LoadMasterKey(masterKeyPath)
	if err != nil {
		if flag.Lookup("test.v") != nil || (a.Config != nil && a.Config.Server.DevNoAuth) || os.Getenv("DASH_ENV") == "dev" {
			masterKey = make([]byte, 32)
		} else {
			return fmt.Errorf("guard module: failed to load master key: %w", err)
		}
	}

	var cloudSvc *cloud.Service
	if cs, ok := a.CloudService.(*cloud.Service); ok && cs != nil {
		cloudSvc = cs
	} else {
		credStore := credentials.NewStore(a.DB, masterKey)
		pm := provider.NewManager(filepath.Join(os.TempDir(), "dash-runtime"), "bin", "/usr/local/bin")
		cloudSvc = cloud.NewService(a.DB, credStore, pm)
		a.CloudService = cloudSvc
	}

	var jobEng *jobs.Engine
	if je, ok := a.JobEngine.(*jobs.Engine); ok {
		jobEng = je
	}

	m.store = guard.NewStore(a.DB)

	evalInterval := 60 * time.Second
	if a.Config != nil && a.Config.Server.DevNoAuth {
		evalInterval = 30 * time.Second
	}

	guardCfg := guard.Config{
		Interval:        evalInterval,
		PrewarnRatio:    0.8,
		DefaultTimezone: "Asia/Shanghai",
	}

	if ge, ok := a.GuardEngine.(*guard.Engine); ok && ge != nil {
		m.engine = ge
	} else {
		credStore := credentials.NewStore(a.DB, masterKey)
		pm := provider.NewManager(filepath.Join(os.TempDir(), "dash-runtime"), "bin", "/usr/local/bin")
		if jobEng != nil {
			guard.RegisterGuardJobs(jobEng.Registry(), a.DB, credStore, pm)
		}
		m.engine = guard.NewEngine(a.DB, m.store, credStore, pm, jobEng, guardCfg)
		a.GuardEngine = m.engine
	}

	// 仅在真实运行阶段启动后台主循环 (单测时显式调用 EvaluateOnce)
	if flag.Lookup("test.v") == nil {
		_ = m.engine.Start(context.Background())
	}

	m.handler = NewHandler(m.engine, m.store, cloudSvc, jobEng)
	m.registerRoutes(a)

	return nil
}

func (m *Module) registerRoutes(a *app.App) {
	// 概览
	a.HandleAuthed("GET /api/v1/guard/overview", m.handler.HandleOverview)

	// 演练与触发评估
	a.HandleAuthed("POST /api/v1/guard/dry-run", m.handler.HandleDryRun)
	a.HandleAuthed("POST /api/v1/guard/evaluate", m.handler.HandleEvaluate)

	// 历史周期查询
	a.HandleAuthed("GET /api/v1/guard/cycles", m.handler.HandleListCycles)

	// 规则更新
	a.HandleAuthed("PUT /api/v1/guard/rules/", m.handler.HandleUpdateRule)

	// 手动强制开机 (二次确认)
	a.HandleAuthed("POST /api/v1/guard/instances/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/force-start") {
			m.handler.HandleForceStart(w, r)
			return
		}
		http.NotFound(w, r)
	})
}
