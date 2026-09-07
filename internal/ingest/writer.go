package ingest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/logx"
)

// HostTableColumns 定义 sample_host 批量插入的 19 列有序定义 (08-field-map.md §1)。
var HostTableColumns = []string{
	"node_id",
	"ts_ms",
	"cpu_pct",
	"mem_used",
	"swap_used",
	"load1",
	"load5",
	"load15",
	"disk_used",
	"net_up_bps",
	"net_down_bps",
	"net_total_up",
	"net_total_down",
	"traffic_up",
	"traffic_down",
	"proc_count",
	"tcp_count",
	"udp_count",
	"uptime_s",
}

// DimTableColumns 定义 sample_dim 批量插入的 3 列有序定义 (08-field-map.md §2)。
var DimTableColumns = []string{
	"series_id",
	"ts_ms",
	"val",
}

// WriterOptions 封装批量刷写器的配置参数。
type WriterOptions struct {
	FlushInterval time.Duration
	RetryDelays   []time.Duration
}

// DefaultWriterOptions 返回默认刷写配置：每 5 秒刷写，重试 3 次（退避 1s, 2s, 4s）。
func DefaultWriterOptions() WriterOptions {
	return WriterOptions{
		FlushInterval: 5 * time.Second,
		RetryDelays: []time.Duration{
			1 * time.Second,
			2 * time.Second,
			4 * time.Second,
		},
	}
}

// Writer 负责定时或定量将 RingBuffer 中的采样批量刷写至 ADB。
// 满足：一次 flush 仅执行两条批量语句，失败重试 3 次退避，仍失败则丢弃最老批并告警。
type Writer struct {
	db             *db.DB
	buffer         *RingBuffer
	opts           WriterOptions
	droppedBatches uint64
	droppedRows    uint64
	lastDropTime   time.Time
	lastEventTime  time.Time
	mu             sync.Mutex
	stopCh         chan struct{}
	doneWg         sync.WaitGroup
	running        bool
}

// NewWriter 创建批量落库写入器。
func NewWriter(database *db.DB, buffer *RingBuffer, opts *WriterOptions) *Writer {
	opt := DefaultWriterOptions()
	if opts != nil {
		if opts.FlushInterval > 0 {
			opt.FlushInterval = opts.FlushInterval
		}
		if len(opts.RetryDelays) > 0 {
			opt.RetryDelays = opts.RetryDelays
		}
	}

	return &Writer{
		db:     database,
		buffer: buffer,
		opts:   opt,
		stopCh: make(chan struct{}),
	}
}

// Start 启动后台刷写循环。
func (w *Writer) Start(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.stopCh = make(chan struct{})
	w.mu.Unlock()

	w.doneWg.Add(1)
	go w.runLoop(ctx)
}

// Stop 优雅停止后台刷写循环，并在退出前执行最后一次刷写。
func (w *Writer) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	close(w.stopCh)
	w.mu.Unlock()

	w.doneWg.Wait()

	// 退出前排空残留批次
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.flushOnce(ctx)
}

func (w *Writer) runLoop(ctx context.Context) {
	defer w.doneWg.Done()

	ticker := time.NewTicker(w.opts.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.flushOnce(ctx)
		case <-w.buffer.NotifyFlush():
			w.flushOnce(ctx)
		}
	}
}

// FlushSync 同步触发一次缓冲区刷写（供测试与立即落地使用）。
func (w *Writer) FlushSync(ctx context.Context) error {
	batch := w.buffer.Swap()
	if batch == nil {
		return nil
	}
	return w.writeBatchWithRetry(ctx, batch)
}

func (w *Writer) flushOnce(ctx context.Context) {
	batch := w.buffer.Swap()
	if batch == nil {
		return
	}

	if err := w.writeBatchWithRetry(ctx, batch); err != nil {
		logx.Error(fmt.Sprintf("ingest/writer: batch write aborted: %v", err))
	}
}

func (w *Writer) writeBatchWithRetry(ctx context.Context, batch *Batch) error {
	if w.db == nil {
		// 无数据库实例时直接消费，避免测试阻塞
		return nil
	}

	var lastErr error
	maxAttempts := 1 + len(w.opts.RetryDelays)
	failStart := time.Now()

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := w.opts.RetryDelays[attempt-1]
			select {
			case <-w.stopCh:
				return lastErr
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		err := w.writeBatch(ctx, batch)
		if err == nil {
			return nil
		}

		lastErr = err
		logx.Warn(fmt.Sprintf("ingest/writer: flush attempt %d/%d failed: %v", attempt+1, maxAttempts, err))
	}

	// 3 次重试仍失败：丢弃批次，计入丢批指标，受控触发 system.db_unreachable 告警
	atomic.AddUint64(&w.droppedBatches, 1)
	atomic.AddUint64(&w.droppedRows, uint64(len(batch.HostSamples)))

	w.mu.Lock()
	now := time.Now()
	w.lastDropTime = now
	shouldEmit := now.Sub(w.lastEventTime) >= 60*time.Second
	if shouldEmit {
		w.lastEventTime = now
	}
	droppedBatchesCount := atomic.LoadUint64(&w.droppedBatches)
	w.mu.Unlock()

	if shouldEmit {
		failedSecs := int(time.Since(failStart).Seconds())
		if failedSecs < 1 {
			failedSecs = 1
		}
		events.Emit(ctx, events.Event{
			Type:     "system.db_unreachable",
			Source:   "ingest",
			Title:    "数据库不可达，落库批次已丢弃",
			DedupKey: "system.db_unreachable",
			Payload: map[string]any{
				"error_msg":       lastErr.Error(),
				"failed_seconds":  failedSecs,
				"dropped_batches": droppedBatchesCount,
			},
		})
	}

	return fmt.Errorf("flush failed after %d attempts, batch dropped: %w", maxAttempts, lastErr)
}

// writeBatch 将当前批次写入数据库。一次 flush 严格只产生两条批量语句。
func (w *Writer) writeBatch(ctx context.Context, batch *Batch) error {
	// 1. 批量插入 sample_host
	if len(batch.HostSamples) > 0 {
		rows := make([][]any, len(batch.HostSamples))
		for i, h := range batch.HostSamples {
			rows[i] = []any{
				h.NodeID,
				h.TsMs,
				h.CPUPct,
				h.MemUsed,
				h.SwapUsed,
				h.Load1,
				h.Load5,
				h.Load15,
				h.DiskUsed,
				h.NetUpBps,
				h.NetDownBps,
				h.NetTotalUp,
				h.NetTotalDown,
				h.TrafficUp,
				h.TrafficDown,
				h.ProcCount,
				h.TCPCount,
				h.UDPCount,
				h.UptimeS,
			}
		}

		if err := w.db.BatchInsert(ctx, "sample_host", HostTableColumns, rows); err != nil {
			return fmt.Errorf("batch insert sample_host (%d rows): %w", len(rows), err)
		}
	}

	// 2. 批量插入 sample_dim
	if len(batch.DimSamples) > 0 {
		dimRows := make([][]any, len(batch.DimSamples))
		for i, d := range batch.DimSamples {
			dimRows[i] = []any{
				d.SeriesID,
				d.TsMs,
				d.Val,
			}
		}

		if err := w.db.BatchInsert(ctx, "sample_dim", DimTableColumns, dimRows); err != nil {
			return fmt.Errorf("batch insert sample_dim (%d rows): %w", len(dimRows), err)
		}
	}

	return nil
}

// DroppedBatches 返回落库失败丢弃的批次总数。
func (w *Writer) DroppedBatches() uint64 {
	return atomic.LoadUint64(&w.droppedBatches)
}

// DroppedRows 返回落库失败丢弃的主机指标总行数。
func (w *Writer) DroppedRows() uint64 {
	return atomic.LoadUint64(&w.droppedRows)
}
