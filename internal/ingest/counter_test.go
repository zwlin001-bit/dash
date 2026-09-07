package ingest_test

import (
	"context"
	"testing"
	"time"

	"dash/internal/ingest"
)

func TestCounter_NoPrevWritesNull(t *testing.T) {
	cm := ingest.NewCounterManager(nil)
	ctx := context.Background()

	nodeID := "node-new-001"
	curUp := int64(5000000)
	curDown := int64(10000000)

	// 第一轮上报：无 prev
	deltaUp, deltaDown := cm.CalculateDelta(ctx, nodeID, time.Now().UnixMilli(), &curUp, &curDown)

	// ★ 验收要求：没有 prev 时，本轮的 traffic_up/down 写 NULL，不写全量累计值
	if deltaUp != nil {
		t.Fatalf("expected nil (NULL) deltaUp on first report without prev, got %d", *deltaUp)
	}
	if deltaDown != nil {
		t.Fatalf("expected nil (NULL) deltaDown on first report without prev, got %d", *deltaDown)
	}

	// 第二轮上报：cur 增加 5000
	nextUp := curUp + 5000
	nextDown := curDown + 8000
	dUp2, dDown2 := cm.CalculateDelta(ctx, nodeID, time.Now().UnixMilli(), &nextUp, &nextDown)

	if dUp2 == nil || *dUp2 != 5000 {
		t.Fatalf("expected deltaUp=5000, got %v", dUp2)
	}
	if dDown2 == nil || *dDown2 != 8000 {
		t.Fatalf("expected deltaDown=8000, got %v", dDown2)
	}
}

// TestCounter_RebootResetHandling 验证验收标准 3：
// 模拟机器重启（net_total_up 归零），traffic_up 不出现负值、不出现异常巨值
func TestCounter_RebootResetHandling(t *testing.T) {
	cm := ingest.NewCounterManager(nil)
	ctx := context.Background()
	nodeID := "node-reboot-001"

	// 初始稳态
	up1 := int64(100_000_000_000) // 100 GB
	down1 := int64(200_000_000_000)
	_, _ = cm.CalculateDelta(ctx, nodeID, 1000, &up1, &down1)

	// 机器重启：计数器清零重新开始累计
	upReboot := int64(500)   // 重启后刚跑了 500 bytes
	downReboot := int64(800) // 重启后刚跑了 800 bytes

	deltaUp, deltaDown := cm.CalculateDelta(ctx, nodeID, 2000, &upReboot, &downReboot)

	if deltaUp == nil {
		t.Fatal("expected non-nil deltaUp on reboot")
	}
	if *deltaUp < 0 {
		t.Fatalf("deltaUp must not be negative on reboot, got %d", *deltaUp)
	}
	if *deltaUp != 500 {
		t.Fatalf("expected deltaUp=500 on reset, got %d", *deltaUp)
	}

	if deltaDown == nil {
		t.Fatal("expected non-nil deltaDown on reboot")
	}
	if *deltaDown < 0 {
		t.Fatalf("deltaDown must not be negative on reboot, got %d", *deltaDown)
	}
	if *deltaDown != 800 {
		t.Fatalf("expected deltaDown=800 on reset, got %d", *deltaDown)
	}
}

func TestCounter_RecoveryFromDB(t *testing.T) {
	db := getTestDB(t)
	cleanupTables(t, db)
	ctx := context.Background()

	nodeID := "node-recover-001"
	nowMs := time.Now().UnixMilli()

	// 在 sample_host 中插入最近一行的累计值 (10 秒前)
	prevUp := int64(50_000_000)
	prevDown := int64(80_000_000)
	ts10sAgo := nowMs - 10000

	cols := []string{"node_id", "ts_ms", "net_total_up", "net_total_down"}
	rows := [][]any{{nodeID, ts10sAgo, prevUp, prevDown}}
	if err := db.BatchInsert(ctx, "sample_host", cols, rows); err != nil {
		t.Fatalf("failed to seed sample_host: %v", err)
	}

	// 模拟 dashd 启动，新建 CounterManager 并执行 RecoverAll
	cm := ingest.NewCounterManager(db)
	if err := cm.RecoverAll(ctx, nowMs); err != nil {
		t.Fatalf("failed to recover: %v", err)
	}

	// 模拟 agent 上报：新值在旧值基础上增加 1234
	curUp := prevUp + 1234
	curDown := prevDown + 5678

	deltaUp, deltaDown := cm.CalculateDelta(ctx, nodeID, nowMs, &curUp, &curDown)

	if deltaUp == nil || *deltaUp != 1234 {
		t.Fatalf("expected recovered deltaUp=1234, got %v", deltaUp)
	}
	if deltaDown == nil || *deltaDown != 5678 {
		t.Fatalf("expected recovered deltaDown=5678, got %v", deltaDown)
	}
}

func TestCounter_IgnoreOldDataOver1Hour(t *testing.T) {
	db := getTestDB(t)
	cleanupTables(t, db)
	ctx := context.Background()

	nodeID := "node-stale-001"
	nowMs := time.Now().UnixMilli()

	// 超过 1 小时前的旧数据 (2 小时前)
	staleTs := nowMs - 2*3600*1000
	cols := []string{"node_id", "ts_ms", "net_total_up", "net_total_down"}
	rows := [][]any{{nodeID, staleTs, int64(10_000_000), int64(20_000_000)}}
	if err := db.BatchInsert(ctx, "sample_host", cols, rows); err != nil {
		t.Fatalf("failed to seed stale sample_host: %v", err)
	}

	cm := ingest.NewCounterManager(db)
	if err := cm.RecoverAll(ctx, nowMs); err != nil {
		t.Fatalf("failed to recover: %v", err)
	}

	curUp := int64(15_000_000)
	curDown := int64(25_000_000)

	// 超过 1 小时当作无 prev，返回 nil (NULL)
	deltaUp, deltaDown := cm.CalculateDelta(ctx, nodeID, nowMs, &curUp, &curDown)
	if deltaUp != nil {
		t.Fatalf("expected nil deltaUp for stale data older than 1h, got %v", deltaUp)
	}
	if deltaDown != nil {
		t.Fatalf("expected nil deltaDown for stale data older than 1h, got %v", deltaDown)
	}
}
