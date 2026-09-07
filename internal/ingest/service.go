package ingest

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"dash/internal/config"
	"dash/internal/db"
	"dash/internal/protocol"
)

// Service 统一协调指标接收、计数器增量计算、最新值缓存、维度时序维护及后台批量落库。
type Service struct {
	db      *db.DB
	buffer  *RingBuffer
	writer  *Writer
	latest  *LatestCache
	counter *CounterManager
	series  *SeriesManager
	mu      sync.Mutex
	running bool
}

// NewService 构建 Ingest 服务实例。
func NewService(database *db.DB, cfg *config.Config, opts *WriterOptions) *Service {
	buffer := NewRingBuffer(DefaultBufferCapacity)
	writer := NewWriter(database, buffer, opts)
	latest := NewLatestCache()
	counter := NewCounterManager(database)
	series := NewSeriesManager(database)

	return &Service{
		db:      database,
		buffer:  buffer,
		writer:  writer,
		latest:  latest,
		counter: counter,
		series:  series,
	}
}

// Start 启动服务：恢复节点计数器 prev 并启动批量写入循环。
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()

	// 进程启动恢复最近 1 小时内各节点的流量累计值
	nowMs := time.Now().UnixMilli()
	_ = s.counter.RecoverAll(ctx, nowMs)

	// 启动后台刷写循环
	s.writer.Start(ctx)
	return nil
}

// Stop 停止后台刷写并排空残留缓冲区。
func (s *Service) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	s.writer.Stop()
}

// Ingest 是接收 Agent 指标的核心入口，由 WebSocket 与 HTTP 回退处理逻辑调用。
// 满足：
// 1. 永不阻塞采集端；
// 2. 内存计算网络流量增量并处理 reset；
// 3. 刷新最新值内存缓存供前端直接读取；
// 4. 提取 Slow 维度样本并映射 series_id；
// 5. 写入 RingBuffer 等待 5 秒或 80% 容量批量落库。
func (s *Service) Ingest(nodeID string, effectiveTs int64, m *protocol.MetricsParams) {
	if m == nil || nodeID == "" {
		return
	}

	var curUp, curDown *int64
	if m.Net != nil {
		curUp = m.Net.TotalUp
		curDown = m.Net.TotalDown
	}

	// 1. 计算流量增量（reset 感知）
	deltaUp, deltaDown := s.counter.CalculateDelta(context.Background(), nodeID, effectiveTs, curUp, curDown)

	// 2. 刷新最新值缓存（前端实时监控视图直接读内存，不查库）
	s.latest.UpdateFromMetrics(nodeID, m, deltaUp, deltaDown)

	// 3. 提取维度时序样本
	var dimSamples []DimSample
	if m.Slow != nil {
		// 磁盘使用与容量 (08-field-map.md §2)
		for _, d := range m.Slow.Disks {
			if d.Key == "" {
				continue
			}
			if d.Used != nil {
				sID, err := s.series.GetOrCreate(context.Background(), nodeID, "disk.used", d.Key, effectiveTs)
				if err == nil && sID != "" {
					dimSamples = append(dimSamples, DimSample{
						SeriesID: sID,
						TsMs:     effectiveTs,
						Val:      float64(*d.Used),
					})
				}
			}
			if d.Total != nil {
				sID, err := s.series.GetOrCreate(context.Background(), nodeID, "disk.total", d.Key, effectiveTs)
				if err == nil && sID != "" {
					dimSamples = append(dimSamples, DimSample{
						SeriesID: sID,
						TsMs:     effectiveTs,
						Val:      float64(*d.Total),
					})
				}
			}
		}

		// 网卡累计流量
		for _, nic := range m.Slow.NICs {
			if nic.Key == "" {
				continue
			}
			if nic.TotalUp != nil {
				sID, err := s.series.GetOrCreate(context.Background(), nodeID, "nic.total_up", nic.Key, effectiveTs)
				if err == nil && sID != "" {
					dimSamples = append(dimSamples, DimSample{
						SeriesID: sID,
						TsMs:     effectiveTs,
						Val:      float64(*nic.TotalUp),
					})
				}
			}
			if nic.TotalDown != nil {
				sID, err := s.series.GetOrCreate(context.Background(), nodeID, "nic.total_down", nic.Key, effectiveTs)
				if err == nil && sID != "" {
					dimSamples = append(dimSamples, DimSample{
						SeriesID: sID,
						TsMs:     effectiveTs,
						Val:      float64(*nic.TotalDown),
					})
				}
			}
		}
	}

	// 4. 组装 sample_host 主机行
	host := HostSample{
		NodeID:      nodeID,
		TsMs:        effectiveTs,
		CPUPct:      m.CPUPct,
		MemUsed:     m.MemUsed,
		SwapUsed:    m.SwapUsed,
		UptimeS:     m.UptimeS,
		TrafficUp:   deltaUp,
		TrafficDown: deltaDown,
	}
	if m.Load != nil {
		l1 := m.Load[0]
		l5 := m.Load[1]
		l15 := m.Load[2]
		host.Load1 = &l1
		host.Load5 = &l5
		host.Load15 = &l15
	}
	if m.Net != nil {
		host.NetUpBps = m.Net.UpBps
		host.NetDownBps = m.Net.DownBps
		host.NetTotalUp = m.Net.TotalUp
		host.NetTotalDown = m.Net.TotalDown
	}
	if m.Slow != nil {
		host.DiskUsed = m.Slow.DiskUsed
		host.ProcCount = m.Slow.ProcCount
		host.TCPCount = m.Slow.TCPCount
		host.UDPCount = m.Slow.UDPCount
	}

	// 5. 写入环形双缓冲
	s.buffer.Push(host, dimSamples)
}

// Latest 返回内存最新值缓存实例。
func (s *Service) Latest() *LatestCache {
	return s.latest
}

// Buffer 返回底层环形双缓冲区实例。
func (s *Service) Buffer() *RingBuffer {
	return s.buffer
}

// Writer 返回底层批量写入器实例。
func (s *Service) Writer() *Writer {
	return s.writer
}

// Counter 返回计数器管理器实例。
func (s *Service) Counter() *CounterManager {
	return s.counter
}

// Series 返回维度序列管理器实例。
func (s *Service) Series() *SeriesManager {
	return s.series
}

// DroppedBatches 返回落库失败丢弃的批次数。
func (s *Service) DroppedBatches() uint64 {
	return s.writer.DroppedBatches()
}

// DroppedRows 返回因缓冲满溢或落库失败而丢弃的主机样本总行数。
func (s *Service) DroppedRows() uint64 {
	return s.buffer.DroppedRows() + s.writer.DroppedRows()
}

// HealthResponse 定义 /healthz 端点返回的 JSON 载荷。
type HealthResponse struct {
	Status         string `json:"status"`
	DroppedBatches uint64 `json:"dropped_batches"`
	DroppedRows    uint64 `json:"dropped_rows"`
}

// HandleHealthz 处理 GET /healthz 健康检查请求，暴露丢弃统计指标。
func (s *Service) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(HealthResponse{
		Status:         "ok",
		DroppedBatches: s.DroppedBatches(),
		DroppedRows:    s.DroppedRows(),
	})
}
