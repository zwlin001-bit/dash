package ingest_test

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"dash/internal/db"
	"dash/internal/ingest"
)

func getTestDB(t *testing.T) *db.DB {
	t.Helper()
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("skipping test requiring database: %v", err)
	}
	return d
}

func cleanupTables(t *testing.T, d *db.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tables := []string{
		"sample_host", "sample_host_1m", "sample_host_1h", "sample_host_1d",
		"sample_dim", "sample_dim_1m", "sample_dim_1h", "sample_dim_1d",
	}
	for _, tbl := range tables {
		_, _ = d.Exec(ctx, fmt.Sprintf("DELETE FROM %s", tbl))
	}
}

// TestExpandedHostColumns validates that exactly 44 columns are generated in the exact schema order.
func TestExpandedHostColumns(t *testing.T) {
	cols := ingest.ExpandedHostColumns()
	if len(cols) != 44 {
		t.Fatalf("expected 44 columns, got %d: %v", len(cols), cols)
	}

	// Verify header columns
	if cols[0] != "node_id" || cols[1] != "bucket_ms" || cols[2] != "sample_cnt" {
		t.Fatalf("expected fixed columns [node_id, bucket_ms, sample_cnt], got %v", cols[:3])
	}

	// Verify gauge expansion for cpu_pct
	if cols[3] != "cpu_pct_avg" || cols[4] != "cpu_pct_max" || cols[5] != "cpu_pct_min" {
		t.Fatalf("unexpected cpu_pct expansion: %v", cols[3:6])
	}

	// Verify counter expansion
	foundNetTotalUpLast := false
	for _, c := range cols {
		if c == "net_total_up_last" {
			foundNetTotalUpLast = true
		}
	}
	if !foundNetTotalUpLast {
		t.Fatal("net_total_up_last not found in expanded columns")
	}

	// Verify delta expansion
	if cols[42] != "traffic_up_sum" || cols[43] != "traffic_down_sum" {
		t.Fatalf("unexpected delta columns: %v", cols[42:])
	}
}

// TestSQLGeneration validates the generated SQL structure.
func TestSQLGeneration(t *testing.T) {
	sqlRaw := ingest.BuildHostRollupSQLRaw("sample_host_1m", 60000)
	if sqlRaw == "" {
		t.Fatal("expected non-empty SQL for raw rollup")
	}

	sqlTier := ingest.BuildHostRollupSQLTier("sample_host_1m", "sample_host_1h", 3600000)
	if sqlTier == "" {
		t.Fatal("expected non-empty SQL for tiered rollup")
	}
}

// TestRollupAcc1_AccuracyAndAcc2_Idempotency satisfies Acceptance Criteria 1 and 2:
// 1. 灌 3 天模拟数据，跑 rollup，抽查 _1m 的 avg/max/min 与 raw 直接算的结果一致
// 2. 重复跑 rollup 结果不变（幂等）
func TestRollupAcc1_AccuracyAndAcc2_Idempotency(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	cleanupTables(t, d)
	defer cleanupTables(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	svc := ingest.NewRollupService(d)

	// Seed 3 days of simulated host data.
	// To keep test execution fast yet mathematically representative across days,
	// we generate samples across multiple 1-minute buckets spanning 3 days (72 hours).
	nodeID := "node-test-01"
	baseTime := (int64(1700000000000) / 60000) * 60000 // minute aligned
	totalDays := 3

	// Insert samples in 3 different 1-minute buckets (Day 0, Day 1, Day 2)
	// Each bucket contains 12 samples (simulating 5s intervals).
	type sample struct {
		tsMs       int64
		cpuPct     float64
		memUsed    int64
		netTotalUp int64
		trafficUp  int64
	}

	var allSamples []sample
	for day := 0; day < totalDays; day++ {
		minuteStart := baseTime + int64(day)*86400000
		for i := 0; i < 12; i++ {
			allSamples = append(allSamples, sample{
				tsMs:       minuteStart + int64(i*5000),
				cpuPct:     float64(10 + day*10 + i*2), // e.g. 10..32, 20..42, 30..52
				memUsed:    int64(1000 + i*100),
				netTotalUp: int64(50000 + i*1000),
				trafficUp:  int64(1000),
			})
		}
	}

	// Insert raw samples
	insertRawSQL := `INSERT INTO sample_host (
		node_id, ts_ms, cpu_pct, mem_used, swap_used, load1, load5, load15,
		disk_used, net_up_bps, net_down_bps, net_total_up, net_total_down,
		traffic_up, traffic_down, proc_count, tcp_count, udp_count, uptime_s
	) VALUES (?, ?, ?, ?, 0, 1.0, 1.0, 1.0, 5000, 100, 100, ?, 0, ?, 0, 50, 10, 5, 3600)`

	for _, s := range allSamples {
		_, err := d.Exec(ctx, insertRawSQL,
			nodeID, s.tsMs, s.cpuPct, s.memUsed, s.netTotalUp, s.trafficUp,
		)
		if err != nil {
			t.Fatalf("failed to insert raw sample: %v", err)
		}
	}

	toTime := baseTime + int64(totalDays)*86400000 + 60000

	// 1. Run 1m Rollup
	if err := svc.Rollup1m(ctx, baseTime, toTime); err != nil {
		t.Fatalf("Rollup1m failed: %v", err)
	}

	// Helper to verify bucket calculation
	verifyBucket := func(minuteStart int64, expectedAvg, expectedMax, expectedMin float64, expectedCnt int, expectedTrafficSum, expectedNetLast int64) {
		var cnt int
		var cpuAvg, cpuMax, cpuMin float64
		var trafficSum, netLast int64
		q := `SELECT sample_cnt, cpu_pct_avg, cpu_pct_max, cpu_pct_min, traffic_up_sum, net_total_up_last
		      FROM sample_host_1m WHERE node_id = ? AND bucket_ms = ?`
		err := d.QueryRow(ctx, q, nodeID, minuteStart).Scan(&cnt, &cpuAvg, &cpuMax, &cpuMin, &trafficSum, &netLast)
		if err != nil {
			t.Fatalf("query sample_host_1m for bucket %d failed: %v", minuteStart, err)
		}

		if cnt != expectedCnt {
			t.Errorf("bucket %d: expected cnt=%d, got %d", minuteStart, expectedCnt, cnt)
		}
		if math.Abs(cpuAvg-expectedAvg) > 0.0001 {
			t.Errorf("bucket %d: expected cpuAvg=%.4f, got %.4f", minuteStart, expectedAvg, cpuAvg)
		}
		if math.Abs(cpuMax-expectedMax) > 0.0001 {
			t.Errorf("bucket %d: expected cpuMax=%.4f, got %.4f", minuteStart, expectedMax, cpuMax)
		}
		if math.Abs(cpuMin-expectedMin) > 0.0001 {
			t.Errorf("bucket %d: expected cpuMin=%.4f, got %.4f", minuteStart, expectedMin, cpuMin)
		}
		if trafficSum != expectedTrafficSum {
			t.Errorf("bucket %d: expected trafficSum=%d, got %d", minuteStart, expectedTrafficSum, trafficSum)
		}
		if netLast != expectedNetLast {
			t.Errorf("bucket %d: expected netLast=%d, got %d", minuteStart, expectedNetLast, netLast)
		}
	}

	// Verify Day 0 bucket (10, 12, 14, 16, 18, 20, 22, 24, 26, 28, 30, 32)
	// avg = (10+32)/2 = 21.0, max = 32.0, min = 10.0, cnt = 12, trafficSum = 12 * 1000 = 12000, netLast = 50000 + 11*1000 = 61000
	verifyBucket(baseTime, 21.0, 32.0, 10.0, 12, 12000, 61000)

	// Verify Day 1 bucket (20..42): avg = 31.0, max = 42.0, min = 20.0
	verifyBucket(baseTime+86400000, 31.0, 42.0, 20.0, 12, 12000, 61000)

	// Verify Day 2 bucket (30..52): avg = 41.0, max = 52.0, min = 30.0
	verifyBucket(baseTime+2*86400000, 41.0, 52.0, 30.0, 12, 12000, 61000)

	// 2. Acceptance Criterion 2: Idempotency (repeat rollup, result must remain unchanged)
	if err := svc.Rollup1m(ctx, baseTime, toTime); err != nil {
		t.Fatalf("repeated Rollup1m failed: %v", err)
	}

	var rowCount int
	err := d.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host_1m WHERE node_id = ?", nodeID).Scan(&rowCount)
	if err != nil {
		t.Fatalf("count sample_host_1m failed: %v", err)
	}
	if rowCount != 3 {
		t.Fatalf("expected 3 rows after repeat rollup, got %d", rowCount)
	}

	// Check that values remain strictly identical
	verifyBucket(baseTime, 21.0, 32.0, 10.0, 12, 12000, 61000)
	verifyBucket(baseTime+86400000, 31.0, 42.0, 20.0, 12, 12000, 61000)
}

// TestRollupAcc3_WeightedAverage satisfies Acceptance Criterion 3:
// 3. _1h 的加权 avg 正确：构造一个各分钟样本数不等的用例，验证结果不等于简单平均
func TestRollupAcc3_WeightedAverage(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	cleanupTables(t, d)
	defer cleanupTables(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc := ingest.NewRollupService(d)

	nodeID := "node-weighted-test"
	hourStart := int64(1700000000000) // aligned to hour (1700000000000 % 3600000 == 0 if 1700000000000 is divisible, or truncate)
	hourStart = (hourStart / 3600000) * 3600000
	hourEnd := hourStart + 3600000

	// Construct two 1-minute buckets in sample_host_1m within the same hour:
	// Minute 1: sample_cnt = 10, cpu_pct_avg = 10.0
	// Minute 2: sample_cnt = 90, cpu_pct_avg = 100.0
	// Simple average: (10.0 + 100.0) / 2 = 55.0
	// Weighted average: (10 * 10.0 + 90 * 100.0) / (10 + 90) = 9100 / 100 = 91.0
	min1Bucket := hourStart
	min2Bucket := hourStart + 60000

	cols := ingest.ExpandedHostColumns()
	vals1 := make([]any, len(cols))
	vals2 := make([]any, len(cols))

	vals1[0], vals1[1], vals1[2] = nodeID, min1Bucket, 10
	vals2[0], vals2[1], vals2[2] = nodeID, min2Bucket, 90

	// cpu_pct is at index 3 (_avg), 4 (_max), 5 (_min)
	vals1[3], vals1[4], vals1[5] = 10.0, 10.0, 10.0
	vals2[3], vals2[4], vals2[5] = 100.0, 100.0, 100.0

	// Fill remaining columns with zero/defaults
	for i := 6; i < len(cols); i++ {
		vals1[i] = 0
		vals2[i] = 0
	}

	if err := d.BatchInsert(ctx, "sample_host_1m", cols, [][]any{vals1, vals2}); err != nil {
		t.Fatalf("failed to insert test 1m rows: %v", err)
	}

	// Run 1h rollup
	if err := svc.Rollup1h(ctx, hourStart, hourEnd); err != nil {
		t.Fatalf("Rollup1h failed: %v", err)
	}

	// Query sample_host_1h
	var sampleCnt int
	var cpuAvg, cpuMax, cpuMin float64
	q := "SELECT sample_cnt, cpu_pct_avg, cpu_pct_max, cpu_pct_min FROM sample_host_1h WHERE node_id = ? AND bucket_ms = ?"
	err := d.QueryRow(ctx, q, nodeID, hourStart).Scan(&sampleCnt, &cpuAvg, &cpuMax, &cpuMin)
	if err != nil {
		t.Fatalf("query sample_host_1h failed: %v", err)
	}

	if sampleCnt != 100 {
		t.Fatalf("expected total sample_cnt=100, got %d", sampleCnt)
	}

	simpleAvg := (10.0 + 100.0) / 2.0 // 55.0
	expectedWeightedAvg := 91.0

	if math.Abs(cpuAvg-simpleAvg) < 0.0001 {
		t.Fatalf("FAIL: cpu_pct_avg equals simple average (%.2f)! Weighted average was expected.", cpuAvg)
	}

	if math.Abs(cpuAvg-expectedWeightedAvg) > 0.0001 {
		t.Fatalf("expected weighted avg %.4f, got %.4f", expectedWeightedAvg, cpuAvg)
	}

	if cpuMax != 100.0 || cpuMin != 10.0 {
		t.Fatalf("expected max=100, min=10, got max=%.1f, min=%.1f", cpuMax, cpuMin)
	}

	t.Logf("Successfully verified weighted average: got %.2f (simple avg was %.2f)", cpuAvg, simpleAvg)
}

// TestCatchUp validates startup catch-up from MAX(bucket_ms) in sample_host_1m across 1-hour chunks.
func TestCatchUp(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	cleanupTables(t, d)
	defer cleanupTables(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	svc := ingest.NewRollupService(d)

	nodeID := "node-catchup-test"
	now := time.Now()
	// Seed raw data for 2 hours ago (2 full hours)
	twoHoursAgo := now.Add(-2 * time.Hour).Truncate(time.Hour).UnixMilli()

	// Insert raw samples across 120 minutes (1 sample per minute)
	insertSQL := `INSERT INTO sample_host (
		node_id, ts_ms, cpu_pct, mem_used, swap_used, load1, load5, load15,
		disk_used, net_up_bps, net_down_bps, net_total_up, net_total_down,
		traffic_up, traffic_down, proc_count, tcp_count, udp_count, uptime_s
	) VALUES (?, ?, 15.0, 1000, 0, 1.0, 1.0, 1.0, 5000, 100, 100, 1000, 0, 10, 0, 50, 10, 5, 3600)`

	for i := 0; i < 120; i++ {
		ts := twoHoursAgo + int64(i*60000)
		if _, err := d.Exec(ctx, insertSQL, nodeID, ts); err != nil {
			t.Fatalf("failed to insert raw sample: %v", err)
		}
	}

	// Run CatchUp
	if err := svc.CatchUp(ctx, now.UnixMilli()); err != nil {
		t.Fatalf("CatchUp failed: %v", err)
	}

	// Verify sample_host_1m has 120 buckets
	var cnt1m int
	if err := d.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host_1m WHERE node_id = ?", nodeID).Scan(&cnt1m); err != nil {
		t.Fatalf("count 1m failed: %v", err)
	}
	if cnt1m != 120 {
		t.Fatalf("expected 120 1m buckets after catch-up, got %d", cnt1m)
	}

	// Verify sample_host_1h has 2 buckets
	var cnt1h int
	if err := d.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host_1h WHERE node_id = ?", nodeID).Scan(&cnt1h); err != nil {
		t.Fatalf("count 1h failed: %v", err)
	}
	if cnt1h < 2 {
		t.Fatalf("expected at least 2 1h buckets after catch-up, got %d", cnt1h)
	}
}
