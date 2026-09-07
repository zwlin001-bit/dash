package ingest_test

import (
	"context"
	"testing"
	"time"

	"dash/internal/events"
	"dash/internal/ingest"
)

func TestWriter_NormalBatchWrite(t *testing.T) {
	db := getTestDB(t)
	cleanupTables(t, db)
	ctx := context.Background()

	buffer := ingest.NewRingBuffer(100)
	writer := ingest.NewWriter(db, buffer, nil)

	// 推入 3 条主机样本和 2 条维度样本
	cpu1, cpu2 := 10.0, 20.0
	mem1 := int64(1024)
	up1 := int64(500)

	buffer.Push(ingest.HostSample{
		NodeID:   "node-w-001",
		TsMs:     1000,
		CPUPct:   &cpu1,
		MemUsed:  &mem1,
		NetUpBps: &up1,
	}, []ingest.DimSample{
		{SeriesID: "series-dim-001", TsMs: 1000, Val: 100.0},
	})

	buffer.Push(ingest.HostSample{
		NodeID: "node-w-002",
		TsMs:   1000,
		CPUPct: &cpu2,
	}, []ingest.DimSample{
		{SeriesID: "series-dim-002", TsMs: 1000, Val: 200.0},
	})

	if err := writer.FlushSync(ctx); err != nil {
		t.Fatalf("failed to flush: %v", err)
	}

	// 验证库中记录
	var hostCnt, dimCnt int
	_ = db.QueryRow(ctx, "SELECT COUNT(*) FROM sample_host").Scan(&hostCnt)
	_ = db.QueryRow(ctx, "SELECT COUNT(*) FROM sample_dim").Scan(&dimCnt)

	if hostCnt != 2 {
		t.Fatalf("expected 2 sample_host rows, got %d", hostCnt)
	}
	if dimCnt != 2 {
		t.Fatalf("expected 2 sample_dim rows, got %d", dimCnt)
	}
}

func TestWriter_RetryAndDropAccounting(t *testing.T) {
	// 连接到一个无效的 DB 实例模拟不可达
	fakeDB := getTestDB(t)
	_ = fakeDB.Close() // 故意关闭底层数据库触发连接失败

	ctx := context.Background()
	buffer := ingest.NewRingBuffer(10)

	// 配置毫秒级快速退避以便测试快速执行
	opts := &ingest.WriterOptions{
		FlushInterval: 100 * time.Millisecond,
		RetryDelays: []time.Duration{
			5 * time.Millisecond,
			10 * time.Millisecond,
			20 * time.Millisecond,
		},
	}

	writer := ingest.NewWriter(fakeDB, buffer, opts)

	// 注册默认事件 Store 检验 system.db_unreachable 产生
	eventStore := events.NewStore(nil, 64)
	events.SetDefaultStore(eventStore)

	buffer.Push(ingest.HostSample{NodeID: "node-fail-001", TsMs: 1000}, nil)
	buffer.Push(ingest.HostSample{NodeID: "node-fail-002", TsMs: 1000}, nil)

	// 执行 flush，预期在重试 3 次后返回错误并计入掉批
	err := writer.FlushSync(ctx)
	if err == nil {
		t.Fatal("expected flush to fail on closed database")
	}

	if writer.DroppedBatches() != 1 {
		t.Fatalf("expected 1 dropped batch, got %d", writer.DroppedBatches())
	}
	if writer.DroppedRows() != 2 {
		t.Fatalf("expected 2 dropped rows, got %d", writer.DroppedRows())
	}
}
