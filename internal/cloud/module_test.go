package cloud_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dash/internal/app"
	"dash/internal/cloud"
	"dash/internal/config"
	"dash/internal/provider"
	"dash/internal/ulid"
)

func setupTestApp(t *testing.T) (*app.App, *cloud.Module, *provider.MockClient) {
	d := getTestDB(t)
	cfg := config.DefaultConfig()
	cfg.Server.DevNoAuth = true // bypass auth in test

	a := app.NewApp(d, cfg, "test-version")
	mod := cloud.NewModule()
	if err := mod.Register(a); err != nil {
		t.Fatalf("failed to register cloud module: %v", err)
	}

	mock := &provider.MockClient{
		DescribeFunc: func(ctx context.Context) (*provider.ProviderDescription, error) {
			return &provider.ProviderDescription{ProviderCode: "aliyun", DisplayName: "阿里云"}, nil
		},
		GetCDTTrafficFunc: func(ctx context.Context, cred map[string]string) (*provider.CDTTrafficResult, error) {
			return &provider.CDTTrafficResult{TrafficBytes: 5000000}, nil
		},
		DiscoverFunc: func(ctx context.Context, cred map[string]string, regions []string, accountSite string) ([]provider.NormalizedResource, error) {
			return []provider.NormalizedResource{
				{
					ProviderCode: "aliyun",
					Kind:         "instance",
					Ref:          "i-test100",
					Name:         "test-instance-100",
					Region:       "cn-hongkong",
					Status:       "running",
					PublicIPs:    []string{"1.1.1.1"},
					Specs:        provider.ResourceSpecs{VCPU: 2, MemMB: 4096},
				},
			}, nil
		},
		ActionFunc: func(ctx context.Context, req provider.ActionParams) (*provider.ActionResponse, error) {
			return &provider.ActionResponse{JobHandle: "job-act-1", Status: "succeeded"}, nil
		},
	}
	mod.ProviderManager().RegisterMock("aliyun", mock)

	return a, mod, mock
}

func TestCredentialsAPI(t *testing.T) {
	a, _, _ := setupTestApp(t)

	// 1. Create Credential via POST /api/v1/credentials
	createBody, _ := json.Marshal(map[string]string{
		"name":              "aliyun-cred-" + ulid.New(),
		"cred_kind":         "aliyun_ak",
		"access_key_id":     "LTAI5testAK1234",
		"access_key_secret": "mySuperSecretSK9999",
	})
	req := httptest.NewRequest("POST", "/api/v1/credentials", bytes.NewReader(createBody))
	rec := httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}

	var created cloud.CloudAccount // we just need ID and check response
	var summary map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	credID := summary["id"].(string)
	fingerprint := summary["fingerprint"].(string)
	if fingerprint != "LTAI****1234" {
		t.Fatalf("expected fingerprint LTAI****1234, got %s", fingerprint)
	}

	// Ensure secret is not returned
	if strings.Contains(rec.Body.String(), "mySuperSecretSK9999") {
		t.Fatal("secret leaked in response!")
	}

	// 2. List Credentials via GET /api/v1/credentials
	req = httptest.NewRequest("GET", "/api/v1/credentials", nil)
	rec = httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "mySuperSecretSK9999") {
		t.Fatal("secret leaked in list response!")
	}

	// 3. Create Cloud Account referencing this credential
	accBody, _ := json.Marshal(map[string]any{
		"provider_code":  "aliyun",
		"name":           "acc-" + ulid.New(),
		"credential_id":  credID,
		"default_region": "cn-hongkong",
		"account_site":   "china",
	})
	req = httptest.NewRequest("POST", "/api/v1/cloud-accounts", bytes.NewReader(accBody))
	rec = httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// 4. Try to delete credential -> MUST return 409 Conflict
	req = httptest.NewRequest("DELETE", "/api/v1/credentials/"+credID, nil)
	rec = httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when deleting used credential, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Delete cloud account
	req = httptest.NewRequest("DELETE", "/api/v1/cloud-accounts/"+created.ID, nil)
	rec = httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete account failed: %d", rec.Code)
	}

	// 6. Delete credential now succeeds
	req = httptest.NewRequest("DELETE", "/api/v1/credentials/"+credID, nil)
	rec = httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete credential failed after account removed: %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCloudAccountsAndSyncAPI(t *testing.T) {
	a, mod, _ := setupTestApp(t)

	// Create credential
	credSummary, err := mod.CredStore().Create(context.Background(), "cred-"+ulid.New(), "aliyun_ak", []byte(`{"access_key_id":"ak123","access_key_secret":"sk123"}`))
	if err != nil {
		t.Fatalf("create cred failed: %v", err)
	}

	// 1. Create Account
	accBody, _ := json.Marshal(map[string]any{
		"provider_code":  "aliyun",
		"name":           "acc-sync-" + ulid.New(),
		"credential_id":  credSummary.ID,
		"default_region": "cn-hongkong",
		"account_site":   "china",
	})
	req := httptest.NewRequest("POST", "/api/v1/cloud-accounts", bytes.NewReader(accBody))
	rec := httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create account failed: %d: %s", rec.Code, rec.Body.String())
	}

	var acc cloud.CloudAccount
	_ = json.Unmarshal(rec.Body.Bytes(), &acc)

	// 2. Discover endpoint
	discBody, _ := json.Marshal(map[string]any{
		"credential_id": credSummary.ID,
		"regions":       []string{"cn-hongkong"},
		"account_site":  "china",
	})
	req = httptest.NewRequest("POST", "/api/v1/cloud-accounts/discover", bytes.NewReader(discBody))
	rec = httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("discover failed: %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Trigger sync: POST /api/v1/cloud-accounts/{id}/sync
	req = httptest.NewRequest("POST", "/api/v1/cloud-accounts/"+acc.ID+"/sync", nil)
	rec = httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("trigger sync failed: %d: %s", rec.Code, rec.Body.String())
	}

	var syncResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &syncResp)
	jobID := syncResp["job_id"].(string)

	// Wait for sync to complete
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		req = httptest.NewRequest("GET", "/api/v1/cloud-accounts/sync/"+jobID, nil)
		rec = httptest.NewRecorder()
		a.Mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			var st cloud.SyncJobStatus
			_ = json.Unmarshal(rec.Body.Bytes(), &st)
			if st.State == "succeeded" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 4. List resources: GET /api/v1/cloud-resources
	req = httptest.NewRequest("GET", "/api/v1/cloud-resources?account_id="+acc.ID, nil)
	rec = httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list resources failed: %d", rec.Code)
	}

	var resList struct {
		Items []cloud.CloudResource `json:"items"`
		Total int                   `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resList)
	if len(resList.Items) != 1 {
		t.Fatalf("expected 1 synced resource, got %d", len(resList.Items))
	}
	rID := resList.Items[0].ID

	// 5. Action: POST /api/v1/cloud-resources/{id}/action
	actBody, _ := json.Marshal(map[string]string{"action": "stop"})
	req = httptest.NewRequest("POST", "/api/v1/cloud-resources/"+rID+"/action", bytes.NewReader(actBody))
	rec = httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("action failed: %d: %s", rec.Code, rec.Body.String())
	}

	var actResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &actResp)
	if actResp["status"] != "succeeded" {
		t.Fatalf("expected succeeded, got %+v", actResp)
	}
}

func TestListRegionsAPI(t *testing.T) {
	a, _, mock := setupTestApp(t)

	mock.ListRegionsFunc = func(ctx context.Context, cred map[string]string) ([]provider.Region, error) {
		return []provider.Region{
			{RegionID: "cn-hangzhou", LocalName: "华东1（杭州）"},
			{RegionID: "eu-central-1", LocalName: "欧洲中部 1 (法兰克福)"},
		}, nil
	}

	req := httptest.NewRequest("GET", "/api/v1/cloud-accounts/regions", nil)
	rec := httptest.NewRecorder()
	a.Mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Items []provider.Region `json:"items"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Fatalf("expected 2 regions, got %d", resp.Total)
	}
	if resp.Items[0].RegionID != "cn-hangzhou" || resp.Items[1].RegionID != "eu-central-1" {
		t.Errorf("unexpected items: %+v", resp.Items)
	}
}
