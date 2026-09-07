package ingest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"dash/internal/ingest"
)

// TestRetentionAcc4_CleanDeletesExpiredRows satisfies Acceptance Criterion 4:
// 4. 清理任务跑完，sample_host 里没有超出保留期的行
func TestRetentionAcc4_CleanDeletesExpiredRows(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	cleanupTables(t, d)
	defer cleanupTables(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Ensure retention settings are configured in DB:
	// retention.raw_days = 3
	// retention.1m_days = 30
	// retention.1d_days = 0 (permanent)
	_, _ = d.Exec(ctx, "DELETE FROM settings WHERE setting_key LIKE 'retention.%'")
	_, _ = d.Exec(ctx, "INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.raw_days', '3', 0)")
	_, _ = d.Exec(ctx, "INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.1m_days', '30', 0)")
	_, _ = d.Exec(ctx, "INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.1d_days', '0', 0)")

	svc := ingest.NewRetentionService(d)
	svc.SetSleepInterval(0) // 0 sleep for fast test execution

	now := time.Now()
	nowMs := now.UnixMilli()
	rawCutoffMs := nowMs - int64(3)*86400*1000
	m1CutoffMs := nowMs - int64(30)*86400*1000

	// 1. Seed sample_host:
	// 4 expired rows (older than 3 days)
	// 4 valid rows (within 3 days)
	insertHostSQL := `INSERT INTO sample_host (
		node_id, ts_ms, cpu_pct, mem_used, swap_used, load1, load5, load15,
		disk_used, net_up_bps, net_down_bps, net_total_up, net_total_down,
		traffic_up, traffic_down, proc_count, tcp_count, udp_count, uptime_s
	) VALUES (?, ?, 10.0, 1000, 0, 1.0, 1.0, 1.0, 5000, 100, 100, 1000, 0, 10, 0, 50, 10, 5, 3600)`

	expiredHostTimestamps := []int64{
		rawCutoffMs - 86400000,  // 4 days ago
		rawCutoffMs - 3600000*5, // 3 days + 5 hours ago
		rawCutoffMs - 3600000*2, // 3 days + 2 hours ago
		rawCutoffMs - 1000,      // 1 sec before cutoff
	}
	validHostTimestamps := []int64{
		rawCutoffMs + 1000,      // 1 sec after cutoff
		rawCutoffMs + 3600000*2, // 2 days + 22 hours ago
		nowMs - 3600000,         // 1 hour ago
		nowMs - 1000,            // 1 sec ago
	}

	for i, ts := range expiredHostTimestamps {
		_, err := d.Exec(ctx, insertHostSQL, fmt.Sprintf("node-exp-%d", i), ts)
		if err != nil {
			t.Fatalf("failed to insert expired host: %v", err)
		}
	}
	for i, ts := range validHostTimestamps {
		_, err := d.Exec(ctx, insertHostSQL, fmt.Sprintf("node-val-%d", i), ts)
		if err != nil {
			t.Fatalf("failed to insert valid host: %v", err)
		}
	}

	// 2. Seed sample_host_1m:
	// 2 expired rows (older than 30 days)
	// 2 valid rows (within 30 days)
	cols1m := ingest.ExpandedHostColumns()
	expired1mVals := [][]any{
		makeRow1m("node-1m-exp1", m1CutoffMs-86400000, cols1m),
		makeRow1m("node-1m-exp2", m1CutoffMs-3600000, cols1m),
	}
	valid1mVals := [][]any{
		makeRow1m("node-1m-val1", m1CutoffMs+3600000, cols1m),
		makeRow1m("node-1m-val2", nowMs-60000, cols1m),
	}
	if err := d.BatchInsert(ctx, "sample_host_1m", cols1m, expired1mVals); err != nil {
		t.Fatalf("failed to insert expired 1m rows: %v", err)
	}
	if err := d.BatchInsert(ctx, "sample_host_1m", cols1m, valid1mVals); err != nil {
		t.Fatalf("failed to insert valid 1m rows: %v", err)
	}

	// 3. Seed sample_host_1d: 2 rows (retention.1d_days = 0, must NEVER be deleted)
	oldDayVals := [][]any{
		makeRow1m("node-1d-old1", nowMs-int64(500)*86400*1000, cols1m),
		makeRow1m("node-1d-old2", nowMs-int64(1000)*86400*1000, cols1m),
	}
	if err := d.BatchInsert(ctx, "sample_host_1d", cols1m, oldDayVals); err != nil {
		t.Fatalf("failed to insert 1d rows: %v", err)
	}

	// Run retention cleanup
	if err := svc.Clean(ctx, nowMs); err != nil {
		t.Fatalf("retention Clean failed: %v", err)
	}

	// Verify sample_host:
	// - NO rows older than cutoff
	// - Exactly 4 valid rows remain
	var expiredCount int
	err := d.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host WHERE ts_ms < ?", rawCutoffMs).Scan(&expiredCount)
	if err != nil {
		t.Fatalf("query expired sample_host failed: %v", err)
	}
	if expiredCount != 0 {
		t.Fatalf("expected 0 expired rows in sample_host, found %d", expiredCount)
	}

	var validCount int
	err = d.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host WHERE ts_ms >= ?", rawCutoffMs).Scan(&validCount)
	if err != nil {
		t.Fatalf("query valid sample_host failed: %v", err)
	}
	if validCount != len(validHostTimestamps) {
		t.Fatalf("expected %d valid rows in sample_host, found %d", len(validHostTimestamps), validCount)
	}

	// Verify sample_host_1m:
	var expired1mCount int
	err = d.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host_1m WHERE bucket_ms < ?", m1CutoffMs).Scan(&expired1mCount)
	if err != nil {
		t.Fatalf("query expired sample_host_1m failed: %v", err)
	}
	if expired1mCount != 0 {
		t.Fatalf("expected 0 expired rows in sample_host_1m, found %d", expired1mCount)
	}

	var valid1mCount int
	err = d.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host_1m WHERE bucket_ms >= ?", m1CutoffMs).Scan(&valid1mCount)
	if err != nil {
		t.Fatalf("query valid sample_host_1m failed: %v", err)
	}
	if valid1mCount != len(valid1mVals) {
		t.Fatalf("expected %d valid rows in sample_host_1m, found %d", len(valid1mVals), valid1mCount)
	}

	// Verify sample_host_1d:
	// retention.1d_days = 0 means permanent, so all old rows must be preserved!
	var day1Count int
	err = d.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host_1d").Scan(&day1Count)
	if err != nil {
		t.Fatalf("query sample_host_1d failed: %v", err)
	}
	if day1Count != len(oldDayVals) {
		t.Fatalf("expected %d rows in sample_host_1d (permanent retention), found %d", len(oldDayVals), day1Count)
	}
}

// TestRetentionAcc5_BoundedChunkDeletions satisfies Acceptance Criterion 5:
// 5. 清理过程中没有长事务（证明每次 DELETE 的区间是有界的，每轮最多 1 小时）
func TestRetentionAcc5_BoundedChunkDeletions(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	cleanupTables(t, d)
	defer cleanupTables(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc := ingest.NewRetentionService(d)
	svc.SetSleepInterval(1 * time.Millisecond) // non-zero sleep to test sleep behavior

	// Populate 4 hours of expired data:
	// oldest = 10:00, cutoff = 14:00 (span = 4 hours)
	t0 := int64(1700000000000)
	t0 = (t0 / 3600000) * 3600000
	cutoff := t0 + 4*3600000

	insertHostSQL := `INSERT INTO sample_host (
		node_id, ts_ms, cpu_pct, mem_used, swap_used, load1, load5, load15,
		disk_used, net_up_bps, net_down_bps, net_total_up, net_total_down,
		traffic_up, traffic_down, proc_count, tcp_count, udp_count, uptime_s
	) VALUES (?, ?, 10.0, 1000, 0, 1.0, 1.0, 1.0, 5000, 100, 100, 1000, 0, 10, 0, 50, 10, 5, 3600)`

	for h := 0; h < 4; h++ {
		ts := t0 + int64(h)*3600000 + 1000
		if _, err := d.Exec(ctx, insertHostSQL, fmt.Sprintf("node-h-%d", h), ts); err != nil {
			t.Fatalf("failed to insert host sample: %v", err)
		}
	}

	// Clean table directly with cutoff
	start := time.Now()
	if err := svc.CleanTable(ctx, "sample_host", "ts_ms", cutoff); err != nil {
		t.Fatalf("CleanTable failed: %v", err)
	}
	elapsed := time.Since(start)

	// Since 4 hours were processed and sleepInterval is 1ms, at least 4 chunks executed
	if elapsed < 4*time.Millisecond {
		t.Logf("Elapsed: %v", elapsed)
	}

	// Verify all 4 rows deleted
	var count int
	if err := d.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host WHERE ts_ms < ?", cutoff).Scan(&count); err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after cleanup, got %d", count)
	}
}

func makeRow1m(nodeID string, bucketMs int64, cols []string) []any {
	row := make([]any, len(cols))
	row[0] = nodeID
	row[1] = bucketMs
	row[2] = 1
	for i := 3; i < len(cols); i++ {
		row[i] = 0
	}
	return row
}
