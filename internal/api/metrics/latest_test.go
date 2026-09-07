package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLatestMetricsEndpoint(t *testing.T) {
	store := NewLatestStore()
	h := NewHandler(nil, store)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	nodeID := "01TESTNODE000000000000001"

	// 1. 未命中时返回 404 NOT_FOUND
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/nodes/"+nodeID+"/latest", nil)
	rec404 := httptest.NewRecorder()
	mux.ServeHTTP(rec404, req404)

	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec404.Code, rec404.Body.String())
	}

	var errResp ApiErrorResponse
	if err := json.Unmarshal(rec404.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to parse error response: %v", err)
	}
	if errResp.Error.Code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND code, got %s", errResp.Error.Code)
	}

	// 2. 写入内存最新值缓存
	totalMem := int64(16000000000)
	uptime := int64(864000)
	sample := NodeLatest{
		TsMs:       1750000000000,
		CpuPct:     15.5,
		MemUsed:    4000000000,
		MemTotal:   &totalMem,
		NetUpBps:   102400,
		NetDownBps: 204800,
		UptimeS:    &uptime,
	}
	store.SetLatest(nodeID, sample)

	// 3. 再次查询返回 200 与完整最新值
	req200 := httptest.NewRequest(http.MethodGet, "/api/v1/nodes/"+nodeID+"/latest", nil)
	rec200 := httptest.NewRecorder()
	mux.ServeHTTP(rec200, req200)

	if rec200.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec200.Code, rec200.Body.String())
	}

	var gotLatest NodeLatest
	if err := json.Unmarshal(rec200.Body.Bytes(), &gotLatest); err != nil {
		t.Fatalf("failed to parse latest json: %v", err)
	}

	if gotLatest.TsMs != sample.TsMs || gotLatest.CpuPct != sample.CpuPct || gotLatest.MemUsed != sample.MemUsed {
		t.Fatalf("mismatched latest metrics: got %+v, want %+v", gotLatest, sample)
	}
	if gotLatest.MemTotal == nil || *gotLatest.MemTotal != totalMem {
		t.Fatalf("mismatched mem_total: got %v, want %d", gotLatest.MemTotal, totalMem)
	}
}
