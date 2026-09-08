package jobs_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apiJobs "dash/internal/api/jobs"
	"dash/internal/app"
	"dash/internal/db"
	"dash/internal/jobs"
	"dash/internal/migrate"
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

	_, _ = d.Exec(ctx, "SELECT GET_LOCK('dash_api_jobs_test', 30)")
	t.Cleanup(func() {
		_, _ = d.Exec(context.Background(), "SELECT RELEASE_LOCK('dash_api_jobs_test')")
	})

	mig := migrate.New(d, "../../../migrations")
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("migration up failed: %v", err)
	}

	_, _ = d.Exec(ctx, "DELETE FROM job_steps")
	_, _ = d.Exec(ctx, "DELETE FROM jobs")

	return d
}

func TestJobsHTTPAPI(t *testing.T) {
	d := setupAPITestDB(t)
	defer d.Close()

	a := app.NewApp(d, nil, "dev")
	a.Config = nil // DevNoAuth or no middleware = allow in test if configured

	// Register with DevNoAuth or mock middleware
	a.SetAuthMiddleware(func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			next(w, r)
		}
	})

	mod := apiJobs.NewModule(jobs.Config{
		Workers:        2,
		DefaultTimeout: 5 * time.Second,
		PollInterval:   20 * time.Millisecond,
	})
	if err := mod.Register(a); err != nil {
		t.Fatalf("register jobs module failed: %v", err)
	}
	defer mod.Stop()

	// 1. GET /api/v1/jobs/kinds
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/jobs/kinds", nil)
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/jobs/kinds status: %d", rec.Code)
	}
	var kindsRes struct {
		Kinds []jobs.JobDefinition `json:"kinds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &kindsRes); err != nil || len(kindsRes.Kinds) == 0 {
		t.Fatalf("expected kinds list, got %v", rec.Body.String())
	}

	// 2. POST /api/v1/jobs (Submit)
	subBody := map[string]any{
		"kind": "test.three_steps",
		"params": map[string]any{
			"step1_delay_ms": 10,
			"step2_delay_ms": 10,
			"step3_delay_ms": 10,
		},
	}
	bodyBytes, _ := json.Marshal(subBody)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/jobs", bytes.NewReader(bodyBytes))
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/jobs status: %d, body: %s", rec.Code, rec.Body.String())
	}

	var submitRes struct {
		Job *jobs.Job `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &submitRes); err != nil || submitRes.Job == nil {
		t.Fatalf("failed to decode submit res: %v", err)
	}
	createdID := submitRes.Job.ID

	// 3. GET /api/v1/jobs/{id}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/jobs/"+createdID, nil)
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/jobs/{id} status: %d", rec.Code)
	}

	// 4. GET /api/v1/jobs (List)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/jobs?limit=10&offset=0", nil)
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/jobs status: %d", rec.Code)
	}
	var listRes struct {
		Items []*jobs.Job `json:"items"`
		Total int         `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listRes); err != nil || listRes.Total == 0 || len(listRes.Items) == 0 {
		t.Fatalf("expected jobs list, got %v", rec.Body.String())
	}

	// 5. GET /api/v1/jobs/{id}/stream (SSE)
	sseCtx, sseCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer sseCancel()

	sseReq := httptest.NewRequest("GET", "/api/v1/jobs/"+createdID+"/stream", nil).WithContext(sseCtx)
	sseRec := httptest.NewRecorder()

	sseDone := make(chan struct{})
	go func() {
		defer close(sseDone)
		a.Mux.ServeHTTP(sseRec, sseReq)
	}()

	<-sseDone

	ct := sseRec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	scanner := bufio.NewScanner(sseRec.Body)
	var foundSnapshot bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: snapshot") {
			foundSnapshot = true
			break
		}
	}
	if !foundSnapshot {
		t.Fatalf("expected snapshot event in SSE stream, body was: %s", sseRec.Body.String())
	}

	// 6. Conflict 409 when target is busy
	conflictBody := map[string]any{
		"kind":         "test.three_steps",
		"target_kind":  "node",
		"target_id":    "node-conflict-01",
		"reject_if_busy": true,
	}
	cbBytes, _ := json.Marshal(conflictBody)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/jobs", bytes.NewReader(cbBytes))
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first submit should succeed, got %d", rec.Code)
	}

	// Immediate second submit with reject_if_busy=true
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/jobs", bytes.NewReader(cbBytes))
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second submit with reject_if_busy should return 409 Conflict, got %d", rec.Code)
	}

	// 7. Cancel and Retry API
	cancelJob, err := mod.Engine().Submit(context.Background(), jobs.SubmitRequest{
		Kind: "test.three_steps",
		Params: map[string]any{
			"step1_delay_ms": 5000,
		},
	})
	if err != nil {
		t.Fatalf("submit for cancel failed: %v", err)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/jobs/"+cancelJob.ID+"/cancel", nil)
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status: %d", rec.Code)
	}

	// Retry cancelled job
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/jobs/"+cancelJob.ID+"/retry", nil)
	a.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry status: %d", rec.Code)
	}
}
