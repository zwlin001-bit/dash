package cloud_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"dash/internal/cloud"
	"dash/internal/credentials"
	"dash/internal/db"
	"dash/internal/provider"
	"dash/internal/ulid"
)

func getTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("Skipping test: cannot connect to local MySQL on 33306: %v", err)
	}
	return d
}

func TestCloudService(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	masterKey := make([]byte, 32)
	_, _ = rand.Read(masterKey)

	credStore := credentials.NewStore(d, masterKey)
	pm := provider.NewManager(t.TempDir())

	// Create test credential
	akJSON, _ := json.Marshal(map[string]string{
		"access_key_id":     "LTAI5testCloudAK",
		"access_key_secret": "secretCloudSK",
	})
	credSummary, err := credStore.Create(ctx, "cred-"+ulid.New(), "aliyun_ak", akJSON)
	if err != nil {
		t.Fatalf("create test cred failed: %v", err)
	}

	// Register mock provider
	var trafficCalls int
	var discoverCalls int
	var actionCalled string

	mock := &provider.MockClient{
		DescribeFunc: func(ctx context.Context) (*provider.ProviderDescription, error) {
			return &provider.ProviderDescription{ProviderCode: "aliyun", DisplayName: "阿里云"}, nil
		},
		GetCDTTrafficFunc: func(ctx context.Context, cred map[string]string) (*provider.CDTTrafficResult, error) {
			trafficCalls++
			return &provider.CDTTrafficResult{TrafficBytes: 12345678}, nil
		},
		DiscoverFunc: func(ctx context.Context, cred map[string]string, regions []string, accountSite string) ([]provider.NormalizedResource, error) {
			discoverCalls++
			return []provider.NormalizedResource{
				{
					ProviderCode: "aliyun",
					Kind:         "instance",
					Ref:          "i-discovered01",
					Name:         "test-ecs-01",
					Region:       "cn-hongkong",
					Status:       "running",
					PublicIPs:    []string{"1.2.3.4"},
					PrivateIPs:   []string{"172.16.0.2"},
					Specs:        provider.ResourceSpecs{VCPU: 2, MemMB: 2048},
					Attrs:        map[string]any{"os_type": "linux"},
				},
			}, nil
		},
		ActionFunc: func(ctx context.Context, req provider.ActionParams) (*provider.ActionResponse, error) {
			actionCalled = req.Action + ":" + req.Ref
			return &provider.ActionResponse{JobHandle: "job-100", Status: "succeeded"}, nil
		},
	}
	pm.RegisterMock("aliyun", mock)

	svc := cloud.NewService(d, credStore, pm)
	if err := svc.EnsureBuiltinProviders(ctx); err != nil {
		t.Fatalf("EnsureBuiltinProviders failed: %v", err)
	}

	// 1. Create account
	acc, err := svc.CreateAccount(ctx, cloud.CloudAccount{
		ProviderCode:  "aliyun",
		Name:          "aliyun-acc-" + ulid.New(),
		CredentialID:  credSummary.ID,
		DefaultRegion: "cn-hongkong",
		AccountSite:   "china",
	})
	if err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}
	if acc.ID == "" {
		t.Fatal("expected non-empty account ID")
	}

	// 2. List accounts
	accounts, err := svc.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts failed: %v", err)
	}
	if len(accounts) == 0 {
		t.Fatal("expected at least 1 account")
	}

	// 3. Auto-discover
	discovered, err := svc.DiscoverAccount(ctx, credSummary.ID, []string{"cn-hongkong"}, "china")
	if err != nil {
		t.Fatalf("DiscoverAccount failed: %v", err)
	}
	if len(discovered) != 1 || discovered[0].Ref != "i-discovered01" {
		t.Fatalf("unexpected discovered resources: %+v", discovered)
	}

	// 4. Trigger Sync
	jobID, err := svc.TriggerSync(ctx, acc.ID)
	if err != nil {
		t.Fatalf("TriggerSync failed: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected non-empty job ID")
	}

	// Wait for async sync to complete (max 3 seconds)
	deadline := time.Now().Add(3 * time.Second)
	var finalStatus *cloud.SyncJobStatus
	for time.Now().Before(deadline) {
		st, err := svc.GetSyncStatus(jobID)
		if err == nil && (st.State == "succeeded" || st.State == "failed") {
			finalStatus = st
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if finalStatus == nil || finalStatus.State != "succeeded" {
		t.Fatalf("expected sync job to succeed, got: %+v", finalStatus)
	}
	if finalStatus.SyncedCount != 1 || finalStatus.TrafficBytes != 12345678 {
		t.Fatalf("unexpected sync result: %+v", finalStatus)
	}

	// 5. Verify resources in database
	resources, err := svc.ListResources(ctx, acc.ID, "", "", "", "")
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource in db, got %d", len(resources))
	}
	res := resources[0]
	if res.ResRef != "i-discovered01" || res.Status != "running" {
		t.Fatalf("unexpected resource: %+v", res)
	}

	// 6. Action: stop resource
	actResp, err := svc.ActionResource(ctx, res.ID, "stop")
	if err != nil {
		t.Fatalf("ActionResource failed: %v", err)
	}
	if actResp.Status != "succeeded" {
		t.Fatalf("expected action succeeded, got %s", actResp.Status)
	}
	if actionCalled != "stop:i-discovered01" {
		t.Fatalf("unexpected action called on mock: %s", actionCalled)
	}

	// 7. Cleanup
	if err := svc.DeleteAccount(ctx, acc.ID); err != nil {
		t.Fatalf("DeleteAccount failed: %v", err)
	}
}

func TestEnsureBuiltinProviders_SelfHealing(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	masterKey := make([]byte, 32)
	credStore := credentials.NewStore(d, masterKey)
	pm := provider.NewManager(t.TempDir())
	svc := cloud.NewService(d, credStore, pm)

	// Clean existing aliyun provider
	_, _ = d.Exec(ctx, "DELETE FROM providers WHERE provider_code = ?", "aliyun")

	// 1. Fresh seeding -> should be absolute path
	if err := svc.EnsureBuiltinProviders(ctx); err != nil {
		t.Fatalf("EnsureBuiltinProviders failed: %v", err)
	}

	var execPath string
	if err := d.QueryRow(ctx, "SELECT exec_path FROM providers WHERE provider_code = ?", "aliyun").Scan(&execPath); err != nil {
		t.Fatalf("query exec_path failed: %v", err)
	}
	if execPath != "/usr/local/bin/dash-provider-aliyun" {
		t.Errorf("expected /usr/local/bin/dash-provider-aliyun, got %s", execPath)
	}

	// 2. Simulate old machine with relative path
	_, err := d.Exec(ctx, "UPDATE providers SET exec_path = ? WHERE provider_code = ?", "bin/dash-provider-aliyun", "aliyun")
	if err != nil {
		t.Fatalf("simulate old relative exec_path failed: %v", err)
	}

	// 3. EnsureBuiltinProviders during startup/upgrade should self-heal to absolute path
	if err := svc.EnsureBuiltinProviders(ctx); err != nil {
		t.Fatalf("EnsureBuiltinProviders self-healing failed: %v", err)
	}

	if err := d.QueryRow(ctx, "SELECT exec_path FROM providers WHERE provider_code = ?", "aliyun").Scan(&execPath); err != nil {
		t.Fatalf("query exec_path after self-heal failed: %v", err)
	}
	if execPath != "/usr/local/bin/dash-provider-aliyun" {
		t.Errorf("expected self-healed /usr/local/bin/dash-provider-aliyun, got %s", execPath)
	}

	// 4. Idempotency: run again, should remain unchanged
	if err := svc.EnsureBuiltinProviders(ctx); err != nil {
		t.Fatalf("idempotent EnsureBuiltinProviders failed: %v", err)
	}
	if err := d.QueryRow(ctx, "SELECT exec_path FROM providers WHERE provider_code = ?", "aliyun").Scan(&execPath); err != nil {
		t.Fatalf("query exec_path after second run failed: %v", err)
	}
	if execPath != "/usr/local/bin/dash-provider-aliyun" {
		t.Errorf("expected /usr/local/bin/dash-provider-aliyun, got %s", execPath)
	}
}

