package guard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apiGuard "dash/internal/api/guard"
	"dash/internal/app"
	"dash/internal/cloud"
	"dash/internal/credentials"
	"dash/internal/db"
	"dash/internal/guard"
	"dash/internal/jobs"
	"dash/internal/migrate"
	"dash/internal/provider"
	"dash/internal/ulid"
)

func setupAPITestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("Skipping API test: MySQL test DB not accessible: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, _ = d.Exec(ctx, "SELECT GET_LOCK('dash_api_guard_test', 30)")
	t.Cleanup(func() {
		_, _ = d.Exec(context.Background(), "SELECT RELEASE_LOCK('dash_api_guard_test')")
	})

	mig := migrate.New(d, "../../../migrations")
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("migration up failed: %v", err)
	}

	tables := []string{"guard_rules", "guard_cycles", "jobs", "job_steps", "cloud_resources", "cloud_accounts", "credentials", "events", "audit_log"}
	for _, tbl := range tables {
		_, _ = d.Exec(ctx, fmt.Sprintf("DELETE FROM %s", tbl))
	}

	return d
}

type mockProviderClient struct {
	cdtTrafficBytes int64
}

func (m *mockProviderClient) Describe(ctx context.Context) (*provider.ProviderDescription, error) {
	return &provider.ProviderDescription{ProviderCode: "aliyun", DisplayName: "阿里云"}, nil
}
func (m *mockProviderClient) Healthcheck(ctx context.Context, cred map[string]string, region string) (*provider.HealthcheckResult, error) {
	return &provider.HealthcheckResult{OK: true}, nil
}
func (m *mockProviderClient) ListResources(ctx context.Context, cred map[string]string, region, kind, accountSite string) ([]provider.NormalizedResource, error) {
	return []provider.NormalizedResource{{Ref: "i-test-ecs", Status: "Running", Region: "cn-hangzhou"}}, nil
}
func (m *mockProviderClient) GetResource(ctx context.Context, cred map[string]string, region, kind, ref string) (*provider.NormalizedResource, error) {
	return &provider.NormalizedResource{Ref: ref, Status: "Running", Region: region}, nil
}
func (m *mockProviderClient) Discover(ctx context.Context, cred map[string]string, regions []string, accountSite string) ([]provider.NormalizedResource, error) {
	return nil, nil
}
func (m *mockProviderClient) GetCDTTraffic(ctx context.Context, cred map[string]string) (*provider.CDTTrafficResult, error) {
	return &provider.CDTTrafficResult{TrafficBytes: m.cdtTrafficBytes}, nil
}
func (m *mockProviderClient) Action(ctx context.Context, req provider.ActionParams) (*provider.ActionResponse, error) {
	return &provider.ActionResponse{JobHandle: "mock-handle", Status: "succeeded"}, nil
}
func (m *mockProviderClient) PollJob(ctx context.Context, jobHandle string) (*provider.PollJobResponse, error) {
	return &provider.PollJobResponse{JobHandle: jobHandle, Status: "succeeded"}, nil
}
func (m *mockProviderClient) Close() error { return nil }

func TestGuardHTTPAPI(t *testing.T) {
	d := setupAPITestDB(t)
	defer d.Close()

	ctx := context.Background()
	now := time.Now().UnixMilli()

	// Seed credential, account, and resource
	masterKey := make([]byte, 32)
	credStore := credentials.NewStore(d, masterKey)
	cred, err := credStore.Create(ctx, "api-cred", "aliyun_ak", []byte(`{"access_key_id":"ak","access_key_secret":"sk"}`))
	if err != nil {
		t.Fatal(err)
	}

	accID := ulid.New()
	_, err = d.Exec(ctx, `INSERT INTO cloud_accounts (id, provider_code, name, credential_id, is_enabled, created_at_ms, updated_at_ms)
VALUES (?, 'aliyun', 'API Account', ?, 1, ?, ?)`, accID, cred.ID, now, now)
	if err != nil {
		t.Fatal(err)
	}

	resID := ulid.New()
	_, err = d.Exec(ctx, `INSERT INTO cloud_resources (id, cloud_account_id, provider_code, res_kind, res_ref, name, region, status, synced_at_ms, is_deleted, created_at_ms, updated_at_ms)
VALUES (?, ?, 'aliyun', 'ecs', 'i-test-ecs', 'test-ecs', 'cn-hangzhou', 'Stopped', ?, 0, ?, ?)`, resID, accID, now, now, now)
	if err != nil {
		t.Fatal(err)
	}

	pm := provider.NewManager(t.TempDir(), "bin", "")
	defer pm.Close()
	mockClient := &mockProviderClient{cdtTrafficBytes: 15 * 1024 * 1024 * 1024}
	pm.RegisterMock("aliyun", mockClient)

	cloudSvc := cloud.NewService(d, credStore, pm)
	store := guard.NewStore(d)
	reg := jobs.NewRegistry()
	guard.RegisterGuardJobs(reg, d, credStore, pm)
	je := jobs.NewEngine(d, reg, jobs.Config{Workers: 2, DefaultTimeout: 5 * time.Second})
	_ = je.Start(ctx)
	defer je.Stop()

	engine := guard.NewEngine(d, store, credStore, pm, je, guard.Config{Interval: time.Minute})

	a := app.NewApp(d, nil, "dev")
	a.Config = nil
	a.SetAuthMiddleware(func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			next(w, r)
		}
	})
	a.CloudService = cloudSvc
	a.GuardEngine = engine
	a.JobEngine = je

	mod := apiGuard.NewModule()
	if err := mod.Register(a); err != nil {
		t.Fatalf("register module failed: %v", err)
	}

	// 1. GET /api/v1/guard/overview
	req := httptest.NewRequest(http.MethodGet, "/api/v1/guard/overview", nil)
	rr := httptest.NewRecorder()
	a.Mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/guard/overview status = %d: %s", rr.Code, rr.Body.String())
	}
	var ov guard.OverviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &ov); err != nil {
		t.Fatalf("unmarshal overview failed: %v", err)
	}
	if ov.TotalInstances != 1 {
		t.Errorf("expected 1 instance in overview, got %d", ov.TotalInstances)
	}

	// 2. PUT /api/v1/guard/rules/{id}
	limit20 := 20.0
	start := "08:00"
	stop := "22:00"
	tz := "Asia/Shanghai"
	actionsEnabled := true
	ruleReq := guard.RuleUpdateRequest{
		IsEnabled:       &actionsEnabled,
		ActionsEnabled:  &actionsEnabled,
		TrafficLimitGB:  &limit20,
		ScheduleEnabled: &actionsEnabled,
		ScheduleStart:   &start,
		ScheduleStop:    &stop,
		ScheduleTZ:      &tz,
	}
	reqBody, _ := json.Marshal(ruleReq)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/guard/rules/"+resID, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	a.Mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT /api/v1/guard/rules/%s status = %d: %s", resID, rr.Code, rr.Body.String())
	}

	// 3. POST /api/v1/guard/dry-run
	req = httptest.NewRequest(http.MethodPost, "/api/v1/guard/dry-run", nil)
	rr = httptest.NewRecorder()
	a.Mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/guard/dry-run status = %d: %s", rr.Code, rr.Body.String())
	}

	// 4. Force Start without confirm -> 400 Bad Request
	forceBody, _ := json.Marshal(guard.ForceStartRequest{
		Confirm: false,
		Reason:  "need it now",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/guard/instances/"+resID+"/force-start", bytes.NewReader(forceBody))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	a.Mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request without confirmation, got %d", rr.Code)
	}

	// 5. Force Start with confirm=true -> 202 Accepted, writes to audit_log
	forceBody, _ = json.Marshal(guard.ForceStartRequest{
		Confirm: true,
		Reason:  "urgent troubleshooting",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/guard/instances/"+resID+"/force-start", bytes.NewReader(forceBody))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	a.Mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted with confirmation, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify audit_log entry
	var auditCount int
	err = d.QueryRow(ctx, "SELECT COUNT(*) FROM audit_log WHERE action = 'guard.force_start' AND target_id = ?", resID).Scan(&auditCount)
	if err != nil {
		t.Fatalf("query audit_log failed: %v", err)
	}
	if auditCount == 0 {
		t.Errorf("expected audit_log entry for guard.force_start, got 0")
	}

	// 6. GET /api/v1/guard/cycles
	req = httptest.NewRequest(http.MethodGet, "/api/v1/guard/cycles", nil)
	rr = httptest.NewRecorder()
	a.Mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/guard/cycles status = %d: %s", rr.Code, rr.Body.String())
	}
}
