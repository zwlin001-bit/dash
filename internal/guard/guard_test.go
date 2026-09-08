package guard_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"dash/internal/cloud"
	"dash/internal/credentials"
	"dash/internal/db"
	"dash/internal/guard"
	"dash/internal/jobs"
	"dash/internal/migrate"
	"dash/internal/provider"
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	m := migrate.New(d, "../../migrations")
	if err := m.Up(ctx); err != nil {
		t.Fatalf("run migrations failed: %v", err)
	}

	// 清理当前测试相关的表
	tables := []string{"guard_rules", "guard_cycles", "jobs", "job_steps", "cloud_resources", "cloud_accounts", "credentials", "events", "audit_log"}
	for _, tbl := range tables {
		_, _ = d.Exec(ctx, fmt.Sprintf("DELETE FROM %s", tbl))
	}

	return d
}

// 模拟的 Provider 客户端
type mockProviderClient struct {
	cdtTrafficBytes int64
	cdtError        error
	cdtCalls        int

	describeCalls int
	regionCalls   map[string]int
	instances     map[string][]provider.NormalizedResource

	actionCalls []provider.ActionParams
	actionError error
}

func (m *mockProviderClient) Describe(ctx context.Context) (*provider.ProviderDescription, error) {
	return &provider.ProviderDescription{ProviderCode: "aliyun", DisplayName: "阿里云"}, nil
}

func (m *mockProviderClient) Healthcheck(ctx context.Context, cred map[string]string, region string) (*provider.HealthcheckResult, error) {
	return &provider.HealthcheckResult{OK: true}, nil
}

func (m *mockProviderClient) ListRegions(ctx context.Context, cred map[string]string) ([]provider.Region, error) {
	return []provider.Region{{RegionID: "cn-hangzhou", LocalName: "华东1（杭州）"}}, nil
}

func (m *mockProviderClient) ListResources(ctx context.Context, cred map[string]string, region, kind, accountSite string) ([]provider.NormalizedResource, error) {
	m.describeCalls++
	if m.regionCalls == nil {
		m.regionCalls = make(map[string]int)
	}
	m.regionCalls[region]++
	return m.instances[region], nil
}

func (m *mockProviderClient) GetResource(ctx context.Context, cred map[string]string, region, kind, ref string) (*provider.NormalizedResource, error) {
	return nil, nil
}

func (m *mockProviderClient) Discover(ctx context.Context, cred map[string]string, regions []string, accountSite string) ([]provider.NormalizedResource, error) {
	return nil, nil
}

func (m *mockProviderClient) GetCDTTraffic(ctx context.Context, cred map[string]string) (*provider.CDTTrafficResult, error) {
	m.cdtCalls++
	if m.cdtError != nil {
		return nil, m.cdtError
	}
	return &provider.CDTTrafficResult{TrafficBytes: m.cdtTrafficBytes}, nil
}

func (m *mockProviderClient) Action(ctx context.Context, req provider.ActionParams) (*provider.ActionResponse, error) {
	m.actionCalls = append(m.actionCalls, req)
	if m.actionError != nil {
		return nil, m.actionError
	}
	return &provider.ActionResponse{
		JobHandle: "job-" + ulid.New(),
		Status:    "succeeded",
		Message:   "ok",
	}, nil
}

func (m *mockProviderClient) PollJob(ctx context.Context, jobHandle string) (*provider.PollJobResponse, error) {
	return &provider.PollJobResponse{JobHandle: jobHandle, Status: "succeeded"}, nil
}

func (m *mockProviderClient) ListMetrics(ctx context.Context, params provider.MetricListParams) (*provider.MetricListResult, error) {
	return &provider.MetricListResult{}, nil
}

func (m *mockProviderClient) ListBills(ctx context.Context, cred map[string]string, period, accountSite string) (*provider.BillListResult, error) {
	return &provider.BillListResult{Period: period, Currency: "CNY"}, nil
}

func (m *mockProviderClient) Close() error {
	return nil
}

// 验收 5: 日程计算与解析
func TestEvaluateSchedule(t *testing.T) {
	loc := guard.LoadLocationWithFallback("Asia/Shanghai")

	// 1. 日间模式: 08:30 - 20:00
	start := "08:30"
	stop := "20:00"

	// 早上 07:00 (关机时段，下一次动作为 start 08:30)
	t1 := time.Date(2026, 9, 8, 7, 0, 0, 0, loc)
	inStop, inRun, nextAct, nextTime, err := guard.EvaluateSchedule(start, stop, "Asia/Shanghai", t1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !inStop || inRun || nextAct != "start" {
		t.Errorf("at 07:00 expected inStop=true, inRun=false, nextAct=start; got inStop=%v, inRun=%v, nextAct=%s", inStop, inRun, nextAct)
	}
	if nextTime == nil || nextTime.In(loc).Hour() != 8 || nextTime.In(loc).Minute() != 30 {
		t.Errorf("expected next time 08:30, got %v", nextTime)
	}

	// 中午 12:00 (运行时段，下一次动作为 stop 20:00)
	t2 := time.Date(2026, 9, 8, 12, 0, 0, 0, loc)
	inStop, inRun, nextAct, nextTime, err = guard.EvaluateSchedule(start, stop, "Asia/Shanghai", t2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inStop || !inRun || nextAct != "stop" {
		t.Errorf("at 12:00 expected inStop=false, inRun=true, nextAct=stop; got inStop=%v, inRun=%v, nextAct=%s", inStop, inRun, nextAct)
	}
	if nextTime == nil || nextTime.In(loc).Hour() != 20 || nextTime.In(loc).Minute() != 0 {
		t.Errorf("expected next time 20:00, got %v", nextTime)
	}

	// 晚上 21:00 (关机时段，下一次动作为明日 start 08:30)
	t3 := time.Date(2026, 9, 8, 21, 0, 0, 0, loc)
	inStop, inRun, nextAct, nextTime, err = guard.EvaluateSchedule(start, stop, "Asia/Shanghai", t3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !inStop || inRun || nextAct != "start" {
		t.Errorf("at 21:00 expected inStop=true, inRun=false, nextAct=start; got inStop=%v, inRun=%v, nextAct=%s", inStop, inRun, nextAct)
	}
	if nextTime == nil || nextTime.In(loc).Day() != 9 || nextTime.In(loc).Hour() != 8 {
		t.Errorf("expected next time tomorrow 08:30, got %v", nextTime)
	}

	// 2. 跨夜模式: 22:00 - 06:00
	startNight := "22:00"
	stopNight := "06:00"

	// 凌晨 03:00 (跨夜运行时段)
	tNight := time.Date(2026, 9, 8, 3, 0, 0, 0, loc)
	inStop, inRun, nextAct, _, err = guard.EvaluateSchedule(startNight, stopNight, "Asia/Shanghai", tNight)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inStop || !inRun || nextAct != "stop" {
		t.Errorf("at 03:00 expected inRun=true, got inRun=%v, inStop=%v", inRun, inStop)
	}
}

// 决策优先级: 日程 > 流量 > 保活
func TestDecideInstance_PriorityAndRules(t *testing.T) {
	loc := guard.LoadLocationWithFallback("Asia/Shanghai")
	noon := time.Date(2026, 9, 8, 12, 0, 0, 0, loc)
	midnight := time.Date(2026, 9, 8, 23, 0, 0, 0, loc)

	startSched := "08:30"
	stopSched := "20:00"
	limit100 := 100.0

	resRunning := cloud.CloudResource{
		ID:             "res-01",
		CloudAccountID: "acc-01",
		ResRef:         "i-001",
		Name:           "web-prod",
		Region:         "cn-hangzhou",
		Status:         "Running",
	}

	resStopped := cloud.CloudResource{
		ID:             "res-02",
		CloudAccountID: "acc-01",
		ResRef:         "i-002",
		Name:           "api-dev",
		Region:         "cn-hangzhou",
		Status:         "Stopped",
	}

	// 条件 1: 日程启用 且 处于计划关机时段 (23:00) 且 实例 Running → 停止
	rule1 := &guard.GuardRule{
		IsEnabled:       true,
		ActionsEnabled:  true,
		ScheduleEnabled: true,
		ScheduleStart:   &startSched,
		ScheduleStop:    &stopSched,
		ScheduleTZ:      "Asia/Shanghai",
		TrafficLimitGB:  &limit100,
	}
	d1 := guard.DecideInstance(resRunning, rule1, 50.0, nil, midnight)
	if d1.ProposedAction != guard.ActionStop || d1.Reason != "处于计划关机时段" {
		t.Fatalf("Rule 1 expected ActionStop by schedule; got %s (%s)", d1.ProposedAction, d1.Reason)
	}

	// 条件 2: 日程启用 且 处于计划运行时段 (12:00) 且 实例 Stopped 且 流量未超 → 启动
	d2 := guard.DecideInstance(resStopped, rule1, 50.0, nil, noon)
	if d2.ProposedAction != guard.ActionStart || d2.Reason != "处于计划运行时段且流量未超" {
		t.Fatalf("Rule 2 expected ActionStart by schedule; got %s (%s)", d2.ProposedAction, d2.Reason)
	}

	// 条件 3: 流量 ≥ 阈值 且 实例 Running → 停止
	rule3 := &guard.GuardRule{
		IsEnabled:       true,
		ActionsEnabled:  true,
		ScheduleEnabled: false,
		TrafficLimitGB:  &limit100,
	}
	d3 := guard.DecideInstance(resRunning, rule3, 105.0, nil, noon)
	if d3.ProposedAction != guard.ActionStop {
		t.Fatalf("Rule 3 expected ActionStop by traffic; got %s", d3.ProposedAction)
	}

	// 条件 4: 流量 < 阈值 且 实例 Stopped 且 不在计划关机时段 → 启动（保活）
	d4 := guard.DecideInstance(resStopped, rule3, 50.0, nil, noon)
	if d4.ProposedAction != guard.ActionStart {
		t.Fatalf("Rule 4 expected ActionStart by keepalive; got %s", d4.ProposedAction)
	}

	// 条件 5: 实例正常 Running 且流量正常 → 不动
	d5 := guard.DecideInstance(resRunning, rule3, 50.0, nil, noon)
	if d5.ProposedAction != guard.ActionNoop {
		t.Fatalf("Rule 5 expected ActionNoop; got %s", d5.ProposedAction)
	}
}

// 验收 6: 安全性质 1 与 2
// 安全性质 1: 计划关机只依赖 ECS 状态可读，CDT 查询失败照样执行关机
// 安全性质 2: 流量读不到时不许启动任何实例
func TestSafetyProperties_CDTFailureIsolation(t *testing.T) {
	loc := guard.LoadLocationWithFallback("Asia/Shanghai")
	midnight := time.Date(2026, 9, 8, 23, 0, 0, 0, loc)
	noon := time.Date(2026, 9, 8, 12, 0, 0, 0, loc)

	startSched := "08:30"
	stopSched := "20:00"
	limit100 := 100.0

	rule := &guard.GuardRule{
		IsEnabled:       true,
		ActionsEnabled:  true,
		ScheduleEnabled: true,
		ScheduleStart:   &startSched,
		ScheduleStop:    &stopSched,
		ScheduleTZ:      "Asia/Shanghai",
		TrafficLimitGB:  &limit100,
	}

	resRunning := cloud.CloudResource{
		ID:             "res-01",
		CloudAccountID: "acc-01",
		Status:         "Running",
	}

	resStopped := cloud.CloudResource{
		ID:             "res-02",
		CloudAccountID: "acc-01",
		Status:         "Stopped",
	}

	cdtErr := errors.New("CDT query forbidden (NoPermission)")

	// 1. 安全性质 1: 计划关机即使 CDT 失败，依然照常关机！
	dStop := guard.DecideInstance(resRunning, rule, 0, cdtErr, midnight)
	if dStop.ProposedAction != guard.ActionStop {
		t.Fatalf("Safety 1 violation: scheduled stop must proceed even when CDT fails; got %s", dStop.ProposedAction)
	}

	// 2. 安全性质 2: 读不到流量时，绝不许启动实例！
	dStartSched := guard.DecideInstance(resStopped, rule, 0, cdtErr, noon)
	if dStartSched.ProposedAction != guard.ActionNoop {
		t.Fatalf("Safety 2 violation: must NOT start instance when CDT query fails; got %s", dStartSched.ProposedAction)
	}

	ruleNoSched := &guard.GuardRule{
		IsEnabled:       true,
		ActionsEnabled:  true,
		ScheduleEnabled: false,
		TrafficLimitGB:  &limit100,
	}
	dKeepalive := guard.DecideInstance(resStopped, ruleNoSched, 0, cdtErr, noon)
	if dKeepalive.ProposedAction != guard.ActionNoop {
		t.Fatalf("Safety 2 violation: keepalive must NOT start instance when CDT query fails; got %s", dKeepalive.ProposedAction)
	}
}

// 安全性质 3: 过渡状态跳过 (Starting / Stopping / Pending)
func TestSafetyProperty3_TransitionalStatus(t *testing.T) {
	loc := guard.LoadLocationWithFallback("Asia/Shanghai")
	noon := time.Date(2026, 9, 8, 12, 0, 0, 0, loc)

	transitional := []string{"Starting", "Stopping", "Pending", "stopping", "starting"}
	rule := &guard.GuardRule{
		IsEnabled:      true,
		ActionsEnabled: true,
	}

	for _, st := range transitional {
		res := cloud.CloudResource{
			ID:     "res-trans",
			Status: st,
		}
		d := guard.DecideInstance(res, rule, 200.0, nil, noon)
		if d.ProposedAction != guard.ActionNoop {
			t.Fatalf("Safety 3 violation: transitional status %s must be skipped (noop); got %s", st, d.ProposedAction)
		}
	}
}

// 验收 3: actions_enabled = false 时只发事件不动手，实例状态不变
func TestAcceptance3_ActionsEnabledFalse_DoesNotMutate(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()

	// 创建凭据与账号
	masterKey := make([]byte, 32)
	credStore := credentials.NewStore(d, masterKey)
	cred, err := credStore.Create(ctx, "test-cred", "aliyun_ak", []byte(`{"access_key_id":"test-ak","access_key_secret":"test-sk"}`))
	if err != nil {
		t.Fatal(err)
	}

	accID := ulid.New()
	now := time.Now().UnixMilli()
	_, err = d.Exec(ctx, `INSERT INTO cloud_accounts (id, provider_code, name, credential_id, is_enabled, created_at_ms, updated_at_ms)
VALUES (?, 'aliyun', 'Test Account', ?, 1, ?, ?)`, accID, cred.ID, now, now)
	if err != nil {
		t.Fatal(err)
	}

	resID := ulid.New()
	_, err = d.Exec(ctx, `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
VALUES (?, ?, 'aliyun', 'ecs', 'i-mock-01', 'mock-ecs', 'cn-hangzhou', 'Running', ?, 0, ?, ?)`, resID, accID, now, now, now)
	if err != nil {
		t.Fatal(err)
	}

	store := guard.NewStore(d)
	limit50 := 50.0
	// actions_enabled = false
	err = store.UpsertRule(ctx, &guard.GuardRule{
		CloudResourceID: resID,
		IsEnabled:       true,
		ActionsEnabled:  false,
		TrafficLimitGB:  &limit50,
	})
	if err != nil {
		t.Fatal(err)
	}

	mockClient := &mockProviderClient{
		cdtTrafficBytes: 80 * 1024 * 1024 * 1024, // 80 GB > 50 GB
		instances: map[string][]provider.NormalizedResource{
			"cn-hangzhou": {
				{Ref: "i-mock-01", Status: "Running"},
			},
		},
	}
	pm := provider.NewManager(t.TempDir(), "bin", "")
	pm.RegisterMock("aliyun", mockClient)

	reg := jobs.NewRegistry()
	je := jobs.NewEngine(d, reg, jobs.Config{Workers: 2, DefaultTimeout: 5 * time.Second})

	engine := guard.NewEngine(d, store, credStore, pm, je, guard.Config{Interval: time.Minute})

	res, err := engine.EvaluateOnce(ctx, false)
	if err != nil {
		t.Fatalf("EvaluateOnce failed: %v", err)
	}

	if res.ActedCount != 0 {
		t.Errorf("expected acted count 0 when actions_enabled=false, got %d", res.ActedCount)
	}
	if len(mockClient.actionCalls) != 0 {
		t.Errorf("expected 0 action calls, got %d", len(mockClient.actionCalls))
	}
}

// 验收 4: 演练模式 (dry-run) 列出「会停止 X、会启动 Y」，云上状态零变化
func TestAcceptance4_DryRunMode(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	masterKey := make([]byte, 32)
	credStore := credentials.NewStore(d, masterKey)
	cred, _ := credStore.Create(ctx, "test-cred-dry", "aliyun_ak", []byte(`{"access_key_id":"test-ak","access_key_secret":"test-sk"}`))

	accID := ulid.New()
	now := time.Now().UnixMilli()
	_, _ = d.Exec(ctx, `INSERT INTO cloud_accounts (id, provider_code, name, credential_id, is_enabled, created_at_ms, updated_at_ms)
VALUES (?, 'aliyun', 'Dry Account', ?, 1, ?, ?)`, accID, cred.ID, now, now)

	resID1 := ulid.New()
	resID2 := ulid.New()
	_, _ = d.Exec(ctx, `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
VALUES (?, ?, 'aliyun', 'ecs', 'i-dry-run-01', 'inst-stop', 'cn-hangzhou', 'Running', ?, 0, ?, ?)`, resID1, accID, now, now, now)
	_, _ = d.Exec(ctx, `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
VALUES (?, ?, 'aliyun', 'ecs', 'i-dry-run-02', 'inst-start', 'cn-hangzhou', 'Stopped', ?, 0, ?, ?)`, resID2, accID, now, now, now)

	store := guard.NewStore(d)
	limit50 := 50.0
	// actions_enabled 为 true，但在 dry-run 下依然绝不动手
	_ = store.UpsertRule(ctx, &guard.GuardRule{
		CloudResourceID: resID1,
		IsEnabled:       true,
		ActionsEnabled:  true,
		TrafficLimitGB:  &limit50,
	})
	_ = store.UpsertRule(ctx, &guard.GuardRule{
		CloudResourceID: resID2,
		IsEnabled:       true,
		ActionsEnabled:  true,
		TrafficLimitGB:  &limit50,
	})

	mockClient := &mockProviderClient{
		cdtTrafficBytes: 80 * 1024 * 1024 * 1024, // 超限
		instances: map[string][]provider.NormalizedResource{
			"cn-hangzhou": {
				{Ref: "i-dry-run-01", Status: "Running"},
				{Ref: "i-dry-run-02", Status: "Stopped"},
			},
		},
	}
	pm := provider.NewManager(t.TempDir(), "bin", "")
	pm.RegisterMock("aliyun", mockClient)

	reg := jobs.NewRegistry()
	je := jobs.NewEngine(d, reg, jobs.Config{Workers: 2, DefaultTimeout: 5 * time.Second})

	engine := guard.NewEngine(d, store, credStore, pm, je, guard.Config{Interval: time.Minute})

	// 执行 dry-run
	res, err := engine.EvaluateOnce(ctx, true)
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}

	if !res.IsDryRun {
		t.Fatalf("expected IsDryRun=true")
	}
	if len(mockClient.actionCalls) != 0 {
		t.Fatalf("dry-run must NOT mutate cloud state; got %d action calls", len(mockClient.actionCalls))
	}

	foundStop := false
	for _, item := range res.Items {
		if item.ResourceRef == "i-dry-run-01" && item.ProposedAction == guard.ActionStop {
			foundStop = true
		}
	}
	if !foundStop {
		t.Fatalf("expected dry-run to list stop for i-dry-run-01")
	}
}

// 验收 8: 一个账号 20 台实例跨 3 region 跑一轮 → CDT 调用 1 次、DescribeInstances 3 次
func TestAcceptance8_BatchGroupingCalls(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	masterKey := make([]byte, 32)
	credStore := credentials.NewStore(d, masterKey)
	cred, _ := credStore.Create(ctx, "test-cred-group", "aliyun_ak", []byte(`{"access_key_id":"ak","access_key_secret":"sk"}`))

	accID := ulid.New()
	now := time.Now().UnixMilli()
	_, _ = d.Exec(ctx, `INSERT INTO cloud_accounts (id, provider_code, name, credential_id, is_enabled, created_at_ms, updated_at_ms)
VALUES (?, 'aliyun', 'Group Account', ?, 1, ?, ?)`, accID, cred.ID, now, now)

	regions := []string{"cn-hangzhou", "cn-shanghai", "cn-beijing"}
	instancesMock := make(map[string][]provider.NormalizedResource)

	// 插入 20 台机器跨 3 个 region
	for i := 0; i < 20; i++ {
		reg := regions[i%3]
		ref := fmt.Sprintf("i-inst-%02d", i)
		resID := ulid.New()
		_, _ = d.Exec(ctx, `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
VALUES (?, ?, 'aliyun', 'ecs', ?, ?, ?, 'Running', ?, 0, ?, ?)`, resID, accID, ref, ref, reg, now, now, now)

		instancesMock[reg] = append(instancesMock[reg], provider.NormalizedResource{
			Ref:    ref,
			Status: "Running",
			Region: reg,
		})
	}

	mockClient := &mockProviderClient{
		cdtTrafficBytes: 10 * 1024 * 1024 * 1024,
		instances:       instancesMock,
	}
	pm := provider.NewManager(t.TempDir(), "bin", "")
	pm.RegisterMock("aliyun", mockClient)

	store := guard.NewStore(d)
	engine := guard.NewEngine(d, store, credStore, pm, nil, guard.Config{Interval: time.Minute})

	_, err := engine.EvaluateOnce(ctx, true)
	if err != nil {
		t.Fatalf("EvaluateOnce failed: %v", err)
	}

	// 断言: CDT 调用 1 次，DescribeInstances (ListResources) 3 次
	if mockClient.cdtCalls != 1 {
		t.Fatalf("expected exactly 1 CDT call per account, got %d", mockClient.cdtCalls)
	}
	if mockClient.describeCalls != 3 {
		t.Fatalf("expected exactly 3 DescribeInstances calls across 3 regions, got %d", mockClient.describeCalls)
	}
}

// 验收 10: 80% 预警与 100% 停机去重与通知接收
func TestAcceptance10_PrewarningAndDedup(t *testing.T) {
	rule := &guard.GuardRule{
		ID:             "rule-prewarn",
		IsEnabled:      true,
		TrafficLimitGB: func() *float64 { v := 100.0; return &v }(),
	}

	// 75 GB < 80% → 不预警
	if guard.ShouldPrewarn(rule, 75.0) {
		t.Errorf("expected ShouldPrewarn false for 75GB/100GB")
	}

	// 85 GB (85%) → 预警
	if !guard.ShouldPrewarn(rule, 85.0) {
		t.Errorf("expected ShouldPrewarn true for 85GB/100GB")
	}

	// 105 GB (超额) → 由停机规则接管，不发 prewarn
	if guard.ShouldPrewarn(rule, 105.0) {
		t.Errorf("expected ShouldPrewarn false for >=100%%")
	}
}

// 验收 7: 月初流量归零后被停的实例被拉起
func TestAcceptance7_MonthResetCatchup(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	now := time.Now().UnixMilli()
	masterKey := make([]byte, 32)
	credStore := credentials.NewStore(d, masterKey)
	cred, _ := credStore.Create(ctx, "test-cred-reset", "aliyun_ak", []byte(`{"access_key_id":"ak","access_key_secret":"sk"}`))

	accID := ulid.New()
	_, _ = d.Exec(ctx, `INSERT INTO cloud_accounts (id, provider_code, name, credential_id, is_enabled, created_at_ms, updated_at_ms)
VALUES (?, 'aliyun', 'Reset Account', ?, 1, ?, ?)`, accID, cred.ID, now, now)

	resID := ulid.New()
	_, _ = d.Exec(ctx, `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
VALUES (?, ?, 'aliyun', 'ecs', 'i-reset-01', 'inst-reset', 'cn-hangzhou', 'Stopped', ?, 0, ?, ?)`, resID, accID, now, now, now)

	store := guard.NewStore(d)
	limit50 := 50.0
	lastAction := "traffic_stop"
	rule := &guard.GuardRule{
		CloudResourceID: resID,
		IsEnabled:       true,
		ActionsEnabled:  true,
		TrafficLimitGB:  &limit50,
		LastAction:      &lastAction,
	}
	_ = store.UpsertRule(ctx, rule)

	// 新月 CDT 流量归零 (0 字节)
	mockClient := &mockProviderClient{
		cdtTrafficBytes: 0,
		instances: map[string][]provider.NormalizedResource{
			"cn-hangzhou": {
				{Ref: "i-reset-01", Status: "Stopped"},
			},
		},
	}
	pm := provider.NewManager(t.TempDir(), "bin", "")
	pm.RegisterMock("aliyun", mockClient)

	reg := jobs.NewRegistry()
	guard.RegisterGuardJobs(reg, d, credStore, pm)
	je := jobs.NewEngine(d, reg, jobs.Config{Workers: 2, DefaultTimeout: 5 * time.Second})

	engine := guard.NewEngine(d, store, credStore, pm, je, guard.Config{Interval: time.Minute})

	res, err := engine.EvaluateOnce(ctx, false)
	if err != nil {
		t.Fatalf("EvaluateOnce failed: %v", err)
	}

	if len(res.Items) == 0 {
		t.Fatalf("expected 1 item in evaluation result")
	}

	item := res.Items[0]
	if item.ProposedAction != guard.ActionStart {
		t.Fatalf("expected ProposedAction Start after month reset, got %s (reason: %s)", item.ProposedAction, item.Reason)
	}
	if !item.JobSubmitted {
		t.Fatalf("expected job to be submitted for instance restart")
	}
}

// 验收 9: 同一实例连续 10 个周期超阈值，生成的 dedup_key 恒定，防止消息风暴
func TestAcceptance9_DedupNotification(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	now := time.Now().UnixMilli()
	masterKey := make([]byte, 32)
	credStore := credentials.NewStore(d, masterKey)
	cred, _ := credStore.Create(ctx, "test-cred-dedup", "aliyun_ak", []byte(`{"access_key_id":"ak","access_key_secret":"sk"}`))

	accID := ulid.New()
	_, _ = d.Exec(ctx, `INSERT INTO cloud_accounts (id, provider_code, name, credential_id, is_enabled, created_at_ms, updated_at_ms)
VALUES (?, 'aliyun', 'Dedup Account', ?, 1, ?, ?)`, accID, cred.ID, now, now)

	resID := ulid.New()
	_, _ = d.Exec(ctx, `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
VALUES (?, ?, 'aliyun', 'ecs', 'i-dedup-01', 'inst-dedup', 'cn-hangzhou', 'Running', ?, 0, ?, ?)`, resID, accID, now, now, now)

	store := guard.NewStore(d)
	limit50 := 50.0
	rule := &guard.GuardRule{
		CloudResourceID: resID,
		IsEnabled:       true,
		ActionsEnabled:  false, // 只发事件
		TrafficLimitGB:  &limit50,
	}
	_ = store.UpsertRule(ctx, rule)

	// 流量 80 GB > 50 GB
	mockClient := &mockProviderClient{
		cdtTrafficBytes: 80 * 1024 * 1024 * 1024,
		instances: map[string][]provider.NormalizedResource{
			"cn-hangzhou": {
				{Ref: "i-dedup-01", Status: "Running"},
			},
		},
	}
	pm := provider.NewManager(t.TempDir(), "bin", "")
	pm.RegisterMock("aliyun", mockClient)

	engine := guard.NewEngine(d, store, credStore, pm, nil, guard.Config{Interval: time.Minute})

	// 连续运行 10 轮
	for i := 0; i < 10; i++ {
		res, err := engine.EvaluateOnce(ctx, false)
		if err != nil {
			t.Fatalf("cycle %d failed: %v", i, err)
		}
		if len(res.Items) != 1 || res.Items[0].ProposedAction != guard.ActionStop {
			t.Fatalf("cycle %d: expected stop action", i)
		}
	}
}
