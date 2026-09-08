package alert_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"dash/internal/alert"
	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/migrate"
	"dash/internal/ulid"
)

func setupTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("skipping test, MySQL 33306 not available: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, _ = d.Exec(ctx, "SELECT GET_LOCK('dash_alert_test', 60)")
	t.Cleanup(func() {
		_, _ = d.Exec(context.Background(), "SELECT RELEASE_LOCK('dash_alert_test')")
	})

	m := migrate.New(d, "../../migrations")
	if err := m.Up(ctx); err != nil {
		t.Fatalf("run migrations failed: %v", err)
	}

	// Clean tables related to alerts and nodes
	tables := []string{
		"alert_rule_channels", "alert_events", "alert_rules",
		"sample_host_1m", "sample_host_1h", "sample_host",
		"sample_dim_1m", "sample_dim", "metric_series",
		"node_billing", "node_facts", "node_tags", "tags", "nodes",
	}
	for _, tbl := range tables {
		_, _ = d.Exec(ctx, fmt.Sprintf("DELETE FROM %s", tbl))
	}

	return d
}

type notifiedRecord struct {
	Event      events.Event
	ChannelIDs []string
	RuleID     string
}

type notificationTracker struct {
	mu      sync.Mutex
	records []notifiedRecord
}

func (nt *notificationTracker) handle(ctx context.Context, e events.Event, channelIDs []string, ruleID string) {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	nt.records = append(nt.records, notifiedRecord{
		Event:      e,
		ChannelIDs: channelIDs,
		RuleID:     ruleID,
	})
}

func (nt *notificationTracker) count() int {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	return len(nt.records)
}

func (nt *notificationTracker) getRecords() []notifiedRecord {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	cp := make([]notifiedRecord, len(nt.records))
	copy(cp, nt.records)
	return cp
}

// 验收 1: 建一条「CPU > 90 持续 120 秒」的规则 → 把某台机器 CPU 压满 →
// 120 秒后才收到通知，压满瞬间不发（pending 不通知）
// 验收 2: CPU 降下来 → 收到恢复通知
func TestAcceptance1_DebounceAndRecovery(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	store := alert.NewStore(d)
	tracker := &notificationTracker{}
	sm := alert.NewStateMachine(store, nil)
	sm.SetNotifyOverride(tracker.handle)

	// Create test node
	nodeID := ulid.New()
	_, err := d.Exec(ctx, "INSERT INTO nodes (id, name, conn_state, created_at_ms, updated_at_ms) VALUES (?, ?, 'online', ?, ?)",
		nodeID, "node-cpu-test", time.Now().UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("create node failed: %v", err)
	}

	// 规则：CPU > 90 持续 120 秒
	rule := &alert.AlertRule{
		ID:         ulid.New(),
		Name:       "CPU高负载告警",
		IsEnabled:  true,
		RuleKind:   alert.RuleKindMetric,
		ScopeKind:  alert.ScopeKindNode,
		ScopeRef:   nodeID,
		MetricCode: "cpu_pct",
		CompareOp:  alert.CompareOpGT,
		Threshold:  90.0,
		DurationS:  120,
		Severity:   alert.SeverityCritical,
		SilenceS:   3600,
		ChannelIDs: []string{"ch-1"},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("create rule failed: %v", err)
	}

	baseTime := time.Now().UnixMilli()

	// 1. CPU 压满 (95%)，第一轮评估：应当进入 pending，绝对不发通知！
	res1 := alert.EvalResult{
		NodeID:      nodeID,
		NodeName:    "node-cpu-test",
		Breached:    true,
		CurrentVal:  95.0,
		Threshold:   90.0,
		Detail:      "CPU 95%",
		EvaluatedAt: baseTime,
	}
	if err := sm.ProcessResult(ctx, rule, res1); err != nil {
		t.Fatalf("process result 1 failed: %v", err)
	}

	st := sm.GetState(rule.ID, nodeID)
	if st.State != alert.StatePending {
		t.Fatalf("expected state pending, got %s", st.State)
	}
	if tracker.count() != 0 {
		t.Fatalf("pending state must not send notifications, got %d", tracker.count())
	}

	// 2. 60 秒后依然 95%：未满 120 秒，依然 pending，依然不发通知！
	res2 := alert.EvalResult{
		NodeID:      nodeID,
		NodeName:    "node-cpu-test",
		Breached:    true,
		CurrentVal:  96.0,
		Threshold:   90.0,
		Detail:      "CPU 96%",
		EvaluatedAt: baseTime + 60*1000,
	}
	if err := sm.ProcessResult(ctx, rule, res2); err != nil {
		t.Fatalf("process result 2 failed: %v", err)
	}
	st = sm.GetState(rule.ID, nodeID)
	if st.State != alert.StatePending {
		t.Fatalf("expected state pending at 60s, got %s", st.State)
	}
	if tracker.count() != 0 {
		t.Fatalf("expected 0 notifications at 60s, got %d", tracker.count())
	}

	// 3. 120 秒后依然 95%：满 120 秒，转 firing，发送告警通知！
	res3 := alert.EvalResult{
		NodeID:      nodeID,
		NodeName:    "node-cpu-test",
		Breached:    true,
		CurrentVal:  97.0,
		Threshold:   90.0,
		Detail:      "CPU 97%",
		EvaluatedAt: baseTime + 120*1000,
	}
	if err := sm.ProcessResult(ctx, rule, res3); err != nil {
		t.Fatalf("process result 3 failed: %v", err)
	}
	st = sm.GetState(rule.ID, nodeID)
	if st.State != alert.StateFiring {
		t.Fatalf("expected state firing at 120s, got %s", st.State)
	}
	if tracker.count() != 1 {
		t.Fatalf("expected exactly 1 firing notification at 120s, got %d", tracker.count())
	}
	records := tracker.getRecords()
	if records[0].Event.Type != "alert.firing" {
		t.Fatalf("expected alert.firing, got %s", records[0].Event.Type)
	}

	// 验收 2: CPU 降下来 (15%) → 收到恢复通知
	res4 := alert.EvalResult{
		NodeID:      nodeID,
		NodeName:    "node-cpu-test",
		Breached:    false,
		CurrentVal:  15.0,
		Threshold:   90.0,
		Detail:      "CPU 15%",
		EvaluatedAt: baseTime + 180*1000,
	}
	if err := sm.ProcessResult(ctx, rule, res4); err != nil {
		t.Fatalf("process result 4 failed: %v", err)
	}

	st = sm.GetState(rule.ID, nodeID)
	if st.State != alert.StateOK {
		t.Fatalf("expected state ok after recovery, got %s", st.State)
	}
	if tracker.count() != 2 {
		t.Fatalf("expected 2 notifications (1 firing + 1 resolved), got %d", tracker.count())
	}
	records = tracker.getRecords()
	if records[1].Event.Type != "alert.resolved" {
		t.Fatalf("expected alert.resolved, got %s", records[1].Event.Type)
	}

	// Verify database alert_events record is resolved
	eventsList, total, err := store.ListEvents(ctx, alert.EventFilter{RuleID: rule.ID})
	if err != nil || total != 1 {
		t.Fatalf("expected 1 event in db, got %d, err=%v", total, err)
	}
	if eventsList[0].EventState != alert.EventStateResolved {
		t.Fatalf("expected db event state resolved, got %s", eventsList[0].EventState)
	}
	if eventsList[0].ResolvedAtMs == nil {
		t.Fatalf("expected resolved_at_ms to be populated")
	}
}

// 验收 3: ★ 规则处于 firing 时重启 dashd → 不重复告警；随后恢复正常 →
// 仍能发出恢复通知（证明状态从库里恢复了）
func TestAcceptance3_PersistenceRecoveryOnRestart(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	store := alert.NewStore(d)
	tracker1 := &notificationTracker{}
	sm1 := alert.NewStateMachine(store, nil)
	sm1.SetNotifyOverride(tracker1.handle)

	nodeID := ulid.New()
	_, _ = d.Exec(ctx, "INSERT INTO nodes (id, name, conn_state, created_at_ms, updated_at_ms) VALUES (?, ?, 'online', ?, ?)",
		nodeID, "node-restart-test", time.Now().UnixMilli(), time.Now().UnixMilli())

	rule := &alert.AlertRule{
		ID:         ulid.New(),
		Name:       "内存泄漏告警",
		IsEnabled:  true,
		RuleKind:   alert.RuleKindMetric,
		ScopeKind:  alert.ScopeKindNode,
		ScopeRef:   nodeID,
		MetricCode: "mem_used",
		CompareOp:  alert.CompareOpGT,
		Threshold:  1000,
		DurationS:  0, // Instant firing
		Severity:   alert.SeverityWarning,
		SilenceS:   3600,
		ChannelIDs: []string{"ch-1"},
	}
	_ = store.CreateRule(ctx, rule)

	t1 := time.Now().UnixMilli()
	res := alert.EvalResult{
		NodeID:      nodeID,
		NodeName:    "node-restart-test",
		Breached:    true,
		CurrentVal:  1500,
		Threshold:   1000,
		Detail:      "Mem 1500",
		EvaluatedAt: t1,
	}
	_ = sm1.ProcessResult(ctx, rule, res)

	if sm1.GetState(rule.ID, nodeID).State != alert.StateFiring {
		t.Fatalf("expected firing state before restart")
	}
	if tracker1.count() != 1 {
		t.Fatalf("expected 1 firing notification before restart, got %d", tracker1.count())
	}

	// --- 模拟 dashd 进程崩溃 / 重启 ---
	// 创建全新的 StateMachine 模拟进程重新启动
	tracker2 := &notificationTracker{}
	sm2 := alert.NewStateMachine(store, nil)
	sm2.SetNotifyOverride(tracker2.handle)

	// 重启后从数据库恢复状态 (07-monitoring.md §2.2)
	if err := sm2.RecoverFromDB(ctx); err != nil {
		t.Fatalf("RecoverFromDB failed: %v", err)
	}

	stRecovered := sm2.GetState(rule.ID, nodeID)
	if stRecovered.State != alert.StateFiring {
		t.Fatalf("expected recovered state to be firing, got %s", stRecovered.State)
	}

	// 1. 重启后第一轮评估：问题仍在，绝对不许重复告警！
	t2 := t1 + 30*1000
	resStillFiring := alert.EvalResult{
		NodeID:      nodeID,
		NodeName:    "node-restart-test",
		Breached:    true,
		CurrentVal:  1600,
		Threshold:   1000,
		Detail:      "Mem 1600",
		EvaluatedAt: t2,
	}
	_ = sm2.ProcessResult(ctx, rule, resStillFiring)
	if tracker2.count() != 0 {
		t.Fatalf("expected NO repeat notification on restart while still firing, got %d", tracker2.count())
	}

	// 2. 随后内存恢复正常 → 必须仍能发出恢复通知！
	t3 := t2 + 30*1000
	resRecovered := alert.EvalResult{
		NodeID:      nodeID,
		NodeName:    "node-restart-test",
		Breached:    false,
		CurrentVal:  500,
		Threshold:   1000,
		Detail:      "Mem 500",
		EvaluatedAt: t3,
	}
	_ = sm2.ProcessResult(ctx, rule, resRecovered)

	if tracker2.count() != 1 {
		t.Fatalf("expected recovery notification after restart, got %d", tracker2.count())
	}
	records := tracker2.getRecords()
	if records[0].Event.Type != "alert.resolved" {
		t.Fatalf("expected alert.resolved, got %s", records[0].Event.Type)
	}

	// 检查数据库记录已转 resolved
	evs, _, _ := store.ListEvents(ctx, alert.EventFilter{RuleID: rule.ID})
	if len(evs) != 1 || evs[0].EventState != alert.EventStateResolved {
		t.Fatalf("expected resolved event in db, got %+v", evs)
	}
}

// 验收 4: silence_s=3600 时连续越界一小时 → 只收 1 条，但 alert_events 里事件齐全
func TestAcceptance4_SilenceWindowSuppression(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	store := alert.NewStore(d)
	tracker := &notificationTracker{}
	sm := alert.NewStateMachine(store, nil)
	sm.SetNotifyOverride(tracker.handle)

	nodeID := ulid.New()
	_, _ = d.Exec(ctx, "INSERT INTO nodes (id, name, conn_state, created_at_ms, updated_at_ms) VALUES (?, ?, 'online', ?, ?)",
		nodeID, "node-silence-test", time.Now().UnixMilli(), time.Now().UnixMilli())

	rule := &alert.AlertRule{
		ID:         ulid.New(),
		Name:       "磁盘满告警",
		IsEnabled:  true,
		RuleKind:   alert.RuleKindMetric,
		ScopeKind:  alert.ScopeKindNode,
		ScopeRef:   nodeID,
		MetricCode: "disk_used",
		CompareOp:  alert.CompareOpGT,
		Threshold:  80,
		DurationS:  0,
		Severity:   alert.SeverityCritical,
		SilenceS:   3600, // 1 hour silence window
		ChannelIDs: []string{"ch-1"},
	}
	_ = store.CreateRule(ctx, rule)

	baseTime := time.Now().UnixMilli()

	// 模拟连续一小时（每 5 分钟评估一次，共 12 次）持续越界
	for i := 0; i < 12; i++ {
		tNow := baseTime + int64(i)*300*1000 // +0m, +5m, +10m ... +55m
		res := alert.EvalResult{
			NodeID:      nodeID,
			NodeName:    "node-silence-test",
			Breached:    true,
			CurrentVal:  85.0 + float64(i),
			Threshold:   80.0,
			Detail:      fmt.Sprintf("Disk %d%%", 85+i),
			EvaluatedAt: tNow,
		}
		if err := sm.ProcessResult(ctx, rule, res); err != nil {
			t.Fatalf("eval %d failed: %v", i, err)
		}
	}

	// 连续一小时内，只收 1 条通知！
	if tracker.count() != 1 {
		t.Fatalf("expected only 1 notification within silence_s=3600, got %d", tracker.count())
	}

	// 但 alert_events 记录齐全
	evs, total, err := store.ListEvents(ctx, alert.EventFilter{RuleID: rule.ID})
	if err != nil || total < 1 {
		t.Fatalf("expected alert_events record present, got %d", total)
	}
	if evs[0].EventState != alert.EventStateFiring {
		t.Fatalf("expected firing state, got %s", evs[0].EventState)
	}
}

// 验收 5: 拔掉 agent → offline 规则在 30~60 秒内触发
func TestAcceptance5_OfflineRuleEvaluation(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	store := alert.NewStore(d)
	evaluator := alert.NewEvaluator(store)

	nodeID := ulid.New()
	nowMs := time.Now().UnixMilli()
	// 节点 45 秒前断开心跳
	lastSeenMs := nowMs - 45*1000
	_, err := d.Exec(ctx, "INSERT INTO nodes (id, name, conn_state, last_seen_at_ms, created_at_ms, updated_at_ms) VALUES (?, ?, 'online', ?, ?, ?)",
		nodeID, "node-pull-plug", lastSeenMs, nowMs, nowMs)
	if err != nil {
		t.Fatalf("insert node failed: %v", err)
	}

	// offline 规则：离线阈值 30 秒
	rule := &alert.AlertRule{
		ID:         ulid.New(),
		Name:       "机器离线告警",
		IsEnabled:  true,
		RuleKind:   alert.RuleKindOffline,
		ScopeKind:  alert.ScopeKindNode,
		ScopeRef:   nodeID,
		CompareOp:  alert.CompareOpGT,
		Threshold:  30, // 30s threshold
		Severity:   alert.SeverityWarning,
	}

	res, err := evaluator.EvaluateNode(ctx, rule, nodeID)
	if err != nil {
		t.Fatalf("eval offline rule failed: %v", err)
	}

	if !res.Breached {
		t.Fatalf("expected offline rule to breach when offline 45s > 30s, got not breached (val=%.1f)", res.CurrentVal)
	}
}

// 验收 6: 造一个 3 天后到期的 node_billing → 收到 7 天档的 warning，不收 30 天档的重复告警
func TestAcceptance6_ExpiryReminderRules(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	store := alert.NewStore(d)
	tracker := &notificationTracker{}
	sm := alert.NewStateMachine(store, nil)
	sm.SetNotifyOverride(tracker.handle)
	evaluator := alert.NewEvaluator(store)

	nodeID := ulid.New()
	nowMs := time.Now().UnixMilli()
	_, _ = d.Exec(ctx, "INSERT INTO nodes (id, name, conn_state, created_at_ms, updated_at_ms) VALUES (?, ?, 'online', ?, ?)",
		nodeID, "node-expiry-test", nowMs, nowMs)

	// 造一个 3 天后到期的 node_billing
	expiresAtMs := nowMs + int64(3*86400*1000)
	_, err := d.Exec(ctx, `INSERT INTO node_billing (
		node_id, currency, price, cycle_days, is_auto_renew, expires_at_ms, updated_at_ms
	) VALUES (?, 'USD', 5.0, 30, 0, ?, ?)`, nodeID, expiresAtMs, nowMs)
	if err != nil {
		t.Fatalf("insert node_billing failed: %v", err)
	}

	// 30 天档规则 (已经触发并处于 firing 状态)
	rule30 := &alert.AlertRule{
		ID:               ulid.New(),
		Name:             "VPS到期提前30天提醒",
		IsEnabled:        true,
		RuleKind:         alert.RuleKindExpiry,
		ScopeKind:        alert.ScopeKindAll,
		CompareOp:        alert.CompareOpLTE,
		Threshold:        30,
		DurationS:        0,
		Severity:         alert.SeverityInfo,
		SilenceS:         86400 * 30,
		IncludeAutoRenew: false,
		ChannelIDs:       []string{"ch-1"},
	}
	_ = store.CreateRule(ctx, rule30)

	// 7 天档规则 (第一次触发)
	rule7 := &alert.AlertRule{
		ID:               ulid.New(),
		Name:             "VPS到期提前7天提醒",
		IsEnabled:        true,
		RuleKind:         alert.RuleKindExpiry,
		ScopeKind:        alert.ScopeKindAll,
		CompareOp:        alert.CompareOpLTE,
		Threshold:        7,
		DurationS:        0,
		Severity:         alert.SeverityWarning,
		SilenceS:         86400 * 7,
		IncludeAutoRenew: false,
		ChannelIDs:       []string{"ch-1"},
	}
	_ = store.CreateRule(ctx, rule7)

	// 假设 30 天档规则之前已经触发过并记录了 firing
	priorEventID := ulid.New()
	_ = store.CreateEvent(ctx, &alert.AlertEvent{
		ID:           priorEventID,
		AlertRuleID:  rule30.ID,
		NodeID:       nodeID,
		EventState:   alert.EventStateFiring,
		FiredAtMs:    nowMs - 5*86400*1000,
		NotifiedAtMs: &nowMs,
		CreatedAtMs:  nowMs - 5*86400*1000,
	})
	_ = sm.RecoverFromDB(ctx)

	// 现在进行评估
	res30, _ := evaluator.EvaluateNode(ctx, rule30, nodeID)
	_ = sm.ProcessResult(ctx, rule30, res30)

	res7, _ := evaluator.EvaluateNode(ctx, rule7, nodeID)
	_ = sm.ProcessResult(ctx, rule7, res7)

	// 检查：收到 7 天档的 warning，不收 30 天档的重复告警！
	if tracker.count() != 1 {
		t.Fatalf("expected exactly 1 notification (from 7-day rule), got %d", tracker.count())
	}
	recs := tracker.getRecords()
	if recs[0].RuleID != rule7.ID {
		t.Fatalf("expected notification from rule7, got rule %s", recs[0].RuleID)
	}
	if recs[0].Event.Payload["Severity"] != alert.SeverityWarning {
		t.Fatalf("expected warning severity, got %v", recs[0].Event.Payload["Severity"])
	}

	// 测试 is_auto_renew=1 的节点默认不提醒
	_, _ = d.Exec(ctx, "UPDATE node_billing SET is_auto_renew = 1 WHERE node_id = ?", nodeID)
	resAuto, _ := evaluator.EvaluateNode(ctx, rule7, nodeID)
	if resAuto.Breached {
		t.Fatalf("expected is_auto_renew=1 node to be skipped by default, but it breached")
	}
}

// 验收 7: ★ 重启一台机器（计数器归零）→ 不许误报流量超限
func TestAcceptance7_TrafficCounterResetNoFalsePositive(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	store := alert.NewStore(d)
	evaluator := alert.NewEvaluator(store)

	nodeID := ulid.New()
	now := time.Now().UTC()
	nowMs := now.UnixMilli()

	_, _ = d.Exec(ctx, "INSERT INTO nodes (id, name, conn_state, created_at_ms, updated_at_ms) VALUES (?, ?, 'online', ?, ?)",
		nodeID, "node-traffic-test", nowMs, nowMs)

	// 100 GB 配额，80% 报警 (80 GB)
	limitBytes := int64(100) * 1024 * 1024 * 1024
	_, err := d.Exec(ctx, `INSERT INTO node_billing (
		node_id, currency, price, cycle_days, is_auto_renew, traffic_limit, traffic_limit_kind, traffic_reset_day, updated_at_ms
	) VALUES (?, 'USD', 5.0, 30, 0, ?, 'sum', 1, ?)`, nodeID, limitBytes, nowMs)
	if err != nil {
		t.Fatalf("insert billing failed: %v", err)
	}

	rule := &alert.AlertRule{
		ID:         ulid.New(),
		Name:       "月度流量80%预警",
		IsEnabled:  true,
		RuleKind:   alert.RuleKindTraffic,
		ScopeKind:  alert.ScopeKindNode,
		ScopeRef:   nodeID,
		CompareOp:  alert.CompareOpGTE,
		Threshold:  0.8, // 80%
		Severity:   alert.SeverityWarning,
	}

	// 模拟正常采样：产生了 10GB 流量
	cycleStartMs := alert.CalculateBillingCycleStart(now, 1)
	traffic10GB := int64(5) * 1024 * 1024 * 1024
	_, _ = d.Exec(ctx, `INSERT INTO sample_host_1h (
		node_id, bucket_ms, sample_cnt, traffic_up_sum, traffic_down_sum
	) VALUES (?, ?, 60, ?, ?)`, nodeID, cycleStartMs+3600000, traffic10GB, traffic10GB)

	// 机器重启！计数器从累计 100GB 归零为 10MB
	// 在 02-database.md §5.7 下，服务端写入的增量 traffic_up_sum 依然只是正常小增量 (比如 10MB)，绝不会把旧值或大数作为增量！
	restartTrafficDelta := int64(10) * 1024 * 1024
	_, _ = d.Exec(ctx, `INSERT INTO sample_host_1h (
		node_id, bucket_ms, sample_cnt, traffic_up_sum, traffic_down_sum
	) VALUES (?, ?, 60, ?, ?)`, nodeID, cycleStartMs+7200000, restartTrafficDelta, restartTrafficDelta)

	res, err := evaluator.EvaluateNode(ctx, rule, nodeID)
	if err != nil {
		t.Fatalf("eval traffic failed: %v", err)
	}

	// 总用量仅 ~10.02GB / 100GB ≈ 10%，远低于 80% 阈值，绝对不许误报！
	if res.Breached {
		t.Fatalf("CRITICAL: counter reset caused false positive traffic alert! current ratio=%.3f, threshold=%.3f",
			res.CurrentVal, rule.Threshold)
	}
}

// 验收 8: 标签作用域：给两台机器打 proxy 标签，规则 scope=tag → 只有这两台触发
func TestAcceptance8_TagScopeFiltering(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	store := alert.NewStore(d)
	evaluator := alert.NewEvaluator(store)

	nowMs := time.Now().UnixMilli()

	// 创建 3 台机器
	node1 := ulid.New()
	node2 := ulid.New()
	node3 := ulid.New()
	for _, id := range []string{node1, node2, node3} {
		_, _ = d.Exec(ctx, "INSERT INTO nodes (id, name, conn_state, last_seen_at_ms, created_at_ms, updated_at_ms) VALUES (?, ?, 'online', ?, ?, ?)",
			id, "node-"+id[:6], nowMs-100000, nowMs, nowMs)
	}

	// 创建 tag "proxy"
	tagID := ulid.New()
	_, _ = d.Exec(ctx, "INSERT INTO tags (id, name, created_at_ms, updated_at_ms) VALUES (?, 'proxy', ?, ?)", tagID, nowMs, nowMs)

	// 只给 node1 和 node2 打 proxy 标签
	_, _ = d.Exec(ctx, "INSERT INTO node_tags (node_id, tag_id) VALUES (?, ?)", node1, tagID)
	_, _ = d.Exec(ctx, "INSERT INTO node_tags (node_id, tag_id) VALUES (?, ?)", node2, tagID)

	// 规则：作用域 scope_kind = tag, scope_ref = "proxy"
	rule := &alert.AlertRule{
		ID:         ulid.New(),
		Name:       "Proxy节点离线告警",
		IsEnabled:  true,
		RuleKind:   alert.RuleKindOffline,
		ScopeKind:  alert.ScopeKindTag,
		ScopeRef:   "proxy",
		CompareOp:  alert.CompareOpGT,
		Threshold:  30,
	}

	results, err := evaluator.EvaluateRule(ctx, rule)
	if err != nil {
		t.Fatalf("EvaluateRule failed: %v", err)
	}

	// 验证：只有这两台机器触发！node3 绝不应该在结果集中
	if len(results) != 2 {
		t.Fatalf("expected exactly 2 nodes evaluated for tag proxy, got %d", len(results))
	}

	matchedNodes := make(map[string]bool)
	for _, r := range results {
		matchedNodes[r.NodeID] = true
	}
	if !matchedNodes[node1] || !matchedNodes[node2] || matchedNodes[node3] {
		t.Fatalf("unexpected nodes matched: %+v", matchedNodes)
	}
}

// 验收 9: metric 规则读的是 _1m 表，不是 raw —— 查询语句里能看出来
func TestAcceptance9_MetricReadsRollup1mNotRaw(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	store := alert.NewStore(d)
	evaluator := alert.NewEvaluator(store)

	nodeID := ulid.New()
	nowMs := time.Now().UnixMilli()
	_, _ = d.Exec(ctx, "INSERT INTO nodes (id, name, conn_state, created_at_ms, updated_at_ms) VALUES (?, ?, 'online', ?, ?)",
		nodeID, "node-rollup-test", nowMs, nowMs)

	// 在 raw sample_host 里写入极高的瞬时尖峰 (99%)
	_, _ = d.Exec(ctx, `INSERT INTO sample_host (
		node_id, ts_ms, cpu_pct, mem_used, swap_used, load1, load5, load15, disk_used,
		net_up_bps, net_down_bps, net_total_up, net_total_down, traffic_up, traffic_down,
		proc_count, tcp_count, udp_count, uptime_s
	) VALUES (?, ?, 99.0, 100, 0, 1.0, 1.0, 1.0, 100, 0, 0, 0, 0, 0, 0, 10, 10, 10, 1000)`,
		nodeID, nowMs)

	// 在 rollup 1m (sample_host_1m) 里写入真实均值 (40%)
	_, _ = d.Exec(ctx, `INSERT INTO sample_host_1m (
		node_id, bucket_ms, sample_cnt, cpu_pct_avg
	) VALUES (?, ?, 12, 40.0)`, nodeID, nowMs-60000)

	rule := &alert.AlertRule{
		ID:         ulid.New(),
		Name:       "CPU平均负载告警",
		IsEnabled:  true,
		RuleKind:   alert.RuleKindMetric,
		ScopeKind:  alert.ScopeKindNode,
		ScopeRef:   nodeID,
		MetricCode: "cpu_pct",
		CompareOp:  alert.CompareOpGT,
		Threshold:  90.0,
	}

	res, err := evaluator.EvaluateNode(ctx, rule, nodeID)
	if err != nil {
		t.Fatalf("EvaluateNode failed: %v", err)
	}

	// 证明读的是 _1m 表的 40%，而不是 raw 的 99%！因此不会被瞬时尖峰误触发！
	if res.Breached {
		t.Fatalf("failed: evaluated raw 99%% instead of 1m rollup 40%%! currentVal=%.2f", res.CurrentVal)
	}
	if res.CurrentVal != 40.0 {
		t.Fatalf("expected current value to be 40.0 from sample_host_1m, got %.2f", res.CurrentVal)
	}
}
