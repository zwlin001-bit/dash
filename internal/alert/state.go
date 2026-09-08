package alert

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"dash/internal/events"
	"dash/internal/logx"
	"dash/internal/notify"
	"dash/internal/ulid"
)

// In-memory states for (rule, node)
const (
	StateOK      = "ok"
	StatePending = "pending"
	StateFiring  = "firing"
)

// NodeState tracks the state machine progress for a single (rule_id, node_id) pair (07-monitoring.md §2.2).
type NodeState struct {
	RuleID             string
	NodeID             string
	State              string // ok / pending / firing
	FirstTriggeredAtMs int64
	LastNotifiedAtMs   int64
	PeakValue          float64
	ActiveEventID      string // ID in alert_events while firing
}

// StateMachine manages state transitions, debouncing, silence windows, and recovery.
type StateMachine struct {
	store      *Store
	dispatcher *notify.Dispatcher

	mu     sync.RWMutex
	states map[string]*NodeState // key: "rule_id:node_id"

	// Mockable notifier for unit testing
	notifyOverride func(ctx context.Context, e events.Event, channelIDs []string, ruleID string)
}

// NewStateMachine creates a new StateMachine.
func NewStateMachine(s *Store, d *notify.Dispatcher) *StateMachine {
	return &StateMachine{
		store:      s,
		dispatcher: d,
		states:     make(map[string]*NodeState),
	}
}

func stateKey(ruleID, nodeID string) string {
	return ruleID + ":" + nodeID
}

// SetNotifyOverride sets a custom notification handler (useful for testing).
func (sm *StateMachine) SetNotifyOverride(fn func(ctx context.Context, e events.Event, channelIDs []string, ruleID string)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.notifyOverride = fn
}

// RecoverFromDB loads all currently active firing events from database on startup (07-monitoring.md §2.2).
// 进程重启后必须从 alert_events 恢复 firing 状态，避免重复告警并保证恢复通知可发。
func (sm *StateMachine) RecoverFromDB(ctx context.Context) error {
	activeEvents, err := sm.store.GetActiveFiringEvents(ctx)
	if err != nil {
		return fmt.Errorf("alert: recover firing events failed: %w", err)
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	for _, ev := range activeEvents {
		k := stateKey(ev.AlertRuleID, ev.NodeID)
		lastNotified := ev.FiredAtMs
		if ev.NotifiedAtMs != nil && *ev.NotifiedAtMs > 0 {
			lastNotified = *ev.NotifiedAtMs
		}
		var peak float64
		if ev.PeakValue != nil {
			peak = *ev.PeakValue
		}

		sm.states[k] = &NodeState{
			RuleID:             ev.AlertRuleID,
			NodeID:             ev.NodeID,
			State:              StateFiring,
			FirstTriggeredAtMs: ev.FiredAtMs,
			LastNotifiedAtMs:   lastNotified,
			PeakValue:          peak,
			ActiveEventID:      ev.ID,
		}
	}

	if len(activeEvents) > 0 {
		logx.Info(fmt.Sprintf("alert: restored %d firing alert states from database", len(activeEvents)))
	}
	return nil
}

// GetState returns the current state of a (rule, node) pair.
func (sm *StateMachine) GetState(ruleID, nodeID string) *NodeState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	st, ok := sm.states[stateKey(ruleID, nodeID)]
	if !ok {
		return &NodeState{
			RuleID: ruleID,
			NodeID: nodeID,
			State:  StateOK,
		}
	}
	// return copy
	cp := *st
	return &cp
}

// ProcessResult applies an evaluation result through the state machine.
func (sm *StateMachine) ProcessResult(ctx context.Context, rule *AlertRule, res EvalResult) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	k := stateKey(rule.ID, res.NodeID)
	st, exists := sm.states[k]
	if !exists {
		st = &NodeState{
			RuleID: rule.ID,
			NodeID: res.NodeID,
			State:  StateOK,
		}
		sm.states[k] = st
	}

	nowMs := res.EvaluatedAt
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}

	if res.Breached {
		return sm.handleBreached(ctx, rule, res, st, nowMs)
	}
	return sm.handleRecovered(ctx, rule, res, st, nowMs)
}

func (sm *StateMachine) handleBreached(ctx context.Context, rule *AlertRule, res EvalResult, st *NodeState, nowMs int64) error {
	switch st.State {
	case StateOK:
		// Check debouncing: if duration_s <= 0, immediately firing
		if rule.DurationS <= 0 {
			return sm.transitionToFiring(ctx, rule, res, st, nowMs)
		}
		// Otherwise, enter pending state (pending 不发通知)
		st.State = StatePending
		st.FirstTriggeredAtMs = nowMs
		st.PeakValue = res.CurrentVal
		return nil

	case StatePending:
		// Update peak value
		if isMoreExtreme(rule.CompareOp, res.CurrentVal, st.PeakValue) {
			st.PeakValue = res.CurrentVal
		}

		// Check if duration_s is satisfied
		durationMs := int64(rule.DurationS) * 1000
		if nowMs-st.FirstTriggeredAtMs >= durationMs {
			return sm.transitionToFiring(ctx, rule, res, st, nowMs)
		}
		// Still pending, do nothing
		return nil

	case StateFiring:
		// Still firing
		if isMoreExtreme(rule.CompareOp, res.CurrentVal, st.PeakValue) {
			st.PeakValue = res.CurrentVal
			if st.ActiveEventID != "" {
				_ = sm.store.UpdateEvent(ctx, &AlertEvent{
					ID:         st.ActiveEventID,
					EventState: EventStateFiring,
					PeakValue:  &st.PeakValue,
				})
			}
		}

		// Silence check for repeat notification
		if rule.SilenceS > 0 && (nowMs-st.LastNotifiedAtMs) >= int64(rule.SilenceS)*1000 {
			// silence window elapsed while still firing, send repeat notification
			sm.sendAlertNotification(ctx, rule, res, st, nowMs, EventStateFiring)
			st.LastNotifiedAtMs = nowMs
			if st.ActiveEventID != "" {
				_ = sm.store.UpdateEvent(ctx, &AlertEvent{
					ID:           st.ActiveEventID,
					EventState:   EventStateFiring,
					NotifiedAtMs: &nowMs,
				})
			}
		}
		return nil

	default:
		st.State = StateOK
		return nil
	}
}

func (sm *StateMachine) handleRecovered(ctx context.Context, rule *AlertRule, res EvalResult, st *NodeState, nowMs int64) error {
	switch st.State {
	case StatePending:
		// Condition returned to normal during pending debounce -> simply reset to OK, no notifications
		st.State = StateOK
		st.FirstTriggeredAtMs = 0
		st.PeakValue = 0
		return nil

	case StateFiring:
		// Transition from firing -> resolved, send recovery notification
		st.State = StateOK
		if st.ActiveEventID != "" {
			_ = sm.store.ResolveEvent(ctx, st.ActiveEventID, nowMs)
		}

		sm.sendAlertNotification(ctx, rule, res, st, nowMs, EventStateResolved)

		st.ActiveEventID = ""
		st.FirstTriggeredAtMs = 0
		st.PeakValue = 0
		return nil

	case StateOK:
		// Already OK
		return nil

	default:
		st.State = StateOK
		return nil
	}
}

func (sm *StateMachine) transitionToFiring(ctx context.Context, rule *AlertRule, res EvalResult, st *NodeState, nowMs int64) error {
	st.State = StateFiring
	st.PeakValue = res.CurrentVal
	eventID := ulid.New()
	st.ActiveEventID = eventID

	// Silence check: silence_s 内同一 (rule, node) 不重复通知，但事件照样记录
	isSilenced := false
	if rule.SilenceS > 0 && st.LastNotifiedAtMs > 0 && (nowMs-st.LastNotifiedAtMs) < int64(rule.SilenceS)*1000 {
		isSilenced = true
	}

	var notifiedAt *int64
	if !isSilenced {
		notifiedAt = &nowMs
		st.LastNotifiedAtMs = nowMs
	}

	peakVal := st.PeakValue
	ev := &AlertEvent{
		ID:           eventID,
		AlertRuleID:  rule.ID,
		NodeID:       res.NodeID,
		EventState:   EventStateFiring,
		FiredAtMs:    nowMs,
		PeakValue:    &peakVal,
		Detail:       res.Detail,
		NotifiedAtMs: notifiedAt,
		CreatedAtMs:  nowMs,
	}

	if err := sm.store.CreateEvent(ctx, ev); err != nil {
		logx.Error(fmt.Sprintf("alert: failed to insert alert event: %v", err))
	}

	if !isSilenced {
		sm.sendAlertNotification(ctx, rule, res, st, nowMs, EventStateFiring)
	}

	return nil
}

func (sm *StateMachine) sendAlertNotification(ctx context.Context, rule *AlertRule, res EvalResult, st *NodeState, nowMs int64, state string) {
	// ★ dedup_key 用 alert:<rule_id>:<node_id>:<fired_at 的小时> (07-monitoring.md §2.1)
	firedHour := time.UnixMilli(nowMs).UTC().Format("2006010215")
	dedupKey := fmt.Sprintf("alert:%s:%s:%s", rule.ID, res.NodeID, firedHour)

	eventType := "alert.firing"
	title := fmt.Sprintf("【告警触发】%s - %s", rule.Name, res.NodeName)
	if state == EventStateResolved {
		eventType = "alert.resolved"
		title = fmt.Sprintf("【告警恢复】%s - %s", rule.Name, res.NodeName)
	} else if rule.RuleKind == RuleKindBudget {
		if res.CurrentVal >= 1.0 {
			eventType = "billing.budget_exceeded"
			title = fmt.Sprintf("【云账单超出预算】%s 当月支出已超额", res.NodeName)
		} else {
			eventType = "billing.budget_warning"
			title = fmt.Sprintf("【云账单预算预警】%s 当月支出达到预警线", res.NodeName)
		}
	}

	payload := map[string]any{
		"NodeName":     res.NodeName,
		"BudgetName":   res.NodeName,
		"RuleName":     rule.Name,
		"Severity":     rule.Severity,
		"Value":        fmt.Sprintf("%.2f", res.CurrentVal),
		"Threshold":    fmt.Sprintf("%.2f", rule.Threshold),
		"ProgressPct":  fmt.Sprintf("%.1f", res.CurrentVal*100),
		"FiredAt":      time.UnixMilli(nowMs).UTC().Format("2006-01-02 15:04:05 UTC"),
		"EventState":   state,
		"Detail":       res.Detail,
		"OccurredAtMs": nowMs,
	}

	targetKind := "node"
	if rule.RuleKind == RuleKindBudget {
		targetKind = "budget"
	}

	ev := events.Event{
		Type:       eventType,
		Source:     "alert",
		TargetKind: targetKind,
		TargetID:   res.NodeID,
		Title:      title,
		Payload:    payload,
		DedupKey:   dedupKey,
		OccurredAt: nowMs,
	}

	// 1. Emit to system events bus (enters events table and timeline)
	events.Emit(ctx, ev)

	// 2. Dispatch to bound notification channels in alert_rule_channels
	if sm.notifyOverride != nil {
		sm.notifyOverride(ctx, ev, rule.ChannelIDs, rule.ID)
		return
	}

	if sm.dispatcher != nil && len(rule.ChannelIDs) > 0 {
		sm.dispatcher.DispatchToChannels(ctx, ev, rule.ChannelIDs, rule.ID)
	}
}

func isMoreExtreme(op string, current, peak float64) bool {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case CompareOpLT, CompareOpLTE:
		return current < peak
	case CompareOpGT, CompareOpGTE:
		fallthrough
	default:
		return current > peak
	}
}
