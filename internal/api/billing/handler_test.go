package billing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apiBilling "dash/internal/api/billing"
	"dash/internal/billing"
	"dash/internal/credentials"
	"dash/internal/db"
	"dash/internal/migrate"
	"dash/internal/ulid"
)

func setupBillingAPITestDB(t *testing.T) *db.DB {
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

	_, _ = d.Exec(ctx, "SELECT GET_LOCK('dash_billing_api_test', 30)")
	t.Cleanup(func() {
		_, _ = d.Exec(context.Background(), "SELECT RELEASE_LOCK('dash_billing_api_test')")
	})

	mig := migrate.New(d, "../../../migrations")
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("migration up failed: %v", err)
	}

	tables := []string{"bill_budgets", "bill_items", "bill_periods", "cloud_resources", "cloud_accounts", "credentials"}
	for _, tbl := range tables {
		_, _ = d.Exec(ctx, fmt.Sprintf("DELETE FROM %s", tbl))
	}

	return d
}

func TestBillingAPI_OverviewAndItemsAndBudgetsCRUD(t *testing.T) {
	d := setupBillingAPITestDB(t)
	defer d.Close()

	ctx := context.Background()
	nowMs := time.Now().UnixMilli()
	currentPeriod := time.Now().UTC().Format("2006-01")

	bStore := billing.NewStore(d)
	credStore := credentials.NewStore(d, make([]byte, 32))
	svc := billing.NewService(d, bStore, credStore, nil, nil)

	mux := http.NewServeMux()
	apiBilling.RegisterRoutes(mux, svc, nil, nil)

	// 1. Seed periods and items
	accID := ulid.New()
	_ = bStore.UpsertPeriod(ctx, &billing.BillPeriod{
		CloudAccountID: accID,
		Period:         currentPeriod,
		Currency:       "CNY",
		TotalAmount:    500.0,
		SyncState:      "ok",
		SyncedAtMs:     &nowMs,
	})

	_ = bStore.ReplaceItems(ctx, accID, currentPeriod, []billing.BillItem{
		{
			CloudAccountID: accID,
			Period:         currentPeriod,
			ResKind:        "instance",
			Currency:       "CNY",
			Amount:         350.0,
		},
		{
			CloudAccountID: accID,
			Period:         currentPeriod,
			ResKind:        "bandwidth",
			Currency:       "CNY",
			Amount:         150.0,
		},
	})

	// Test GET /api/v1/billing/overview
	req := httptest.NewRequest(http.MethodGet, "/api/v1/billing/overview?period="+currentPeriod, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for overview, got %d: %s", rec.Code, rec.Body.String())
	}

	var overview billing.BillingOverview
	if err := json.NewDecoder(rec.Body).Decode(&overview); err != nil {
		t.Fatalf("decode overview failed: %v", err)
	}
	if len(overview.Totals) != 1 || overview.Totals[0].TotalAmount != 500.0 {
		t.Fatalf("unexpected overview totals: %+v", overview.Totals)
	}

	// Test GET /api/v1/billing/items (group_by=kind)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/billing/items?period="+currentPeriod+"&group_by=kind", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for items, got %d: %s", rec.Code, rec.Body.String())
	}

	var itemsResp billing.ItemsResponse
	if err := json.NewDecoder(rec.Body).Decode(&itemsResp); err != nil {
		t.Fatalf("decode items failed: %v", err)
	}
	if len(itemsResp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(itemsResp.Items))
	}

	// Test Budgets CRUD
	// Create Budget
	newBudget := billing.BillBudget{
		ScopeKind: "all",
		Currency:  "CNY",
		Amount:    1000.0,
		WarnRatio: 0.8,
		IsEnabled: true,
	}
	body, _ := json.Marshal(newBudget)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/billing/budgets", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for create budget, got %d: %s", rec.Code, rec.Body.String())
	}

	var createdBudget billing.BillBudget
	if err := json.NewDecoder(rec.Body).Decode(&createdBudget); err != nil {
		t.Fatalf("decode created budget failed: %v", err)
	}
	if createdBudget.ID == "" {
		t.Fatalf("expected budget ID to be generated")
	}

	// List Budgets
	req = httptest.NewRequest(http.MethodGet, "/api/v1/billing/budgets", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for list budgets, got %d", rec.Code)
	}

	var listResp struct {
		Items []*billing.BillBudget `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list budgets failed: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0].Amount != 1000.0 {
		t.Fatalf("unexpected list budgets result: %+v", listResp.Items)
	}
	// Progress should be 500 / 1000 = 0.5
	if listResp.Items[0].CurrentSpent != 500.0 || listResp.Items[0].Progress != 0.5 {
		t.Fatalf("expected spent=500.0, progress=0.5; got spent=%f, progress=%f",
			listResp.Items[0].CurrentSpent, listResp.Items[0].Progress)
	}

	// Delete Budget
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/billing/budgets/"+createdBudget.ID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for delete budget, got %d", rec.Code)
	}
}
