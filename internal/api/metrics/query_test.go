package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dash/internal/db"
)

func TestSelectTable(t *testing.T) {
	tests := []struct {
		name      string
		spanMs    int64
		wantTable string
		wantStep  int64
		wantCol   string
	}{
		{
			name:      "6 hours boundary",
			spanMs:    6 * 3600 * 1000,
			wantTable: "sample_host",
			wantStep:  0,
			wantCol:   "ts_ms",
		},
		{
			name:      "1 hour (under 6h)",
			spanMs:    3600 * 1000,
			wantTable: "sample_host",
			wantStep:  0,
			wantCol:   "ts_ms",
		},
		{
			name:      "3 days boundary",
			spanMs:    3 * 86400 * 1000,
			wantTable: "sample_host_1m",
			wantStep:  60000,
			wantCol:   "bucket_ms",
		},
		{
			name:      "12 hours (between 6h and 3d)",
			spanMs:    12 * 3600 * 1000,
			wantTable: "sample_host_1m",
			wantStep:  60000,
			wantCol:   "bucket_ms",
		},
		{
			name:      "60 days boundary",
			spanMs:    60 * 86400 * 1000,
			wantTable: "sample_host_1h",
			wantStep:  3600000,
			wantCol:   "bucket_ms",
		},
		{
			name:      "10 days (between 3d and 60d)",
			spanMs:    10 * 86400 * 1000,
			wantTable: "sample_host_1h",
			wantStep:  3600000,
			wantCol:   "bucket_ms",
		},
		{
			name:      "1 year (> 60d)",
			spanMs:    365 * 86400 * 1000,
			wantTable: "sample_host_1d",
			wantStep:  86400000,
			wantCol:   "bucket_ms",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTable, gotStep, gotCol := SelectTable(tt.spanMs)
			if gotTable != tt.wantTable || gotStep != tt.wantStep || gotCol != tt.wantCol {
				t.Fatalf("SelectTable(%d) = (%s, %d, %s), want (%s, %d, %s)",
					tt.spanMs, gotTable, gotStep, gotCol, tt.wantTable, tt.wantStep, tt.wantCol)
			}
		})
	}
}

func TestDownsamplePreservesPeaksAndRealTimestamps(t *testing.T) {
	// 创建 1000 个测试点，并在特定索引埋设尖峰
	const n = 1000
	const maxPoints = 400
	baseTime := int64(1750000000000)

	points := make([]RawPoint, n)
	spikeIdx := 555
	spikeTime := baseTime + int64(spikeIdx*5000)
	spikeVal := 99.87

	for i := 0; i < n; i++ {
		ts := baseTime + int64(i*5000)
		val := 10.0 + float64(i%5)
		if i == spikeIdx {
			val = spikeVal
		}
		counterVal := float64(i * 100) // 单调递增
		v := val
		cv := counterVal
		points[i] = RawPoint{
			TsMs: ts,
			Values: map[string]*float64{
				"cpu_pct":      &v,
				"net_total_up": &cv,
			},
		}
	}

	fields := []FieldQuery{
		{FieldName: "cpu_pct", DBCol: "cpu_pct", IsCounter: false},
		{FieldName: "net_total_up", DBCol: "net_total_up", IsCounter: true},
	}

	resTs, resSeries := Downsample(points, fields, maxPoints)

	// 1. 点数必须 ≤ maxPoints (400)
	if len(resTs) > maxPoints {
		t.Fatalf("expected <= %d points, got %d", maxPoints, len(resTs))
	}
	if len(resSeries["cpu_pct"]) != len(resTs) {
		t.Fatalf("cpu_pct series length %d != ts_ms length %d", len(resSeries["cpu_pct"]), len(resTs))
	}

	// 2. 检查尖峰是否被保留
	var foundSpike bool
	var foundSpikeTime bool
	for i, valPtr := range resSeries["cpu_pct"] {
		if valPtr != nil && *valPtr == spikeVal {
			foundSpike = true
			if resTs[i] == spikeTime {
				foundSpikeTime = true
			}
		}
	}

	if !foundSpike {
		t.Fatalf("downsampling dropped peak value %f!", spikeVal)
	}
	if !foundSpikeTime {
		t.Fatalf("downsampling did not use real timestamp %d for peak!", spikeTime)
	}

	// 3. 检查计数器是否单调且取末尾值
	var prevCounter float64 = -1
	for _, valPtr := range resSeries["net_total_up"] {
		if valPtr == nil {
			t.Fatalf("unexpected nil in counter series")
		}
		if *valPtr <= prevCounter {
			t.Fatalf("counter not strictly increasing: prev=%f, cur=%f", prevCounter, *valPtr)
		}
		prevCounter = *valPtr
	}
}

func TestDownsampleNullHandling(t *testing.T) {
	points := []RawPoint{
		{TsMs: 1000, Values: map[string]*float64{"cpu_pct": nil}},
		{TsMs: 2000, Values: map[string]*float64{"cpu_pct": nil}},
	}
	fields := []FieldQuery{{FieldName: "cpu_pct", DBCol: "cpu_pct", IsCounter: false}}
	resTs, resSeries := Downsample(points, fields, 400)

	if len(resTs) != 2 {
		t.Fatalf("expected 2 points, got %d", len(resTs))
	}
	if resSeries["cpu_pct"][0] != nil || resSeries["cpu_pct"][1] != nil {
		t.Fatalf("expected nil for null metrics, got non-nil")
	}
}

func TestParseFields(t *testing.T) {
	// 1. Raw 表
	rawQueries, err := ParseFields("sample_host", "cpu_pct,mem_used,net_total_up")
	if err != nil {
		t.Fatalf("ParseFields raw failed: %v", err)
	}
	if len(rawQueries) != 3 {
		t.Fatalf("expected 3 queries, got %d", len(rawQueries))
	}
	if rawQueries[0].DBCol != "cpu_pct" || rawQueries[1].DBCol != "mem_used" || rawQueries[2].DBCol != "net_total_up" {
		t.Fatalf("unexpected cols for raw table: %+v", rawQueries)
	}

	// 2. Rollup 表：默认聚合与显式聚合
	rollupQueries, err := ParseFields("sample_host_1m", "cpu_pct,cpu_pct:max,mem_used:min,net_total_up,traffic_up")
	if err != nil {
		t.Fatalf("ParseFields rollup failed: %v", err)
	}
	// 去重后应为 4 个字段
	if len(rollupQueries) != 4 {
		t.Fatalf("expected 4 queries, got %d", len(rollupQueries))
	}
	if rollupQueries[0].DBCol != "cpu_pct_avg" {
		t.Fatalf("expected cpu_pct_avg, got %s", rollupQueries[0].DBCol)
	}
	if rollupQueries[1].DBCol != "mem_used_min" {
		t.Fatalf("expected mem_used_min, got %s", rollupQueries[1].DBCol)
	}
	if rollupQueries[2].DBCol != "net_total_up_last" {
		t.Fatalf("expected net_total_up_last, got %s", rollupQueries[2].DBCol)
	}
	if rollupQueries[3].DBCol != "traffic_up_sum" {
		t.Fatalf("expected traffic_up_sum, got %s", rollupQueries[3].DBCol)
	}

	// 3. 非法字段测试
	_, err = ParseFields("sample_host", "cpu_pct,invalid_field")
	if err == nil {
		t.Fatal("expected error for unknown field")
	}

	// 4. 非法聚合测试
	_, err = ParseFields("sample_host_1m", "cpu_pct:sum")
	if err == nil {
		t.Fatal("expected error for unsupported aggregation")
	}
}

// 获取测试数据库（若可用）
func getTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("Skipping DB integration test (cannot connect to local MySQL on 33306): %v", err)
	}
	return d
}

// TestFourSpansAndPerformance 验证验收项 1、2、3：
// 1. 查 6 小时 / 3 天 / 60 天 / 1 年四个跨度，分别命中 raw / 1m / 1h / 1d
// 2. 四个跨度返回点数都 ≤ 400
// 3. 单次查询 < 500ms（记录并打印实际耗时）
func TestFourSpansAndPerformance(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	nodeID := "01TESTNODE000000000000001"
	baseTime := time.Now().UnixMilli()

	// 清理旧种子数据
	_, _ = d.Exec(ctx, "DELETE FROM sample_host WHERE node_id = ?", nodeID)
	_, _ = d.Exec(ctx, "DELETE FROM sample_host_1m WHERE node_id = ?", nodeID)
	_, _ = d.Exec(ctx, "DELETE FROM sample_host_1h WHERE node_id = ?", nodeID)
	_, _ = d.Exec(ctx, "DELETE FROM sample_host_1d WHERE node_id = ?", nodeID)

	defer func() {
		_, _ = d.Exec(ctx, "DELETE FROM sample_host WHERE node_id = ?", nodeID)
		_, _ = d.Exec(ctx, "DELETE FROM sample_host_1m WHERE node_id = ?", nodeID)
		_, _ = d.Exec(ctx, "DELETE FROM sample_host_1h WHERE node_id = ?", nodeID)
		_, _ = d.Exec(ctx, "DELETE FROM sample_host_1d WHERE node_id = ?", nodeID)
	}()

	// 1. 播种 6 小时 raw 数据：4320 个点（5秒一个点）
	t.Log("Seeding 4320 raw points for 6h span...")
	rawCols := []string{"node_id", "ts_ms", "cpu_pct", "mem_used", "net_up_bps", "net_down_bps", "net_total_up"}
	rawRows := make([][]any, 4320)
	rawStart := baseTime - 6*3600*1000
	for i := 0; i < 4320; i++ {
		ts := rawStart + int64(i*5000)
		rawRows[i] = []any{nodeID, ts, float64(i%100) * 0.9, int64(100000000 + i*1000), int64(1024), int64(2048), int64(i * 50)}
	}
	if err := d.BatchInsert(ctx, "sample_host", rawCols, rawRows); err != nil {
		t.Fatalf("failed to seed sample_host: %v", err)
	}

	// 2. 播种 3 天 1m rollup 数据：4320 个点（1分钟一个桶）
	t.Log("Seeding 4320 1m rollup points for 3d span...")
	m1Cols := []string{"node_id", "bucket_ms", "sample_cnt", "cpu_pct_avg", "cpu_pct_max", "cpu_pct_min", "mem_used_avg", "net_total_up_last"}
	m1Rows := make([][]any, 4320)
	m1Start := baseTime - 3*86400*1000
	for i := 0; i < 4320; i++ {
		ts := m1Start + int64(i*60000)
		m1Rows[i] = []any{nodeID, ts, 12, float64(i % 80), float64(i%80) + 10, float64(i % 80), float64(200000000), int64(i * 600)}
	}
	if err := d.BatchInsert(ctx, "sample_host_1m", m1Cols, m1Rows); err != nil {
		t.Fatalf("failed to seed sample_host_1m: %v", err)
	}

	// 3. 播种 60 天 1h rollup 数据：1440 个点（1小时一个桶）
	t.Log("Seeding 1440 1h rollup points for 60d span...")
	h1Cols := []string{"node_id", "bucket_ms", "sample_cnt", "cpu_pct_avg", "cpu_pct_max", "cpu_pct_min", "mem_used_avg", "net_total_up_last"}
	h1Rows := make([][]any, 1440)
	h1Start := baseTime - 60*86400*1000
	for i := 0; i < 1440; i++ {
		ts := h1Start + int64(i*3600000)
		h1Rows[i] = []any{nodeID, ts, 720, float64(i % 70), float64(i%70) + 15, float64(i % 70), float64(300000000), int64(i * 36000)}
	}
	if err := d.BatchInsert(ctx, "sample_host_1h", h1Cols, h1Rows); err != nil {
		t.Fatalf("failed to seed sample_host_1h: %v", err)
	}

	// 4. 播种 1 年 1d rollup 数据：365 个点（1天一个桶）
	t.Log("Seeding 365 1d rollup points for 1y span...")
	d1Cols := []string{"node_id", "bucket_ms", "sample_cnt", "cpu_pct_avg", "cpu_pct_max", "cpu_pct_min", "mem_used_avg", "net_total_up_last"}
	d1Rows := make([][]any, 365)
	d1Start := baseTime - 365*86400*1000
	for i := 0; i < 365; i++ {
		ts := d1Start + int64(i*86400000)
		d1Rows[i] = []any{nodeID, ts, 17280, float64(i % 60), float64(i%60) + 20, float64(i % 60), float64(400000000), int64(i * 864000)}
	}
	if err := d.BatchInsert(ctx, "sample_host_1d", d1Cols, d1Rows); err != nil {
		t.Fatalf("failed to seed sample_host_1d: %v", err)
	}

	engine := NewQueryEngine(d)

	testCases := []struct {
		name       string
		fromMs     int64
		toMs       int64
		fields     string
		wantSource string
	}{
		{
			name:       "Span 6h -> sample_host (raw)",
			fromMs:     rawStart,
			toMs:       baseTime,
			fields:     "cpu_pct,mem_used,net_total_up",
			wantSource: "sample_host",
		},
		{
			name:       "Span 3d -> sample_host_1m",
			fromMs:     m1Start,
			toMs:       baseTime,
			fields:     "cpu_pct:avg,mem_used,net_total_up",
			wantSource: "sample_host_1m",
		},
		{
			name:       "Span 60d -> sample_host_1h",
			fromMs:     h1Start,
			toMs:       baseTime,
			fields:     "cpu_pct:max,mem_used,net_total_up",
			wantSource: "sample_host_1h",
		},
		{
			name:       "Span 1y -> sample_host_1d",
			fromMs:     d1Start,
			toMs:       baseTime,
			fields:     "cpu_pct,mem_used,net_total_up",
			wantSource: "sample_host_1d",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			startTime := time.Now()
			resp, err := engine.QueryMetrics(ctx, nodeID, tc.fromMs, tc.toMs, tc.fields, 400)
			elapsed := time.Since(startTime)

			if err != nil {
				t.Fatalf("QueryMetrics failed: %v", err)
			}

			// 验证 1: 分别命中期望的表
			if resp.Source != tc.wantSource {
				t.Fatalf("expected source %s, got %s", tc.wantSource, resp.Source)
			}

			// 验证 2: 四个跨度返回点数都 ≤ 400
			if len(resp.TsMs) > 400 {
				t.Fatalf("expected points <= 400, got %d", len(resp.TsMs))
			}
			if len(resp.TsMs) == 0 {
				t.Fatalf("expected non-empty points for %s", tc.wantSource)
			}
			for k, s := range resp.Series {
				if len(s) != len(resp.TsMs) {
					t.Fatalf("series %s length %d != ts_ms length %d", k, len(s), len(resp.TsMs))
				}
			}

			// 验证 3: 单次查询耗时 < 500ms
			t.Logf("[%s] 命中表: %s, 返回点数: %d, 单次查询耗时: %v", tc.name, resp.Source, len(resp.TsMs), elapsed)
			if elapsed >= 500*time.Millisecond {
				t.Fatalf("query took %v, exceeding 500ms budget", elapsed)
			}
		})
	}
}

func TestHttpMetricsEndpoint(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	store := NewLatestStore()
	h := NewHandler(d, store)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	from := time.Now().Add(-2 * time.Hour).UnixMilli()
	to := time.Now().UnixMilli()

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/nodes/test-node/metrics?from_ms=%d&to_ms=%d&fields=cpu_pct", from, to), nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MetricsQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	if resp.Source != "sample_host" {
		t.Fatalf("expected source sample_host, got %s", resp.Source)
	}
}
