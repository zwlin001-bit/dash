package cloudmetric_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dash/internal/cloudmetric"
	"dash/internal/credentials"
	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/ingest"
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

	tables := []string{"cloud_samples", "guard_rules", "cloud_resources", "cloud_accounts", "credentials", "events", "audit_log", "sample_host", "sample_dim"}
	for _, tbl := range tables {
		_, _ = d.Exec(ctx, fmt.Sprintf("DELETE FROM %s", tbl))
	}

	return d
}

func TestStoreSaveAndQuery(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	store := cloudmetric.NewStore(d)

	accID := ulid.New()
	resRef := "i-test"

	// 1. Save points
	pts := []provider.MetricPoint{
		{TsMs: 1700000000000, Value: 12.5},
		{TsMs: 1700000300000, Value: 18.0},
		{TsMs: 1700000600000, Value: 15.0},
	}
	if err := store.SavePoints(ctx, accID, resRef, "cpu_pct", pts); err != nil {
		t.Fatalf("SavePoints failed: %v", err)
	}

	// 2. Verify GetLastTs
	lastTs, err := store.GetLastTs(ctx, accID, resRef, "cpu_pct")
	if err != nil {
		t.Fatalf("GetLastTs failed: %v", err)
	}
	if lastTs != 1700000600000 {
		t.Fatalf("expected lastTs 1700000600000, got %d", lastTs)
	}

	// 3. Save another metric
	netPts := []provider.MetricPoint{
		{TsMs: 1700000300000, Value: 500000},
	}
	if err := store.SavePoints(ctx, accID, resRef, "net_up_bps", netPts); err != nil {
		t.Fatalf("SavePoints net_up_bps failed: %v", err)
	}

	// 4. Query series
	resp, err := store.QuerySeries(ctx, accID, resRef, []string{"cpu_pct", "net_up_bps"}, 1700000000000, 1700000600000)
	if err != nil {
		t.Fatalf("QuerySeries failed: %v", err)
	}
	if resp.Source != "cloud_samples" {
		t.Fatalf("expected source cloud_samples, got %s", resp.Source)
	}
	if len(resp.TsMs) != 3 {
		t.Fatalf("expected 3 timestamps, got %d", len(resp.TsMs))
	}

	// Net up bps at index 0 (1700000000000) should be nil (null in json)
	cpuArr := resp.Series["cpu_pct"]
	netArr := resp.Series["net_up_bps"]
	if cpuArr[0] == nil || *cpuArr[0] != 12.5 {
		t.Fatalf("expected cpu_pct[0] = 12.5, got %v", cpuArr[0])
	}
	if netArr[0] != nil {
		t.Fatalf("expected net_up_bps[0] to be nil (missing data), got %v", netArr[0])
	}
	if netArr[1] == nil || *netArr[1] != 500000 {
		t.Fatalf("expected net_up_bps[1] = 500000, got %v", netArr[1])
	}
}

func TestSyncerTargetSelectionAndIncremental(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	masterKey := make([]byte, 32)
	_, _ = rand.Read(masterKey)
	credStore := credentials.NewStore(d, masterKey)
	pm := provider.NewManager(t.TempDir())

	// Create Credential
	akJSON, _ := json.Marshal(map[string]string{
		"access_key_id":     "ak123",
		"access_key_secret": "sk123",
	})
	credSummary, err := credStore.Create(ctx, "test-cred", "aliyun_ak", akJSON)
	if err != nil {
		t.Fatalf("create cred failed: %v", err)
	}

	nowMs := time.Now().UnixMilli()

	// Create Account
	accID := ulid.New()
	_, err = d.Exec(ctx, `INSERT INTO cloud_accounts (id, provider_code, name, credential_id, default_region, is_enabled, created_at_ms, updated_at_ms)
		VALUES (?, 'aliyun', 'acc-test', ?, 'cn-hangzhou', 1, ?, ?)`, accID, credSummary.ID, nowMs, nowMs)
	if err != nil {
		t.Fatalf("insert cloud_account failed: %v", err)
	}

	// Create Resource 1 (Guarded)
	res1ID := ulid.New()
	_, err = d.Exec(ctx, `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'aliyun', 'instance', 'i-guarded1', 'vm-1', 'cn-hangzhou', 'running', ?, 0, ?, ?)`, res1ID, accID, nowMs, nowMs, nowMs)
	if err != nil {
		t.Fatalf("insert guarded res failed: %v", err)
	}

	// Create Guard Rule for Resource 1
	rule1ID := ulid.New()
	_, err = d.Exec(ctx, `INSERT INTO guard_rules (id, cloud_resource_id, is_enabled, actions_enabled, traffic_action, schedule_tz, created_at_ms, updated_at_ms)
		VALUES (?, ?, 1, 0, 'stop', 'Asia/Shanghai', ?, ?)`, rule1ID, res1ID, nowMs, nowMs)
	if err != nil {
		t.Fatalf("insert guard_rule failed: %v", err)
	}

	// Create Resource 2 (Unguarded)
	res2ID := ulid.New()
	_, err = d.Exec(ctx, `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'aliyun', 'instance', 'i-unguarded2', 'vm-2', 'cn-hangzhou', 'running', ?, 0, ?, ?)`, res2ID, accID, nowMs, nowMs, nowMs)
	if err != nil {
		t.Fatalf("insert unguarded res failed: %v", err)
	}

	// Mock ProviderClient
	queriedRefs := make(map[string]int)
	queriedStartTimes := make(map[string]int64)

	mockClient := &provider.MockClient{
		ListMetricsFunc: func(ctx context.Context, params provider.MetricListParams) (*provider.MetricListResult, error) {
			key := fmt.Sprintf("%s:%s", params.ResRef, params.MetricCode)
			queriedRefs[key]++
			queriedStartTimes[key] = params.StartTimeMs

			if params.ResRef == "_account" {
				return &provider.MetricListResult{
					ResRef:     "_account",
					MetricCode: "traffic_month_up",
					Points: []provider.MetricPoint{
						{TsMs: nowMs - 1000, Value: 53687091200}, // 50 GB
					},
				}, nil
			}

			return &provider.MetricListResult{
				ResRef:     params.ResRef,
				MetricCode: params.MetricCode,
				Points: []provider.MetricPoint{
					{TsMs: nowMs - 300000, Value: 25.0},
					{TsMs: nowMs - 1000, Value: 30.0},
				},
			}, nil
		},
	}

	pm.RegisterMock("aliyun", mockClient)
	store := cloudmetric.NewStore(d)
	syncer := cloudmetric.NewSyncer(d, store, credStore, pm)

	// --- ROUND 1 ---
	report1, err := syncer.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce round 1 failed: %v", err)
	}
	if report1.SyncedAccounts != 1 {
		t.Fatalf("expected 1 synced account, got %d", report1.SyncedAccounts)
	}
	if report1.SyncedInstances != 1 {
		t.Fatalf("expected 1 synced instance, got %d", report1.SyncedInstances)
	}

	// Criterion 5: Verify unguarded instance was NEVER queried
	for k := range queriedRefs {
		if k == "i-unguarded2:cpu_pct" || k == "i-unguarded2:net_up_bps" {
			t.Fatalf("unguarded instance %s should not have been queried!", k)
		}
	}

	// Verify guarded instance was queried for cpu_pct, net_up_bps, net_down_bps
	if queriedRefs["i-guarded1:cpu_pct"] != 1 {
		t.Fatalf("expected i-guarded1:cpu_pct queried once, got %d", queriedRefs["i-guarded1:cpu_pct"])
	}
	if queriedRefs["_account:traffic_month_up"] != 1 {
		t.Fatalf("expected _account:traffic_month_up queried once, got %d", queriedRefs["_account:traffic_month_up"])
	}

	// --- ROUND 2 ---
	// In round 2, the lastTs is (nowMs - 1000). So start time must be (nowMs - 1000 + 1).
	time.Sleep(10 * time.Millisecond)
	report2, err := syncer.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce round 2 failed: %v", err)
	}
	_ = report2

	// Criterion 4: Verify round 2 requested incremental start time
	expectedMinStart := nowMs - 1000 + 1
	if queriedStartTimes["i-guarded1:cpu_pct"] < expectedMinStart {
		t.Fatalf("expected incremental startTimeMs >= %d, got %d", expectedMinStart, queriedStartTimes["i-guarded1:cpu_pct"])
	}
}

func TestSyncerFailureIsolation(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	eventsStore := events.NewStore(d, 100)
	eventsStore.Start()
	events.SetDefaultStore(eventsStore)
	defer eventsStore.Stop()

	masterKey := make([]byte, 32)
	_, _ = rand.Read(masterKey)
	credStore := credentials.NewStore(d, masterKey)
	pm := provider.NewManager(t.TempDir())

	akJSON, _ := json.Marshal(map[string]string{"access_key_id": "ak", "access_key_secret": "sk"})
	cred, _ := credStore.Create(ctx, "cred", "aliyun_ak", akJSON)

	nowMs := time.Now().UnixMilli()
	accID := ulid.New()
	_, _ = d.Exec(ctx, `INSERT INTO cloud_accounts (id, provider_code, name, credential_id, default_region, is_enabled, created_at_ms, updated_at_ms)
		VALUES (?, 'aliyun', 'acc-fail', ?, 'cn-hangzhou', 1, ?, ?)`, accID, cred.ID, nowMs, nowMs)

	resID := ulid.New()
	_, _ = d.Exec(ctx, `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'aliyun', 'instance', 'i-fail', 'vm-fail', 'cn-hangzhou', 'running', ?, 0, ?, ?)`, resID, accID, nowMs, nowMs, nowMs)

	ruleID := ulid.New()
	_, _ = d.Exec(ctx, `INSERT INTO guard_rules (id, cloud_resource_id, is_enabled, actions_enabled, traffic_action, schedule_tz, created_at_ms, updated_at_ms)
		VALUES (?, ?, 1, 0, 'stop', 'Asia/Shanghai', ?, ?)`, ruleID, resID, nowMs, nowMs)

	// Mock provider simulates 403 Forbidden / NoPermission on CloudMonitor
	mockClient := &provider.MockClient{
		ListMetricsFunc: func(ctx context.Context, params provider.MetricListParams) (*provider.MetricListResult, error) {
			if params.ResRef == "i-fail" {
				return nil, errors.New("Aliyun CMS 403: NoPermission to access DescribeMetricList")
			}
			return &provider.MetricListResult{ResRef: params.ResRef, MetricCode: params.MetricCode}, nil
		},
	}
	pm.RegisterMock("aliyun", mockClient)

	store := cloudmetric.NewStore(d)
	syncer := cloudmetric.NewSyncer(d, store, credStore, pm)

	// Criterion 6: Sync does not abort or fail fatally
	report, err := syncer.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce should not return fatal error on CMS failure: %v", err)
	}
	if report.ErrorsCount == 0 {
		t.Fatalf("expected errors to be counted in report")
	}

	// Flush and verify event was generated
	time.Sleep(200 * time.Millisecond)
	var eventCount int
	_ = d.QueryRow(ctx, "SELECT COUNT(*) FROM events WHERE event_type = 'cloud.metric.sync_error'").Scan(&eventCount)
	if eventCount == 0 {
		t.Fatalf("expected cloud.metric.sync_error event to be emitted")
	}
}

func TestRetentionCloudSamples(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	store := cloudmetric.NewStore(d)

	accID := ulid.New()
	resRef := "i-retention"

	now := time.Now().UnixMilli()
	ms90DaysAgo := now - 90*86400*1000
	msOld := ms90DaysAgo - 2*3600*1000 // 90 days + 2 hours ago (2 chunk iterations)
	msNew := ms90DaysAgo + 2*3600*1000 // 90 days - 2 hours ago (within retention)

	_ = store.SavePoints(ctx, accID, resRef, "cpu_pct", []provider.MetricPoint{
		{TsMs: msOld, Value: 10.0},
		{TsMs: msNew, Value: 20.0},
	})

	retService := ingest.NewRetentionService(d)
	retService.SetSleepInterval(0)
	retService.SetTargets([]ingest.TableRetentionTarget{
		{TableName: "cloud_samples", TimeCol: "ts_ms", SettingKey: "retention.cloud_metrics_days", DefaultDays: 90},
	})

	// Criterion 8: Run retention cleanup for now
	if err := retService.Clean(ctx, now); err != nil {
		t.Fatalf("retention Clean failed: %v", err)
	}

	// Verify 95 days old point is deleted, 10 days old point remains
	var countOld int
	_ = d.QueryRow(ctx, "SELECT COUNT(*) FROM cloud_samples WHERE ts_ms < ?", now-90*86400*1000).Scan(&countOld)
	if countOld != 0 {
		t.Fatalf("expected 0 points older than 90 days, found %d", countOld)
	}

	var countNew int
	_ = d.QueryRow(ctx, "SELECT COUNT(*) FROM cloud_samples WHERE ts_ms >= ?", now-90*86400*1000).Scan(&countNew)
	if countNew != 1 {
		t.Fatalf("expected 1 point within 90 days, found %d", countNew)
	}
}

func TestHandlerEndpoints(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx := context.Background()
	masterKey := make([]byte, 32)
	_, _ = rand.Read(masterKey)
	credStore := credentials.NewStore(d, masterKey)
	pm := provider.NewManager(t.TempDir())

	store := cloudmetric.NewStore(d)
	syncer := cloudmetric.NewSyncer(d, store, credStore, pm)
	handler := cloudmetric.NewHandler(d, store, syncer)

	now := time.Now().UnixMilli()
	accID := ulid.New()
	_, _ = d.Exec(ctx, `INSERT INTO cloud_accounts (id, provider_code, name, credential_id, is_enabled, created_at_ms, updated_at_ms)
		VALUES (?, 'aliyun', 'handler-acc', 'cred-1', 1, ?, ?)`, accID, now, now)

	resID := ulid.New()
	_, _ = d.Exec(ctx, `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'aliyun', 'instance', 'i-handler', 'vm-handler', 'cn-hangzhou', 'running', ?, 0, ?, ?)`, resID, accID, now, now, now)

	_ = store.SavePoints(ctx, accID, "i-handler", "cpu_pct", []provider.MetricPoint{
		{TsMs: now - 60000, Value: 35.5},
	})
	_ = store.SavePoints(ctx, accID, "_account", "traffic_month_up", []provider.MetricPoint{
		{TsMs: now - 60000, Value: 10737418240}, // 10GB
	})

	// 1. GET /api/v1/cloud-resources/{id}/metrics
	req := httptest.NewRequest("GET", "/api/v1/cloud-resources/"+resID+"/metrics", nil)
	req.SetPathValue("id", resID)
	w := httptest.NewRecorder()
	handler.HandleResourceMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("HandleResourceMetrics status = %d, body: %s", w.Code, w.Body.String())
	}
	var resResp cloudmetric.MetricQueryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resResp); err != nil {
		t.Fatalf("unmarshal resource metrics resp: %v", err)
	}
	if resResp.ResRef != "i-handler" {
		t.Fatalf("expected res_ref i-handler, got %s", resResp.ResRef)
	}
	if len(resResp.TsMs) != 1 {
		t.Fatalf("expected 1 timestamp, got %d", len(resResp.TsMs))
	}

	// 2. GET /api/v1/cloud-metrics/accounts/{id}
	reqAcc := httptest.NewRequest("GET", "/api/v1/cloud-metrics/accounts/"+accID, nil)
	reqAcc.SetPathValue("id", accID)
	wAcc := httptest.NewRecorder()
	handler.HandleAccountMetrics(wAcc, reqAcc)

	if wAcc.Code != http.StatusOK {
		t.Fatalf("HandleAccountMetrics status = %d, body: %s", wAcc.Code, wAcc.Body.String())
	}
	var accResp cloudmetric.MetricQueryResponse
	if err := json.Unmarshal(wAcc.Body.Bytes(), &accResp); err != nil {
		t.Fatalf("unmarshal account metrics resp: %v", err)
	}
	if accResp.ResRef != "_account" {
		t.Fatalf("expected res_ref _account, got %s", accResp.ResRef)
	}
	if len(accResp.TsMs) != 1 {
		t.Fatalf("expected 1 timestamp, got %d", len(accResp.TsMs))
	}
}
