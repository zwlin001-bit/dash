package billing_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"dash/internal/alert"
	"dash/internal/billing"
	"dash/internal/credentials"
	"dash/internal/db"
	"dash/internal/events"
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

	_, _ = d.Exec(ctx, "SELECT GET_LOCK('dash_billing_test', 30)")
	t.Cleanup(func() {
		_, _ = d.Exec(context.Background(), "SELECT RELEASE_LOCK('dash_billing_test')")
	})

	m := migrate.New(d, "../../migrations")
	if err := m.Up(ctx); err != nil {
		t.Fatalf("run migrations failed: %v", err)
	}

	// Clean tables related to billing, alerts, events, cloud, nodes
	tables := []string{
		"bill_budgets", "bill_items", "bill_periods",
		"alert_rule_channels", "alert_events", "alert_rules",
		"events", "node_tags", "tags", "nodes",
		"cloud_resources", "cloud_accounts", "credentials",
	}
	for _, tbl := range tables {
		_, _ = d.Exec(ctx, fmt.Sprintf("DELETE FROM %s", tbl))
	}

	return d
}

type mockBillingClient struct {
	billsByPeriod map[string]*provider.BillListResult
	calls         map[string]int
	mu            sync.Mutex
	failForPeriod map[string]error
}

func newMockBillingClient() *mockBillingClient {
	return &mockBillingClient{
		billsByPeriod: make(map[string]*provider.BillListResult),
		calls:         make(map[string]int),
		failForPeriod: make(map[string]error),
	}
}

func (m *mockBillingClient) Describe(ctx context.Context) (*provider.ProviderDescription, error) {
	return &provider.ProviderDescription{ProviderCode: "aliyun", DisplayName: "Aliyun"}, nil
}
func (m *mockBillingClient) Healthcheck(ctx context.Context, cred map[string]string, region string) (*provider.HealthcheckResult, error) {
	return &provider.HealthcheckResult{OK: true}, nil
}
func (m *mockBillingClient) ListResources(ctx context.Context, cred map[string]string, region, kind, accountSite string) ([]provider.NormalizedResource, error) {
	return nil, nil
}
func (m *mockBillingClient) GetResource(ctx context.Context, cred map[string]string, region, kind, ref string) (*provider.NormalizedResource, error) {
	return nil, nil
}
func (m *mockBillingClient) Discover(ctx context.Context, cred map[string]string, regions []string, accountSite string) ([]provider.NormalizedResource, error) {
	return nil, nil
}
func (m *mockBillingClient) GetCDTTraffic(ctx context.Context, cred map[string]string) (*provider.CDTTrafficResult, error) {
	return nil, nil
}
func (m *mockBillingClient) Action(ctx context.Context, req provider.ActionParams) (*provider.ActionResponse, error) {
	return nil, nil
}
func (m *mockBillingClient) PollJob(ctx context.Context, jobHandle string) (*provider.PollJobResponse, error) {
	return nil, nil
}
func (m *mockBillingClient) Close() error {
	return nil
}

func (m *mockBillingClient) ListBills(ctx context.Context, cred map[string]string, period, site string) (*provider.BillListResult, error) {
	m.mu.Lock()
	m.calls[period]++
	err := m.failForPeriod[period]
	res := m.billsByPeriod[period]
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if res != nil {
		return res, nil
	}
	return &provider.BillListResult{
		Period:      period,
		Currency:    "CNY",
		TotalAmount: 100.0,
		Items:       []provider.BillItem{},
	}, nil
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

// 验收 2 & 3: 回补12个月、关账月份不重复拉、未关联资源(cloud_resource_id为空)落库保留
func TestBilling_SyncBackfillAndClosedMonthSkipAndUnassociatedResources(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	masterKey := make([]byte, 32)
	credStore := credentials.NewStore(d, masterKey)
	bStore := billing.NewStore(d)

	// Create test credential
	cred, err := credStore.Create(ctx, "test-cred", "aliyun_ak", []byte(`{"access_key_id":"test_ak","access_key_secret":"test_sk"}`))
	if err != nil {
		t.Fatalf("create cred failed: %v", err)
	}

	// Create cloud account
	accID := ulid.New()
	nowMs := time.Now().UnixMilli()
	_, err = d.Exec(ctx, `INSERT INTO cloud_accounts (
		id, name, provider_code, credential_id, account_site, is_enabled, created_at_ms, updated_at_ms
	) VALUES (?, 'aliyun-test', 'aliyun', ?, 'china', 1, ?, ?)`, accID, cred.ID, nowMs, nowMs)
	if err != nil {
		t.Fatalf("insert cloud_account failed: %v", err)
	}

	// Insert a local cloud_resource to verify association
	crID := ulid.New()
	_, err = d.Exec(ctx, `INSERT INTO cloud_resources (
		id, cloud_account_id, provider_code, res_ref, res_kind, name, synced_at_ms, created_at_ms, updated_at_ms
	) VALUES (?, ?, 'aliyun', 'i-linked-vm', 'instance', 'my-vm', ?, ?, ?)`, crID, accID, nowMs, nowMs, nowMs)
	if err != nil {
		t.Fatalf("insert cloud_resource failed: %v", err)
	}

	pm := provider.NewManager(t.TempDir(), "bin", "")
	defer pm.Close()
	mockClient := newMockBillingClient()
	pm.RegisterMock("aliyun", mockClient)

	// Configure mock return data for period
	now := time.Now().UTC()
	currentPeriod := now.Format("2006-01")
	pastPeriod := now.AddDate(0, -1, 0).Format("2006-01")

	mockClient.billsByPeriod[pastPeriod] = &provider.BillListResult{
		Period:      pastPeriod,
		Currency:    "CNY",
		TotalAmount: 250.0,
		Items: []provider.BillItem{
			{
				ResKind:     "instance",
				ResRef:      "i-linked-vm",
				ItemName:    "ecs.t6",
				ProductCode: "ecs",
				Amount:      200.0,
			},
			{
				ResKind:     "bandwidth",
				ResRef:      "eip-unlinked",
				ItemName:    "eip package",
				ProductCode: "eip",
				Amount:      50.0,
			},
		},
	}

	svc := billing.NewService(d, bStore, credStore, pm, nil)

	// First sync: 2 months backfill
	if err := svc.SyncAccount(ctx, accID, 2); err != nil {
		t.Fatalf("first sync failed: %v", err)
	}

	// Verify pastPeriod calls
	if mockClient.calls[pastPeriod] != 1 {
		t.Fatalf("expected pastPeriod to be called once, got %d", mockClient.calls[pastPeriod])
	}

	// Check items in DB
	items, err := bStore.ListItems(ctx, pastPeriod, accID)
	if err != nil {
		t.Fatalf("list items failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	var linkedItem, unlinkedItem *billing.BillItem
	for i := range items {
		it := &items[i]
		if it.ResRef != nil && *it.ResRef == "i-linked-vm" {
			linkedItem = it
		}
		if it.ResRef != nil && *it.ResRef == "eip-unlinked" {
			unlinkedItem = it
		}
	}

	// 验收 3: linkedItem has cloud_resource_id; unlinkedItem has cloud_resource_id == nil
	if linkedItem == nil || linkedItem.CloudResourceID == nil || *linkedItem.CloudResourceID != crID {
		t.Fatalf("linked item not correctly linked to cloud_resource_id: %+v", linkedItem)
	}
	if unlinkedItem == nil || unlinkedItem.CloudResourceID != nil {
		t.Fatalf("unlinked item must have nil cloud_resource_id: %+v", unlinkedItem)
	}

	// Second sync: 2 months backfill
	// 验收 2: 已关账的历史月份只拉一次（sync_state='ok' 且 period < 当月即跳过）
	if err := svc.SyncAccount(ctx, accID, 2); err != nil {
		t.Fatalf("second sync failed: %v", err)
	}

	// pastPeriod call count should STILL be 1! (skipped)
	if mockClient.calls[pastPeriod] != 1 {
		t.Fatalf("expected pastPeriod not to be re-fetched on second sync, call count=%d", mockClient.calls[pastPeriod])
	}
	// currentPeriod should have been fetched again (count = 2)
	if mockClient.calls[currentPeriod] != 2 {
		t.Fatalf("expected currentPeriod to be re-fetched (count 2), call count=%d", mockClient.calls[currentPeriod])
	}
}

// 验收 4: 跨账号不同币种分组显示，不折算
func TestBilling_MultiCurrencyOverview(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	bStore := billing.NewStore(d)
	svc := billing.NewService(d, bStore, nil, nil, nil)

	acc1 := ulid.New()
	acc2 := ulid.New()
	nowMs := time.Now().UnixMilli()
	currentPeriod := time.Now().UTC().Format("2006-01")

	// Account 1 has CNY
	_ = bStore.UpsertPeriod(ctx, &billing.BillPeriod{
		CloudAccountID: acc1,
		Period:         currentPeriod,
		Currency:       "CNY",
		TotalAmount:    1234.56,
		SyncState:      "ok",
		SyncedAtMs:     &nowMs,
	})

	// Account 2 has USD
	_ = bStore.UpsertPeriod(ctx, &billing.BillPeriod{
		CloudAccountID: acc2,
		Period:         currentPeriod,
		Currency:       "USD",
		TotalAmount:    78.90,
		SyncState:      "ok",
		SyncedAtMs:     &nowMs,
	})

	overview, err := svc.GetOverview(ctx, currentPeriod)
	if err != nil {
		t.Fatalf("get overview failed: %v", err)
	}

	if len(overview.Totals) != 2 {
		t.Fatalf("expected 2 currency totals, got %d", len(overview.Totals))
	}

	totalsMap := make(map[string]float64)
	for _, tot := range overview.Totals {
		totalsMap[tot.Currency] = tot.TotalAmount
	}

	if totalsMap["CNY"] != 1234.56 {
		t.Fatalf("expected CNY 1234.56, got %f", totalsMap["CNY"])
	}
	if totalsMap["USD"] != 78.90 {
		t.Fatalf("expected USD 78.90, got %f", totalsMap["USD"])
	}
}

// 验收 8: 按标签汇总金额
func TestBilling_ItemsByTagAggregation(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	bStore := billing.NewStore(d)
	svc := billing.NewService(d, bStore, nil, nil, nil)

	nowMs := time.Now().UnixMilli()
	currentPeriod := time.Now().UTC().Format("2006-01")
	accID := ulid.New()

	// Create nodes
	node1 := ulid.New()
	node2 := ulid.New()
	if _, err := d.Exec(ctx, "INSERT INTO nodes (id, name, created_at_ms, updated_at_ms) VALUES (?, 'node-1', ?, ?)", node1, nowMs, nowMs); err != nil {
		t.Fatalf("insert node1 failed: %v", err)
	}
	if _, err := d.Exec(ctx, "INSERT INTO nodes (id, name, created_at_ms, updated_at_ms) VALUES (?, 'node-2', ?, ?)", node2, nowMs, nowMs); err != nil {
		t.Fatalf("insert node2 failed: %v", err)
	}

	// Create tag "proxy" and associate both nodes
	tagID := ulid.New()
	if _, err := d.Exec(ctx, "INSERT INTO tags (id, name, color, created_at_ms, updated_at_ms) VALUES (?, 'proxy', '#fff', ?, ?)", tagID, nowMs, nowMs); err != nil {
		t.Fatalf("insert tag failed: %v", err)
	}
	if _, err := d.Exec(ctx, "INSERT INTO node_tags (node_id, tag_id) VALUES (?, ?)", node1, tagID); err != nil {
		t.Fatalf("insert node_tags1 failed: %v", err)
	}
	if _, err := d.Exec(ctx, "INSERT INTO node_tags (node_id, tag_id) VALUES (?, ?)", node2, tagID); err != nil {
		t.Fatalf("insert node_tags2 failed: %v", err)
	}

	// Create cloud_resources
	cr1 := ulid.New()
	cr2 := ulid.New()
	if _, err := d.Exec(ctx, "INSERT INTO cloud_resources (id, cloud_account_id, provider_code, node_id, res_ref, res_kind, synced_at_ms, created_at_ms, updated_at_ms) VALUES (?, ?, 'aliyun', ?, 'i-1', 'instance', ?, ?, ?)", cr1, accID, node1, nowMs, nowMs, nowMs); err != nil {
		t.Fatalf("insert cr1 failed: %v", err)
	}
	if _, err := d.Exec(ctx, "INSERT INTO cloud_resources (id, cloud_account_id, provider_code, node_id, res_ref, res_kind, synced_at_ms, created_at_ms, updated_at_ms) VALUES (?, ?, 'aliyun', ?, 'i-2', 'instance', ?, ?, ?)", cr2, accID, node2, nowMs, nowMs, nowMs); err != nil {
		t.Fatalf("insert cr2 failed: %v", err)
	}

	// Insert bill items: node1 costs 30, node2 costs 45
	_ = bStore.ReplaceItems(ctx, accID, currentPeriod, []billing.BillItem{
		{
			CloudAccountID:  accID,
			Period:          currentPeriod,
			ResKind:         "instance",
			CloudResourceID: &cr1,
			Currency:        "CNY",
			Amount:          30.0,
		},
		{
			CloudAccountID:  accID,
			Period:          currentPeriod,
			ResKind:         "instance",
			CloudResourceID: &cr2,
			Currency:        "CNY",
			Amount:          45.0,
		},
	})

	// Query items grouped by tag
	tagResp, err := svc.GetItems(ctx, currentPeriod, "tag", "")
	if err != nil {
		t.Fatalf("get items by tag failed: %v", err)
	}

	var proxyTotal float64
	foundProxy := false
	for _, grp := range tagResp.Groups {
		if grp.Key == "proxy" {
			foundProxy = true
			proxyTotal = grp.TotalAmount
		}
	}

	if !foundProxy {
		t.Fatalf("group proxy not found in tag response: %+v", tagResp.Groups)
	}
	if proxyTotal != 75.0 {
		t.Fatalf("expected proxy tag total to be 75.0, got %f", proxyTotal)
	}
}

// 验收 5 & 6: 预算 warning(85%) & exceeded(105%), silence window 去重
func TestBilling_BudgetWarningExceededAndSilence(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	alertStore := alert.NewStore(d)
	bStore := billing.NewStore(d)

	tracker := &notificationTracker{}
	sm := alert.NewStateMachine(alertStore, nil)
	sm.SetNotifyOverride(tracker.handle)

	// Sync built-in rules (includes budget rule)
	if err := alertStore.SyncBuiltinRules(ctx); err != nil {
		t.Fatalf("sync builtin rules failed: %v", err)
	}

	rules, err := alertStore.ListRules(ctx)
	if err != nil {
		t.Fatalf("list rules failed: %v", err)
	}
	var budgetRule *alert.AlertRule
	for _, r := range rules {
		if r.RuleKind == alert.RuleKindBudget {
			budgetRule = r
			break
		}
	}
	if budgetRule == nil {
		t.Fatalf("budget rule not found in builtin rules")
	}

	// Configure budget rule: silence_s = 3600, duration_s = 0, channel mock
	budgetRule.SilenceS = 3600
	budgetRule.DurationS = 0
	budgetRule.ChannelIDs = []string{"chan-1"}
	_ = alertStore.UpdateRule(ctx, budgetRule)

	// Create budget: 100 CNY, warn at 80%
	budgetID := ulid.New()
	err = bStore.CreateBudget(ctx, &billing.BillBudget{
		ID:        budgetID,
		ScopeKind: "all",
		Currency:  "CNY",
		Amount:    100.0,
		WarnRatio: 0.8,
		IsEnabled: true,
	})
	if err != nil {
		t.Fatalf("create budget failed: %v", err)
	}

	currentPeriod := time.Now().UTC().Format("2006-01")
	nowMs := time.Now().UnixMilli()

	// 1. Spend 85 CNY (85%) -> Expect billing.budget_warning
	accID := ulid.New()
	_ = bStore.UpsertPeriod(ctx, &billing.BillPeriod{
		CloudAccountID: accID,
		Period:         currentPeriod,
		Currency:       "CNY",
		TotalAmount:    85.0,
		SyncState:      "ok",
		SyncedAtMs:     &nowMs,
	})

	evaluator := alert.NewEvaluator(alertStore)

	resList, err := evaluator.EvaluateRule(ctx, budgetRule)
	if err != nil {
		t.Fatalf("evaluate rule failed: %v", err)
	}
	if len(resList) != 1 || !resList[0].Breached {
		t.Fatalf("expected breached result at 85%%, got %+v", resList)
	}

	if err := sm.ProcessResult(ctx, budgetRule, resList[0]); err != nil {
		t.Fatalf("process result failed: %v", err)
	}

	recs := tracker.getRecords()
	var warningEv *events.Event
	for _, rec := range recs {
		if rec.Event.Type == "billing.budget_warning" {
			cp := rec.Event
			warningEv = &cp
			break
		}
	}
	if warningEv == nil {
		t.Fatalf("expected billing.budget_warning event, got: %+v", recs)
	}

	// 验收 6: 连续几小时超预算，按 silence_s 去重，不重复发通知
	// Evaluate again 10 minutes later (still 85 CNY)
	resList2, _ := evaluator.EvaluateRule(ctx, budgetRule)
	resList2[0].EvaluatedAt = nowMs + 10*60*1000
	if err := sm.ProcessResult(ctx, budgetRule, resList2[0]); err != nil {
		t.Fatalf("process result 2 failed: %v", err)
	}
	// Verify count of warning events did not increase
	recsAfter10m := tracker.getRecords()
	warningCount := 0
	for _, rec := range recsAfter10m {
		if rec.Event.Type == "billing.budget_warning" {
			warningCount++
		}
	}
	if warningCount != 1 {
		t.Fatalf("expected silence window to suppress repeat notification, got count %d", warningCount)
	}

	// 2. Spend 105 CNY (105%) -> Expect billing.budget_exceeded
	// Simulate silence window expired (2 hours later)
	_ = bStore.UpsertPeriod(ctx, &billing.BillPeriod{
		CloudAccountID: accID,
		Period:         currentPeriod,
		Currency:       "CNY",
		TotalAmount:    105.0,
		SyncState:      "ok",
		SyncedAtMs:     &nowMs,
	})

	resList3, _ := evaluator.EvaluateRule(ctx, budgetRule)
	resList3[0].EvaluatedAt = nowMs + 2*3600*1000
	if err := sm.ProcessResult(ctx, budgetRule, resList3[0]); err != nil {
		t.Fatalf("process result 3 failed: %v", err)
	}

	recsAfterExceeded := tracker.getRecords()
	var exceededEv *events.Event
	for _, rec := range recsAfterExceeded {
		if rec.Event.Type == "billing.budget_exceeded" {
			cp := rec.Event
			exceededEv = &cp
			break
		}
	}
	if exceededEv == nil {
		t.Fatalf("expected billing.budget_exceeded event, got: %+v", recsAfterExceeded)
	}
}

// 验收 7: 去掉 BSS 权限 -> billing.sync_failed 记录下来，error_text 里无 AK/SK 明文，且不阻塞其他账号
func TestBilling_BSSFailureIsolationAndNoSecretInError(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	masterKey := make([]byte, 32)
	credStore := credentials.NewStore(d, masterKey)
	bStore := billing.NewStore(d)

	// Account 1: BSS fails with sensitive AK/SK error message
	secretKey := "my_super_secret_access_key_12345"
	cred1, err := credStore.Create(ctx, "cred-1", "aliyun_ak", []byte(`{"access_key_id":"test_ak","access_key_secret":"`+secretKey+`"}`))
	if err != nil {
		t.Fatalf("create cred1 failed: %v", err)
	}

	acc1ID := ulid.New()
	nowMs := time.Now().UnixMilli()
	_, _ = d.Exec(ctx, "INSERT INTO cloud_accounts (id, name, provider_code, credential_id, is_enabled, created_at_ms, updated_at_ms) VALUES (?, 'acc-fail', 'aliyun', ?, 1, ?, ?)", acc1ID, cred1.ID, nowMs, nowMs)

	// Account 2: Success
	cred2, err := credStore.Create(ctx, "cred-2", "aliyun_ak", []byte(`{"access_key_id":"test_ak2","access_key_secret":"test_sk2"}`))
	if err != nil {
		t.Fatalf("create cred2 failed: %v", err)
	}
	acc2ID := ulid.New()
	_, _ = d.Exec(ctx, "INSERT INTO cloud_accounts (id, name, provider_code, credential_id, is_enabled, created_at_ms, updated_at_ms) VALUES (?, 'acc-success', 'aliyun', ?, 1, ?, ?)", acc2ID, cred2.ID, nowMs, nowMs)

	pm := provider.NewManager(t.TempDir(), "bin", "")
	defer pm.Close()
	mockClient := newMockBillingClient()
	pm.RegisterMock("aliyun", mockClient)

	currentPeriod := time.Now().UTC().Format("2006-01")
	mockClient.failForPeriod[currentPeriod] = errors.New("Aliyun BSS Unauthorized: access_key=" + secretKey)

	svc := billing.NewService(d, bStore, credStore, pm, nil)

	// Sync account 1 fails
	err = svc.SyncAccount(ctx, acc1ID, 1)
	if err == nil {
		t.Fatalf("expected sync account 1 to fail")
	}

	// Verify NO secret key in error text (redacted!)
	p, _ := bStore.GetPeriod(ctx, acc1ID, currentPeriod)
	if p == nil || p.ErrorText == nil {
		t.Fatalf("expected error_text on period")
	}
	if p.ErrorText != nil && (len(*p.ErrorText) == 0 || strings.Contains(*p.ErrorText, secretKey)) {
		t.Fatalf("error text must not contain secret key: %s", *p.ErrorText)
	}

	// Account 2 succeeds when failure is removed for its period
	mockClient.failForPeriod = make(map[string]error)
	if err := svc.SyncAccount(ctx, acc2ID, 1); err != nil {
		t.Fatalf("sync account 2 should succeed even if account 1 failed: %v", err)
	}
}
