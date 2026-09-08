package exec

import (
	"context"
	"fmt"
	"sync"
)

// ActionResult 是一个动作执行完毕后的标准结果。
type ActionResult struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	Truncated bool
}

// ActionHandler 是白名单动作的处理函数。
// 接收 context 与动作专有参数，返回标准结果或系统级错误。
type ActionHandler func(ctx context.Context, args map[string]interface{}) (*ActionResult, error)

// Registry 是动作的硬编码白名单注册表。
// ★ 本书交付执行框架，注册表初始为空（动作在 101~104 四本中逐批注册）。
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]ActionHandler
}

// NewRegistry 创建一个空的白名单注册表。
func NewRegistry() *Registry {
	return &Registry{
		handlers: make(map[string]ActionHandler),
	}
}

// Register 注册一个动作处理函数。
// 如果动作已存在，返回错误以防意外覆盖。
func (r *Registry) Register(action string, h ActionHandler) error {
	if action == "" {
		return fmt.Errorf("action name cannot be empty")
	}
	if h == nil {
		return fmt.Errorf("action handler cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[action]; exists {
		return fmt.Errorf("action %q already registered", action)
	}
	r.handlers[action] = h
	return nil
}

// Get 获取动作对应的处理函数。
func (r *Registry) Get(action string) (ActionHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[action]
	return h, ok
}

// Has 检查动作是否在白名单注册表中。
func (r *Registry) Has(action string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.handlers[action]
	return ok
}

// Actions 返回当前已注册的所有白名单动作列表。
func (r *Registry) Actions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		list = append(list, k)
	}
	return list
}

// RegisterNoop 注册一个仅供自测用的 no-op 动作（98.md §2.1）。
func (r *Registry) RegisterNoop(action string) error {
	return r.Register(action, func(ctx context.Context, args map[string]interface{}) (*ActionResult, error) {
		return &ActionResult{
			ExitCode:  0,
			Stdout:    "ok",
			Stderr:    "",
			Truncated: false,
		}, nil
	})
}
