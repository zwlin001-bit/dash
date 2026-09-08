package notify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dash/internal/app"
	"dash/internal/crypto"
	"dash/internal/events"
	"dash/internal/logx"
)

var (
	defaultMu         sync.RWMutex
	defaultStore      *Store
	defaultDispatcher *Dispatcher
)

// SetDefaultStore sets the package-level default Store.
func SetDefaultStore(s *Store) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultStore = s
}

// GetDefaultStore returns the package-level default Store.
func GetDefaultStore() *Store {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultStore
}

// SetDefaultDispatcher sets the package-level default Dispatcher.
func SetDefaultDispatcher(d *Dispatcher) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultDispatcher = d
}

// GetDefaultDispatcher returns the package-level default Dispatcher.
func GetDefaultDispatcher() *Dispatcher {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultDispatcher
}

// Module implements the app.Module interface for notify subsystem.
type Module struct {
	store      *Store
	dispatcher *Dispatcher
}

// NewModule creates a new Notify module.
func NewModule() *Module {
	return &Module{}
}

func (m *Module) Name() string {
	return "notify"
}

func (m *Module) Register(a *app.App) error {
	// 1. Initialize crypto manager with master key
	masterKeyPath := "/etc/dash/master.key"
	if a.Config != nil && a.Config.Server.MasterKey != "" {
		masterKeyPath = a.Config.Server.MasterKey
	}

	var cryptoMgr *crypto.Manager
	if mgr, err := crypto.NewManager(masterKeyPath); err == nil {
		cryptoMgr = mgr
	} else {
		logx.Warn(fmt.Sprintf("notify: master key file (%s) not found or invalid (%v), attempting dev mode fallback", masterKeyPath, err))
		if a.Config != nil && a.Config.Server.DevNoAuth {
			devKey := []byte("01234567890123456789012345678901")
			cryptoMgr, _ = crypto.NewManagerWithKey(devKey)
		} else {
			dir := filepath.Dir(masterKeyPath)
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				keyBytes := make([]byte, 32)
				copy(keyBytes, []byte(fmt.Sprintf("%032d", time.Now().UnixNano())))
				if wErr := os.WriteFile(masterKeyPath, keyBytes, 0400); wErr == nil {
					cryptoMgr, _ = crypto.NewManagerWithKey(keyBytes)
				}
			}
		}
	}

	// 2. Initialize notify store and template engine
	m.store = NewStore(a.DB, cryptoMgr)
	SetDefaultStore(m.store)

	templateEngine := NewTemplateEngine("/etc/dash/templates")

	siteDomain := "dash.example.com"
	if a.DB != nil {
		var d string
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := a.DB.QueryRow(ctx, "SELECT setting_val FROM settings WHERE setting_key = 'site.domain'").Scan(&d); err == nil && strings.TrimSpace(d) != "" {
			siteDomain = strings.TrimSpace(d)
		}
	}

	// 3. Initialize dispatcher and start background workers
	m.dispatcher = NewDispatcher(m.store, templateEngine, siteDomain, 1024)
	m.dispatcher.Start(2)
	SetDefaultDispatcher(m.dispatcher)

	// 4. Hook dispatcher into events.Store for store+notify events
	if evStore := events.GetDefaultStore(); evStore != nil {
		evStore.SetNotifyHook(m.dispatcher)
	}

	return nil
}

// Store returns the module's Store instance.
func (m *Module) Store() *Store {
	return m.store
}

// Dispatcher returns the module's Dispatcher instance.
func (m *Module) Dispatcher() *Dispatcher {
	return m.dispatcher
}

// Stop gracefully stops the dispatcher workers.
func (m *Module) Stop() {
	if m.dispatcher != nil {
		m.dispatcher.Stop()
	}
}
