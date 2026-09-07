package ingest

import (
	"sync"

	"dash/internal/protocol"
)

// NodeLatest 表示节点的内存最新采样数据（对应 08-field-map.md §1 & §4）。
// 供前端实时监控视图直接读取，完全不查数据库。
type NodeLatest struct {
	TsMs             int64    `json:"ts_ms"`
	CpuPct           float64  `json:"cpu_pct"`
	MemUsed          int64    `json:"mem_used"`
	MemTotal         *int64   `json:"mem_total,omitempty"`
	DiskUsed         *int64   `json:"disk_used,omitempty"`
	DiskTotal        *int64   `json:"disk_total,omitempty"`
	NetUpBps         int64    `json:"net_up_bps"`
	NetDownBps       int64    `json:"net_down_bps"`
	TrafficMonthUp   *int64   `json:"traffic_month_up,omitempty"`
	TrafficMonthDown *int64   `json:"traffic_month_down,omitempty"`
	UptimeS          *int64   `json:"uptime_s,omitempty"`
	Load1            *float64 `json:"load1,omitempty"`
	TcpCount         *int     `json:"tcp_count,omitempty"`
}

// LatestCache 提供纯内存最新值缓存与变更监听机制。
type LatestCache struct {
	mu        sync.RWMutex
	nodes     map[string]NodeLatest
	listeners []func(nodeID string, l NodeLatest)
}

// NewLatestCache 创建一个空的最新值内存缓存。
func NewLatestCache() *LatestCache {
	return &LatestCache{
		nodes: make(map[string]NodeLatest),
	}
}

// Get 读取指定节点的最新采样数据。
func (c *LatestCache) Get(nodeID string) (NodeLatest, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.nodes[nodeID]
	return val, ok
}

// Set 显式设置或更新指定节点的最新采样数据。
func (c *LatestCache) Set(nodeID string, l NodeLatest) {
	c.mu.Lock()
	c.nodes[nodeID] = l
	listeners := make([]func(nodeID string, l NodeLatest), len(c.listeners))
	copy(listeners, c.listeners)
	c.mu.Unlock()

	for _, fn := range listeners {
		if fn != nil {
			fn(nodeID, l)
		}
	}
}

// Delete 从缓存中移除指定节点的最新采样数据。
func (c *LatestCache) Delete(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.nodes, nodeID)
}

// UpdateFromMetrics 根据收到的指标上报报文增量刷新节点最新值，保留静态与长期字段。
func (c *LatestCache) UpdateFromMetrics(nodeID string, m *protocol.MetricsParams, trafficUp, trafficDown *int64) NodeLatest {
	if m == nil {
		l, _ := c.Get(nodeID)
		return l
	}

	c.mu.Lock()
	l, exists := c.nodes[nodeID]
	if !exists {
		l = NodeLatest{}
	}

	l.TsMs = m.TsMs
	if m.CPUPct != nil {
		l.CpuPct = *m.CPUPct
	}
	if m.MemUsed != nil {
		l.MemUsed = *m.MemUsed
	}
	if m.Load != nil {
		val := m.Load[0]
		l.Load1 = &val
	}
	if m.Net != nil {
		if m.Net.UpBps != nil {
			l.NetUpBps = *m.Net.UpBps
		}
		if m.Net.DownBps != nil {
			l.NetDownBps = *m.Net.DownBps
		}
	}
	if m.UptimeS != nil {
		l.UptimeS = m.UptimeS
	}

	// 处理 Slow 档可选字段
	if m.Slow != nil {
		if m.Slow.DiskUsed != nil {
			l.DiskUsed = m.Slow.DiskUsed
		}
		if m.Slow.TCPCount != nil {
			cnt := int(*m.Slow.TCPCount)
			l.TcpCount = &cnt
		}

		// 若上报了各磁盘 total，累加作为 DiskTotal
		if len(m.Slow.Disks) > 0 {
			var totalDisk int64
			hasDiskTotal := false
			for _, d := range m.Slow.Disks {
				if d.Total != nil {
					totalDisk += *d.Total
					hasDiskTotal = true
				}
			}
			if hasDiskTotal {
				l.DiskTotal = &totalDisk
			}
		}
	}

	c.nodes[nodeID] = l
	listeners := make([]func(nodeID string, l NodeLatest), len(c.listeners))
	copy(listeners, c.listeners)
	c.mu.Unlock()

	for _, fn := range listeners {
		if fn != nil {
			fn(nodeID, l)
		}
	}

	return l
}

// SetMemTotal 注入由 facts 上报的物理内存总量。
func (c *LatestCache) SetMemTotal(nodeID string, total int64) {
	c.mu.Lock()
	l, exists := c.nodes[nodeID]
	if !exists {
		l = NodeLatest{}
	}
	l.MemTotal = &total
	c.nodes[nodeID] = l
	c.mu.Unlock()
}

// SetDiskTotal 注入磁盘总量。
func (c *LatestCache) SetDiskTotal(nodeID string, total int64) {
	c.mu.Lock()
	l, exists := c.nodes[nodeID]
	if !exists {
		l = NodeLatest{}
	}
	l.DiskTotal = &total
	c.nodes[nodeID] = l
	c.mu.Unlock()
}

// OnUpdate 注册指标更新回调函数（如向 SSE 推送广播）。
func (c *LatestCache) OnUpdate(fn func(nodeID string, l NodeLatest)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners = append(c.listeners, fn)
}
