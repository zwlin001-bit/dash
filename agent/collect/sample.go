package collect

import (
	"dash/internal/protocol"
)

type diskVal struct {
	used  int64
	total int64
}

type nicVal struct {
	up   int64
	down int64
}

// ListenEntry 表示单个监听端口及关联进程名。
type ListenEntry struct {
	Local string `json:"local"`
	Proc  string `json:"proc"`
}

// ListenPayload 表示 listen 采集器产出的 payload 形状。
type ListenPayload struct {
	Listen []ListenEntry `json:"listen"`
}

// GiascanPayload 表示 giascan 采集器产出的 payload 形状。
type GiascanPayload struct {
	Exists bool    `json:"exists"`
	Size   *int64  `json:"size,omitempty"`
	Text   *string `json:"text,omitempty"`
	Mtime  *int64  `json:"mtime,omitempty"`
	Error  *string `json:"_error,omitempty"`
}

// Sample 保存单轮或多轮采集聚合的结果。
// 内部预先分配存储，避免每轮采集创建大量临时指针对象。
type Sample struct {
	TsMs int64

	// Fast 档字段（指针为 nil 表示未采集或采集失败，不填 0）
	CPUPct   *float64            `json:"cpu_pct,omitempty"`
	MemUsed  *int64              `json:"mem_used,omitempty"`
	SwapUsed *int64              `json:"swap_used,omitempty"`
	Load     *[3]float64         `json:"load,omitempty"`
	Net      *protocol.NetReport `json:"net,omitempty"`
	UptimeS  *int64              `json:"uptime_s,omitempty"`

	// Slow 档字段
	Slow *protocol.SlowReport `json:"slow,omitempty"`

	// Facts 档字段
	Facts *protocol.FactsParams `json:"facts,omitempty"`

	// TierSlow 采集器 payload（指针或 map 为 nil 表示未采集或采集失败，JSON 序列化时省略）
	Listen    *ListenPayload               `json:"listen,omitempty"`
	ToolParam map[string]map[string]string `json:"tool_param,omitempty"`
	Giascan   *GiascanPayload              `json:"giascan,omitempty"`

	// 内部复用的值存储区（避免每轮分配）
	cpuPctVal   float64
	memUsedVal  int64
	swapUsedVal int64
	loadVal     [3]float64
	uptimeVal   int64

	netReportVal    protocol.NetReport
	netUpBpsVal     int64
	netDownBpsVal   int64
	netTotalUpVal   int64
	netTotalDownVal int64

	slowReportVal protocol.SlowReport
	diskUsedVal   int64
	procCountVal  int32
	tcpCountVal   int32
	udpCountVal   int32
	diskValBuf    [32]diskVal
	nicValBuf     [32]nicVal
	diskEntries   []protocol.DiskEntry
	nicEntries    []protocol.NICEntry

	factsVal protocol.FactsParams
}

// NewSample 创建一个新的可复用 Sample。
func NewSample() *Sample {
	return &Sample{
		diskEntries: make([]protocol.DiskEntry, 0, 8),
		nicEntries:  make([]protocol.NICEntry, 0, 8),
	}
}

// Reset 清空指定档位（或全部）的指标指针，复用内部切片底层数组。
func (s *Sample) Reset(tier Tier) {
	if tier == TierFast || tier == 0 {
		s.CPUPct = nil
		s.MemUsed = nil
		s.SwapUsed = nil
		s.Load = nil
		s.Net = nil
		s.UptimeS = nil
	}
	if tier == TierSlow || tier == 0 {
		s.Slow = nil
		s.diskEntries = s.diskEntries[:0]
		s.nicEntries = s.nicEntries[:0]
		s.Listen = nil
		s.ToolParam = nil
		s.Giascan = nil
	}
	if tier == TierFacts || tier == 0 {
		s.Facts = nil
	}
}

// SetListen 设置监听端口清单快照。
func (s *Sample) SetListen(p *ListenPayload) {
	s.Listen = p
}

// SetToolParam 设置工具配置参数快照。
func (s *Sample) SetToolParam(p map[string]map[string]string) {
	s.ToolParam = p
}

// SetGiascan 设置 giascan 可用节点快照。
func (s *Sample) SetGiascan(p *GiascanPayload) {
	s.Giascan = p
}

// SetCPUPct 设置 CPU 使用率百分比。
func (s *Sample) SetCPUPct(v float64) {
	s.cpuPctVal = v
	s.CPUPct = &s.cpuPctVal
}

// SetMem 设置内存与 swap 使用字节数。
func (s *Sample) SetMem(used int64, swap int64, hasSwap bool) {
	s.memUsedVal = used
	s.MemUsed = &s.memUsedVal
	if hasSwap {
		s.swapUsedVal = swap
		s.SwapUsed = &s.swapUsedVal
	}
}

// SetLoad 设置系统负载 (1m, 5m, 15m)。
func (s *Sample) SetLoad(l [3]float64) {
	s.loadVal = l
	s.Load = &s.loadVal
}

// SetNet 设置全网卡聚合的网络收发速率与累计字节。
func (s *Sample) SetNet(upBps, downBps, totalUp, totalDown int64, hasRate bool) {
	s.netTotalUpVal = totalUp
	s.netTotalDownVal = totalDown
	s.netReportVal.TotalUp = &s.netTotalUpVal
	s.netReportVal.TotalDown = &s.netTotalDownVal

	if hasRate {
		s.netUpBpsVal = upBps
		s.netDownBpsVal = downBps
		s.netReportVal.UpBps = &s.netUpBpsVal
		s.netReportVal.DownBps = &s.netDownBpsVal
	} else {
		s.netReportVal.UpBps = nil
		s.netReportVal.DownBps = nil
	}
	s.Net = &s.netReportVal
}

// SetUptime 设置开机运行秒数。
func (s *Sample) SetUptime(sec int64) {
	s.uptimeVal = sec
	s.UptimeS = &s.uptimeVal
}

// EnsureSlow 保证 Slow 报文对象初始化。
func (s *Sample) EnsureSlow() *protocol.SlowReport {
	if s.Slow == nil {
		s.Slow = &s.slowReportVal
		s.Slow.DiskUsed = nil
		s.Slow.ProcCount = nil
		s.Slow.TCPCount = nil
		s.Slow.UDPCount = nil
		s.Slow.Disks = nil
		s.Slow.NICs = nil
	}
	return s.Slow
}

// SetDiskUsed 设置各磁盘挂载点已用字节总和。
func (s *Sample) SetDiskUsed(v int64) {
	slow := s.EnsureSlow()
	s.diskUsedVal = v
	slow.DiskUsed = &s.diskUsedVal
}

// SetProcCount 设置系统进程总数。
func (s *Sample) SetProcCount(v int32) {
	slow := s.EnsureSlow()
	s.procCountVal = v
	slow.ProcCount = &s.procCountVal
}

// SetTCPCount 设置 TCP 连接总数。
func (s *Sample) SetTCPCount(v int32) {
	slow := s.EnsureSlow()
	s.tcpCountVal = v
	slow.TCPCount = &s.tcpCountVal
}

// SetUDPCount 设置 UDP 连接总数。
func (s *Sample) SetUDPCount(v int32) {
	slow := s.EnsureSlow()
	s.udpCountVal = v
	slow.UDPCount = &s.udpCountVal
}

// AddDisk 添加单个挂载点的磁盘用量。
func (s *Sample) AddDisk(k string, used, total int64) {
	slow := s.EnsureSlow()
	idx := len(s.diskEntries)
	var uPtr, tPtr *int64
	if idx < len(s.diskValBuf) {
		s.diskValBuf[idx] = diskVal{used: used, total: total}
		uPtr = &s.diskValBuf[idx].used
		tPtr = &s.diskValBuf[idx].total
	} else {
		u := used
		t := total
		uPtr = &u
		tPtr = &t
	}
	s.diskEntries = append(s.diskEntries, protocol.DiskEntry{
		Key:   k,
		Used:  uPtr,
		Total: tPtr,
	})
	slow.Disks = s.diskEntries
}

// AddNIC 添加单个网卡的累计流量。
func (s *Sample) AddNIC(k string, totalUp, totalDown int64) {
	slow := s.EnsureSlow()
	idx := len(s.nicEntries)
	var uPtr, dPtr *int64
	if idx < len(s.nicValBuf) {
		s.nicValBuf[idx] = nicVal{up: totalUp, down: totalDown}
		uPtr = &s.nicValBuf[idx].up
		dPtr = &s.nicValBuf[idx].down
	} else {
		u := totalUp
		d := totalDown
		uPtr = &u
		dPtr = &d
	}
	s.nicEntries = append(s.nicEntries, protocol.NICEntry{
		Key:       k,
		TotalUp:   uPtr,
		TotalDown: dPtr,
	})
	slow.NICs = s.nicEntries
}

// SetFacts 设置静态 facts。
func (s *Sample) SetFacts(f protocol.FactsParams) {
	s.factsVal = f
	s.Facts = &s.factsVal
}

// ToMetricsParams 将 Sample 转换为协议层 MetricsParams。
func (s *Sample) ToMetricsParams() *protocol.MetricsParams {
	return &protocol.MetricsParams{
		TsMs:     s.TsMs,
		CPUPct:   s.CPUPct,
		MemUsed:  s.MemUsed,
		SwapUsed: s.SwapUsed,
		Load:     s.Load,
		Net:      s.Net,
		UptimeS:  s.UptimeS,
		Slow:     s.Slow,
	}
}
