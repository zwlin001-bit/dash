package protocol

// 上行方法名常量
const (
	MethodAgentHello   = "agent.hello"
	MethodAgentMetrics = "agent.metrics"
	MethodAgentFacts   = "agent.facts"
)

// 第二期方法名，本期只留常量占位，不定义 params 结构体
const (
	MethodAgentResult   = "agent.result"
	MethodAgentPingRes  = "agent.ping_result"
	MethodAgentSpeedRes = "agent.speedtest_result"
)

// ---- agent.hello（request，连接后第一条）----

type HelloParams struct {
	ProtocolVersion int      `json:"protocol_version"`
	AgentVersion    string   `json:"agent_version"`
	BootAtMs        int64    `json:"boot_at_ms"`
	Capabilities    []string `json:"capabilities"` // 第一期恒为 ["metrics","facts"]
	FactsHash       string   `json:"facts_hash"`
}

type HelloResult struct {
	NodeID            string `json:"node_id"`
	ServerTimeMs      int64  `json:"server_time_ms"`
	IntervalFastS     int    `json:"interval_fast_s"`
	IntervalSlowS     int    `json:"interval_slow_s"`
	FactsMaxIntervalS int    `json:"facts_max_interval_s"`
	CollectConns      bool   `json:"collect_conns"`
	NeedFacts         bool   `json:"need_facts"`
}

// ---- agent.metrics（notification，fast 档每轮一条）----
// ★ 所有指标字段都是指针 + omitempty：采集失败时省略，不是填 0

type MetricsParams struct {
	TsMs     int64       `json:"ts_ms"`
	CPUPct   *float64    `json:"cpu_pct,omitempty"`
	MemUsed  *int64      `json:"mem_used,omitempty"`
	SwapUsed *int64      `json:"swap_used,omitempty"`
	Load     *[3]float64 `json:"load,omitempty"` // load1, load5, load15
	Net      *NetReport  `json:"net,omitempty"`
	UptimeS  *int64      `json:"uptime_s,omitempty"`
	Slow     *SlowReport `json:"slow,omitempty"` // 只在 slow 周期到期那轮出现
}

type NetReport struct {
	UpBps     *int64 `json:"up_bps,omitempty"`
	DownBps   *int64 `json:"down_bps,omitempty"`
	TotalUp   *int64 `json:"total_up,omitempty"`
	TotalDown *int64 `json:"total_down,omitempty"`
}

type SlowReport struct {
	DiskUsed  *int64      `json:"disk_used,omitempty"`
	ProcCount *int32      `json:"proc_count,omitempty"`
	TCPCount  *int32      `json:"tcp_count,omitempty"`
	UDPCount  *int32      `json:"udp_count,omitempty"`
	Disks     []DiskEntry `json:"disks,omitempty"`
	NICs      []NICEntry  `json:"nics,omitempty"`
}

type DiskEntry struct {
	Key   string `json:"k"` // 挂载点，如 "/"
	Used  *int64 `json:"used,omitempty"`
	Total *int64 `json:"total,omitempty"`
}

type NICEntry struct {
	Key       string `json:"k"` // 网卡名，如 "eth0"
	TotalUp   *int64 `json:"total_up,omitempty"`
	TotalDown *int64 `json:"total_down,omitempty"`
}

// ---- agent.facts（notification）----
// 字段与 node_facts 表对应，见 10-schema-spec.md §2
// ★ 不含 ipv4/ipv6：agent 不查公网 IP，由服务端从连接来源记录（11-collect-spec.md §9.3）

type FactsParams struct {
	Arch       string `json:"arch,omitempty"`
	OSName     string `json:"os_name,omitempty"`
	OSVersion  string `json:"os_version,omitempty"`
	Kernel     string `json:"kernel,omitempty"`
	Virt       string `json:"virt,omitempty"`
	CPUModel   string `json:"cpu_model,omitempty"`
	CPUCores   int32  `json:"cpu_cores,omitempty"`
	CPUThreads int32  `json:"cpu_threads,omitempty"`
	MemTotal   int64  `json:"mem_total,omitempty"`
	SwapTotal  int64  `json:"swap_total,omitempty"`
	DiskTotal  int64  `json:"disk_total,omitempty"`
	BootAtMs   int64  `json:"boot_at_ms,omitempty"`
	FactsHash  string `json:"facts_hash"`
}
