package ingest

import (
	"sync"
	"sync/atomic"
)

// HostSample 表示一条主机级指标采样，直接对应 sample_host 表的列结构 (08-field-map.md §1)。
type HostSample struct {
	NodeID       string
	TsMs         int64
	CPUPct       *float64
	MemUsed      *int64
	SwapUsed     *int64
	Load1        *float64
	Load5        *float64
	Load15       *float64
	DiskUsed     *int64
	NetUpBps     *int64
	NetDownBps   *int64
	NetTotalUp   *int64
	NetTotalDown *int64
	TrafficUp    *int64
	TrafficDown  *int64
	ProcCount    *int32
	TCPCount     *int32
	UDPCount     *int32
	UptimeS      *int64
}

// DimSample 表示一条维度指标采样，直接对应 sample_dim 表的列结构 (08-field-map.md §2)。
type DimSample struct {
	SeriesID string
	TsMs     int64
	Val      float64
}

// Batch 包含一次批量刷写所需的主机样本与维度样本集合。
type Batch struct {
	HostSamples []HostSample
	DimSamples  []DimSample
}

// DefaultBufferCapacity 为环形缓冲区的默认容量（4096 行）。
// 计算依据：预期节点数 (30) × (flush 间隔 5s / fast 间隔 5s) × 4 ≈ 120，默认 4096 提供充足裕量。
const DefaultBufferCapacity = 4096

// RingBuffer 实现定长环形缓冲区与双缓冲机制。
// 满载时自动淘汰最老数据，保证采集链路永不阻塞，并记录丢弃行数。
type RingBuffer struct {
	mu          sync.Mutex
	capacity    int
	hosts       []HostSample
	head        int
	count       int
	dims        []DimSample
	maxDims     int
	droppedRows uint64
	notifyCh    chan struct{}
}

// NewRingBuffer 创建指定容量的环形缓冲区。若 capacity <= 0 则采用默认容量。
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = DefaultBufferCapacity
	}
	return &RingBuffer{
		capacity: capacity,
		hosts:    make([]HostSample, capacity),
		maxDims:  capacity * 8, // 维度指标上限，防止无界膨胀
		notifyCh: make(chan struct{}, 1),
	}
}

// Push 向缓冲区追加一条主机采样与若干维度采样。
// 若缓冲区已满，自动丢弃最老的一条主机数据（覆盖写入），递增丢弃计数，永不阻塞。
// 当缓冲达到 80% 容量时，触发非阻塞通知。
func (b *RingBuffer) Push(host HostSample, dims []DimSample) (dropped bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == b.capacity {
		// 环已满：覆盖 head 处的样本（淘汰最老数据）
		b.hosts[b.head] = host
		b.head = (b.head + 1) % b.capacity
		atomic.AddUint64(&b.droppedRows, 1)
		dropped = true
	} else {
		idx := (b.head + b.count) % b.capacity
		b.hosts[idx] = host
		b.count++
	}

	// 追加维度指标，若超限则截断最老维度
	if len(dims) > 0 {
		b.dims = append(b.dims, dims...)
		if len(b.dims) > b.maxDims {
			excess := len(b.dims) - b.maxDims
			b.dims = b.dims[excess:]
		}
	}

	// 达到 80% 容量时触发刷写信号
	if b.count >= (b.capacity*8)/10 {
		select {
		case b.notifyCh <- struct{}{}:
		default:
		}
	}

	return dropped
}

// Swap 原子取出当前缓冲的所有数据用于后台落库，并重置写入缓冲。
// 耗时在微秒级，flush 期间新上报的数据无锁写入新缓冲（双缓冲）。
func (b *RingBuffer) Swap() *Batch {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == 0 && len(b.dims) == 0 {
		return nil
	}

	hostBatch := make([]HostSample, b.count)
	for i := 0; i < b.count; i++ {
		hostBatch[i] = b.hosts[(b.head+i)%b.capacity]
	}

	dimBatch := b.dims

	// 重置内部状态
	b.head = 0
	b.count = 0
	b.dims = nil

	return &Batch{
		HostSamples: hostBatch,
		DimSamples:  dimBatch,
	}
}

// NotifyFlush 返回达到 80% 容量时的通知通道。
func (b *RingBuffer) NotifyFlush() <-chan struct{} {
	return b.notifyCh
}

// Len 返回当前缓冲区内的主机样本行数。
func (b *RingBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

// Capacity 返回环形缓冲区设定的容量大小。
func (b *RingBuffer) Capacity() int {
	return b.capacity
}

// DroppedRows 返回因缓冲满溢而累计丢弃的主机样本行数。
func (b *RingBuffer) DroppedRows() uint64 {
	return atomic.LoadUint64(&b.droppedRows)
}
