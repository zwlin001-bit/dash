package jobs

import (
	"errors"
	"sync"
	"time"
)

// Registry 保存已注册的任务类型定义。
type Registry struct {
	mu   sync.RWMutex
	defs map[string]JobDefinition
}

// NewRegistry 创建并初始化注册表。
func NewRegistry() *Registry {
	r := &Registry{
		defs: make(map[string]JobDefinition),
	}
	r.registerBuiltins()
	return r
}

// Register 注册一个 JobDefinition。
func (r *Registry) Register(def JobDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if def.Timeout <= 0 {
		def.Timeout = 10 * time.Minute
	}
	if def.MaxAttempt <= 0 {
		def.MaxAttempt = 1
	}
	r.defs[def.Kind] = def
}

// Get 根据 kind 获取 JobDefinition。
func (r *Registry) Get(kind string) (JobDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[kind]
	return def, ok
}

// List 返回所有已注册的 JobDefinition 副本。
func (r *Registry) List() []JobDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make([]JobDefinition, 0, len(r.defs))
	for _, d := range r.defs {
		res = append(res, d)
	}
	return res
}

func (r *Registry) registerBuiltins() {
	// 内置测试任务：3 个步骤 (验收 1 & 2)
	r.Register(JobDefinition{
		Kind:        "test.three_steps",
		Description: "系统内置测试任务（3个步骤，支持模拟失败与重试）",
		Timeout:     10 * time.Minute,
		MaxAttempt:  3,
		Steps: []StepDef{
			{
				Name:       "准备环境",
				Idempotent: true,
				Run: func(ctx *StepContext) error {
					ctx.Log("步骤 1/3：正在初始化运行上下文...")
					delay := getDelay(ctx.Params, "step1_delay_ms", 300)
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(delay):
					}
					if shouldFail(ctx.Params, "fail_step", 1, ctx.Attempt) {
						ctx.Log("步骤 1/3 发生模拟故障")
						return errors.New("simulated failure at step 1")
					}
					ctx.Log("步骤 1/3：准备完毕")
					ctx.SetSharedData("step1_ready", true)
					return nil
				},
			},
			{
				Name:       "核心处理",
				Idempotent: true,
				Run: func(ctx *StepContext) error {
					ctx.Log("步骤 2/3：开始执行核心处理逻辑...")
					delay := getDelay(ctx.Params, "step2_delay_ms", 300)
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(delay):
					}
					if shouldFail(ctx.Params, "fail_step", 2, ctx.Attempt) {
						ctx.Log("步骤 2/3 发生模拟故障")
						return errors.New("simulated failure at step 2")
					}
					ctx.Log("步骤 2/3：核心处理完成")
					ctx.SetSharedData("step2_data", "success")
					return nil
				},
			},
			{
				Name:       "收尾确认",
				Idempotent: true,
				Run: func(ctx *StepContext) error {
					ctx.Log("步骤 3/3：开始收尾与状态确认...")
					delay := getDelay(ctx.Params, "step3_delay_ms", 300)
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(delay):
					}
					if shouldFail(ctx.Params, "fail_step", 3, ctx.Attempt) {
						ctx.Log("步骤 3/3 发生模拟故障")
						return errors.New("simulated failure at step 3")
					}
					ctx.Log("步骤 3/3：所有步骤顺利完成")
					ctx.SetResult(map[string]any{
						"status":       "ok",
						"completed_at": time.Now().Format(time.RFC3339),
					})
					return nil
				},
			},
		},
	})
}

func getDelay(params map[string]any, key string, defMs int) time.Duration {
	if params == nil {
		return time.Duration(defMs) * time.Millisecond
	}
	if v, ok := params[key]; ok {
		switch n := v.(type) {
		case float64:
			return time.Duration(n) * time.Millisecond
		case int:
			return time.Duration(n) * time.Millisecond
		case int64:
			return time.Duration(n) * time.Millisecond
		}
	}
	return time.Duration(defMs) * time.Millisecond
}

func shouldFail(params map[string]any, key string, stepIndex int, attempt int) bool {
	if params == nil {
		return false
	}
	// 支持 fail_once_at_step: 仅在初次执行时失败，重试（attempt > 1）时不失败
	if v, ok := params["fail_once_at_step"]; ok {
		var failStep int
		switch n := v.(type) {
		case float64:
			failStep = int(n)
		case int:
			failStep = n
		}
		if failStep == stepIndex && attempt <= 1 {
			return true
		}
	}
	if v, ok := params[key]; ok {
		var failStep int
		switch n := v.(type) {
		case float64:
			failStep = int(n)
		case int:
			failStep = n
		}
		if failStep == stepIndex {
			return true
		}
	}
	return false
}
