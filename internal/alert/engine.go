package alert

import (
	"context"
	"fmt"
	"sync"
	"time"

	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/logx"
	"dash/internal/notify"
)

func init() {
	events.RegisterType(events.TypeDef{
		Type:               "alert.firing",
		DisplayName:        "告警触发",
		DefaultSeverity:    "warning",
		DefaultDisposition: "store",
		Description:        "监控告警规则被触发进入 firing 状态",
	})
	events.RegisterType(events.TypeDef{
		Type:               "alert.resolved",
		DisplayName:        "告警恢复",
		DefaultSeverity:    "info",
		DefaultDisposition: "store",
		Description:        "监控告警规则恢复正常进入 resolved 状态",
	})
}

// Config defines scheduling intervals for the alert engine.
type Config struct {
	MetricInterval  time.Duration // Default 1m
	OfflineInterval time.Duration // Default 30s
	HourlyInterval  time.Duration // Default 1h (for expiry & traffic)
}

// DefaultConfig returns recommended intervals per 07-monitoring.md §2.2.
func DefaultConfig() Config {
	return Config{
		MetricInterval:  1 * time.Minute,
		OfflineInterval: 30 * time.Second,
		HourlyInterval:  1 * time.Hour,
	}
}

// Engine coordinates rule evaluation and alerting life-cycles.
type Engine struct {
	db           *db.DB
	store        *Store
	evaluator    *Evaluator
	stateMachine *StateMachine
	cfg          Config

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
}

// NewEngine creates and configures the alert Engine.
func NewEngine(database *db.DB, s *Store, d *notify.Dispatcher, cfg Config) *Engine {
	if cfg.MetricInterval <= 0 {
		cfg.MetricInterval = 1 * time.Minute
	}
	if cfg.OfflineInterval <= 0 {
		cfg.OfflineInterval = 30 * time.Second
	}
	if cfg.HourlyInterval <= 0 {
		cfg.HourlyInterval = 1 * time.Hour
	}

	eval := NewEvaluator(s)
	sm := NewStateMachine(s, d)

	return &Engine{
		db:           database,
		store:        s,
		evaluator:    eval,
		stateMachine: sm,
		cfg:          cfg,
	}
}

// Store returns the underlying rule store.
func (e *Engine) Store() *Store {
	return e.store
}

// StateMachine returns the state machine.
func (e *Engine) StateMachine() *StateMachine {
	return e.stateMachine
}

// Evaluator returns the evaluator.
func (e *Engine) Evaluator() *Evaluator {
	return e.evaluator
}

// Start begins background scheduling and restores firing states from DB.
func (e *Engine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.ctx, e.cancel = context.WithCancel(context.Background())

	// 1. Synchronize default built-in expiry reminder rules
	initCtx, cancelInit := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancelInit()
	if err := e.store.SyncBuiltinRules(initCtx); err != nil {
		logx.Warn(fmt.Sprintf("alert: failed to sync builtin alert rules: %v", err))
	}

	// 2. Restore active firing state from alert_events in database (07-monitoring.md §2.2)
	if err := e.stateMachine.RecoverFromDB(initCtx); err != nil {
		logx.Warn(fmt.Sprintf("alert: failed to recover firing alert state from db: %v", err))
	}

	// 3. Launch background tickers
	e.wg.Add(3)
	go e.runLoop(e.cfg.MetricInterval, []string{RuleKindMetric})
	go e.runLoop(e.cfg.OfflineInterval, []string{RuleKindOffline})
	go e.runLoop(e.cfg.HourlyInterval, []string{RuleKindExpiry, RuleKindTraffic, RuleKindBudget})

	logx.Info("alert: alert engine started")
	return nil
}

// Stop gracefully stops all background evaluation routines.
func (e *Engine) Stop() {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()

	e.wg.Wait()
	logx.Info("alert: alert engine stopped")
}

func (e *Engine) runLoop(interval time.Duration, kinds []string) {
	defer e.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			for _, kind := range kinds {
				evalCtx, cancel := context.WithTimeout(e.ctx, 45*time.Second)
				if err := e.EvaluateKind(evalCtx, kind); err != nil {
					logx.Warn(fmt.Sprintf("alert: eval kind %s failed: %v", kind, err))
				}
				cancel()
			}
		}
	}
}

// EvaluateKind runs evaluation for all rules of a specific rule kind.
func (e *Engine) EvaluateKind(ctx context.Context, ruleKind string) error {
	rules, err := e.store.GetRulesForKind(ctx, ruleKind)
	if err != nil {
		return err
	}

	for _, rule := range rules {
		results, err := e.evaluator.EvaluateRule(ctx, rule)
		if err != nil {
			logx.Warn(fmt.Sprintf("alert: failed to evaluate rule %s (%s): %v", rule.ID, rule.Name, err))
			continue
		}

		for _, res := range results {
			if err := e.stateMachine.ProcessResult(ctx, rule, res); err != nil {
				logx.Warn(fmt.Sprintf("alert: failed to process state for rule %s node %s: %v", rule.ID, res.NodeID, err))
			}
		}
	}
	return nil
}

// EvaluateAll triggers immediate evaluation across all rule kinds.
func (e *Engine) EvaluateAll(ctx context.Context) error {
	kinds := []string{RuleKindMetric, RuleKindOffline, RuleKindExpiry, RuleKindTraffic, RuleKindBudget}
	for _, k := range kinds {
		if err := e.EvaluateKind(ctx, k); err != nil {
			return err
		}
	}
	return nil
}
