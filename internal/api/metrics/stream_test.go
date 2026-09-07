package metrics

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestStreamBroadcastingAndHeaders 验证 SSE 协议头与实时推送事件。
func TestStreamBroadcastingAndHeaders(t *testing.T) {
	store := NewLatestStore()
	h := NewHandler(nil, store)

	server := httptest.NewServer(http.HandlerFunc(h.HandleStream))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	for i := 0; i < 100; i++ {
		if store.ActiveSubscribers() > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	reader := bufio.NewReader(resp.Body)

	// 1. 广播 metrics 事件
	store.BroadcastMetrics("node-1", NodeLatest{
		TsMs:       1750000000000,
		CpuPct:     12.3,
		MemUsed:    2048,
		NetUpBps:   100,
		NetDownBps: 200,
	})

	line1, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read line1: %v", err)
	}
	if strings.TrimSpace(line1) != "event: metrics" {
		t.Fatalf("expected 'event: metrics', got %q", line1)
	}

	line2, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read line2: %v", err)
	}
	if !strings.HasPrefix(line2, "data: ") || !strings.Contains(line2, `"node_id":"node-1"`) {
		t.Fatalf("unexpected data: %q", line2)
	}

	// 2. 广播 node_state 事件
	store.BroadcastNodeState("node-2", "offline", 1750000010000)

	// 跳过空行分隔符
	_, _ = reader.ReadString('\n')

	line3, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read line3: %v", err)
	}
	if strings.TrimSpace(line3) != "event: node_state" {
		t.Fatalf("expected 'event: node_state', got %q", line3)
	}

	line4, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read line4: %v", err)
	}
	if !strings.HasPrefix(line4, "data: ") || !strings.Contains(line4, `"conn_state":"offline"`) {
		t.Fatalf("unexpected data: %q", line4)
	}
}

// TestStreamConnectionLeakCheck 验证验收项：
// 客户端断开必须正确关闭 goroutine 和 channel，运行连接泄漏检查。
func TestStreamConnectionLeakCheck(t *testing.T) {
	store := NewLatestStore()
	h := NewHandler(nil, store)

	server := httptest.NewServer(http.HandlerFunc(h.HandleStream))
	defer server.Close()

	initialGoroutines := runtime.NumGoroutine()

	const clientCount = 10
	cancels := make([]context.CancelFunc, clientCount)

	for i := 0; i < clientCount; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels[i] = cancel

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("client %d failed: %v", i, err)
		}
		// 读一次确认连接已建立
		go func(r *http.Response) {
			buf := make([]byte, 256)
			for {
				if _, err := r.Body.Read(buf); err != nil {
					return
				}
			}
		}(resp)
	}

	// 等待所有连接订阅生效
	time.Sleep(50 * time.Millisecond)
	if active := store.ActiveSubscribers(); active != clientCount {
		t.Fatalf("expected %d active subscribers, got %d", clientCount, active)
	}

	// 断开所有客户端连接
	for _, cancel := range cancels {
		cancel()
	}

	// 等待连接关闭与清理
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.ActiveSubscribers() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if active := store.ActiveSubscribers(); active != 0 {
		t.Fatalf("channel leak detected: expected 0 active subscribers after disconnect, got %d", active)
	}

	// 验证 Goroutine 泄漏
	time.Sleep(100 * time.Millisecond)
	finalGoroutines := runtime.NumGoroutine()
	// 允许小幅度波动（httptest 服务器空闲连接等），但绝不能泄漏 10 个以上
	if finalGoroutines-initialGoroutines > 5 {
		t.Fatalf("goroutine leak detected: initial=%d, final=%d", initialGoroutines, finalGoroutines)
	}
}

// TestStreamWithoutDatabase 验证验收项 5：
// SSE 连接在数据库不可用（db 为 nil 或无法连接）时仍能推送最新值。
func TestStreamWithoutDatabase(t *testing.T) {
	// 传入 nil DB，模拟数据库完全不可用场景
	store := NewLatestStore()
	h := NewHandler(nil, store)

	server := httptest.NewServer(http.HandlerFunc(h.HandleStream))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("SSE connection failed when DB is nil: %v", err)
	}
	defer resp.Body.Close()

	for i := 0; i < 100; i++ {
		if store.ActiveSubscribers() > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	reader := bufio.NewReader(resp.Body)

	// 推送最新采样值
	store.BroadcastMetrics("node-db-down", NodeLatest{
		TsMs:    1750000000000,
		CpuPct:  88.8,
		MemUsed: 1024,
	})

	line1, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to receive event: %v", err)
	}
	if strings.TrimSpace(line1) != "event: metrics" {
		t.Fatalf("expected 'event: metrics', got %q", line1)
	}

	line2, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read data line: %v", err)
	}
	if !strings.Contains(line2, `"node-db-down"`) || !strings.Contains(line2, `88.8`) {
		t.Fatalf("expected payload with node-db-down, got %s", line2)
	}
}
