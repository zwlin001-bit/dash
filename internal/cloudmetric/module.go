package cloudmetric

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"dash/internal/app"
	"dash/internal/cloud"
	"dash/internal/credentials"
	"dash/internal/crypto"
	"dash/internal/jobs"
	"dash/internal/logx"
	"dash/internal/provider"
)

// Module implements app.Module for cloudmetric.
type Module struct {
	store   *Store
	syncer  *Syncer
	handler *Handler

	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewModule creates a new cloudmetric module.
func NewModule() *Module {
	return &Module{
		stopCh: make(chan struct{}),
	}
}

func (m *Module) Name() string {
	return "cloudmetric"
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
			_, _ = rand.Read(masterKey)
		} else {
			return fmt.Errorf("cloudmetric module: failed to load master key: %w", err)
		}
	}

	var credStore *credentials.Store
	var pm *provider.Manager

	if cs, ok := a.CloudService.(*cloud.Service); ok && cs != nil {
		// Module cloud is already registered
		credStore = credentials.NewStore(a.DB, masterKey)
		pm = provider.NewManager(filepath.Join(os.TempDir(), "dash-runtime"), "bin", "/usr/local/bin")
	} else {
		credStore = credentials.NewStore(a.DB, masterKey)
		pm = provider.NewManager(filepath.Join(os.TempDir(), "dash-runtime"), "bin", "/usr/local/bin")
	}

	m.store = NewStore(a.DB)
	m.syncer = NewSyncer(a.DB, m.store, credStore, pm)
	m.handler = NewHandler(a.DB, m.store, m.syncer)

	// Register with Job Engine if available
	if je, ok := a.JobEngine.(*jobs.Engine); ok && je != nil {
		RegisterJobs(je.Registry(), m.syncer)
	}

	// Register HTTP routes
	a.HandleAuthed("GET /api/v1/cloud-resources/{id}/metrics", m.handler.HandleResourceMetrics)
	a.HandleAuthed("GET /api/v1/cloud-metrics/accounts/{id}", m.handler.HandleAccountMetrics)
	a.HandleAuthed("GET /api/v1/cloud-metrics", m.handler.HandleAccountMetrics)
	a.HandleAuthed("POST /api/v1/cloud-metrics/sync", m.handler.HandleSync)

	// Start background loop if not in test
	if flag.Lookup("test.v") == nil && a.DB != nil {
		interval := 10 * time.Minute
		if a.Config != nil && a.Config.Server.DevNoAuth {
			interval = 2 * time.Minute
		}
		go m.startLoop(interval)
	}

	return nil
}

func (m *Module) startLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			_, err := m.syncer.SyncOnce(ctx)
			cancel()
			if err != nil {
				logx.Warn("cloud metric: periodic sync error", "err", err)
			}
		}
	}
}

func (m *Module) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
}

// Syncer returns the syncer instance
func (m *Module) Syncer() *Syncer {
	return m.syncer
}

// Store returns the store instance
func (m *Module) Store() *Store {
	return m.store
}
