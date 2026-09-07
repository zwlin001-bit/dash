package ingest_test

import (
	"fmt"
	"sync"
	"testing"

	"dash/internal/ingest"
)

func TestRingBuffer_NormalAndSwap(t *testing.T) {
	buf := ingest.NewRingBuffer(10)
	if buf.Capacity() != 10 {
		t.Fatalf("expected capacity 10, got %d", buf.Capacity())
	}

	for i := 0; i < 5; i++ {
		host := ingest.HostSample{
			NodeID: fmt.Sprintf("node-%d", i),
			TsMs:   int64(1000 + i),
		}
		dropped := buf.Push(host, nil)
		if dropped {
			t.Fatalf("unexpected dropped on push %d", i)
		}
	}

	if buf.Len() != 5 {
		t.Fatalf("expected len 5, got %d", buf.Len())
	}
	if buf.DroppedRows() != 0 {
		t.Fatalf("expected 0 dropped rows, got %d", buf.DroppedRows())
	}

	batch := buf.Swap()
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if len(batch.HostSamples) != 5 {
		t.Fatalf("expected 5 samples, got %d", len(batch.HostSamples))
	}
	for i := 0; i < 5; i++ {
		expectedID := fmt.Sprintf("node-%d", i)
		if batch.HostSamples[i].NodeID != expectedID {
			t.Fatalf("item %d: expected %s, got %s", i, expectedID, batch.HostSamples[i].NodeID)
		}
	}

	// Buffer should now be empty
	if buf.Len() != 0 {
		t.Fatalf("expected buffer empty after swap, got len %d", buf.Len())
	}
	if buf.Swap() != nil {
		t.Fatal("expected nil batch on empty swap")
	}
}

// TestRingBuffer_OverflowDropOldest 验证验收标准 5：
// 灌入超过 buffer 容量的数据，确认是丢最老的那批且有计数，没有阻塞或 panic
func TestRingBuffer_OverflowDropOldest(t *testing.T) {
	capSize := 50
	buf := ingest.NewRingBuffer(capSize)

	totalPush := 120
	droppedCount := 0

	for i := 0; i < totalPush; i++ {
		host := ingest.HostSample{
			NodeID: fmt.Sprintf("node-%d", i),
			TsMs:   int64(i),
		}
		if buf.Push(host, nil) {
			droppedCount++
		}
	}

	expectedDrops := totalPush - capSize // 120 - 50 = 70
	if droppedCount != expectedDrops {
		t.Fatalf("expected %d dropped returns, got %d", expectedDrops, droppedCount)
	}
	if buf.DroppedRows() != uint64(expectedDrops) {
		t.Fatalf("expected %d recorded dropped rows, got %d", expectedDrops, buf.DroppedRows())
	}
	if buf.Len() != capSize {
		t.Fatalf("expected buffer to hold %d items, got %d", capSize, buf.Len())
	}

	// 确认保留的是最新的 50 条数据（node-70 至 node-119），最老的 70 条被淘汰
	batch := buf.Swap()
	if batch == nil || len(batch.HostSamples) != capSize {
		t.Fatalf("expected %d samples in batch, got %v", capSize, batch)
	}

	for i := 0; i < capSize; i++ {
		expectedID := fmt.Sprintf("node-%d", i+70)
		if batch.HostSamples[i].NodeID != expectedID {
			t.Fatalf("position %d: expected oldest retained item to be %s, got %s", i, expectedID, batch.HostSamples[i].NodeID)
		}
	}
}

func TestRingBuffer_80PercentNotify(t *testing.T) {
	capSize := 10
	buf := ingest.NewRingBuffer(capSize)

	// 推入 7 个元素 (< 80%)
	for i := 0; i < 7; i++ {
		buf.Push(ingest.HostSample{NodeID: fmt.Sprintf("n-%d", i)}, nil)
	}

	select {
	case <-buf.NotifyFlush():
		t.Fatal("unexpected notify before reaching 80% watermark")
	default:
	}

	// 推入第 8 个元素 (达到 80%)
	buf.Push(ingest.HostSample{NodeID: "n-7"}, nil)

	select {
	case <-buf.NotifyFlush():
		// OK, 预期触发
	default:
		t.Fatal("expected notify upon reaching 80% watermark")
	}
}

func TestRingBuffer_ConcurrentPushAndSwap(t *testing.T) {
	buf := ingest.NewRingBuffer(200)
	var wg sync.WaitGroup

	// 并发 10 个 goroutine 各写入 100 条数据
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				buf.Push(ingest.HostSample{
					NodeID: fmt.Sprintf("g%d-n%d", gid, i),
					TsMs:   int64(i),
				}, nil)
			}
		}(g)
	}

	// 并发读取 / Swap
	doneCh := make(chan struct{})
	go func() {
		for {
			select {
			case <-doneCh:
				return
			default:
				_ = buf.Swap()
			}
		}
	}()

	wg.Wait()
	close(doneCh)
}
