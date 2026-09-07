package events

import (
	"context"
	"fmt"
	"sync"
	"time"

	"dash/internal/app"
	"dash/internal/logx"
)

var (
	defaultMu    sync.RWMutex
	defaultStore *Store
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

// Emit 是全工程唯一的事件发布入口 (P1-20)。
// ★ Emit 永不返回错误、永不阻塞。写不进去只打日志。
// 事件系统故障绝不允许影响业务流程。
func Emit(ctx context.Context, e Event) {
	s := GetDefaultStore()
	if s == nil {
		logx.Warn(fmt.Sprintf("events: default store is not initialized, dropped event %s (title=%s)", e.Type, e.Title))
		return
	}
	s.Enqueue(e)
}

// Module 实现 app.Module 契约，供 dashd 启动时注册装配。
type Module struct {
	store *Store
}

// NewModule 创建事件总线模块。
func NewModule() *Module {
	return &Module{}
}

func (m *Module) Name() string {
	return "events"
}

func (m *Module) Register(a *app.App) error {
	m.store = NewStore(a.DB, 2048)
	m.store.Start()
	SetDefaultStore(m.store)

	// 若数据库已连接，同步内置类型定义并载入策略
	if a.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.store.SyncBuiltinTypes(ctx); err != nil {
			logx.Warn(fmt.Sprintf("events: failed to sync builtin event types to db: %v", err))
		}
	}

	return nil
}

// Store 返回模块所持有的 Store 实例。
func (m *Module) Store() *Store {
	return m.store
}
