package metrics

import "dash/internal/ingest"

// NodeLatest 表示节点的最新采样数据（缓存在内存中，对应 08-field-map.md §1）。
type NodeLatest = ingest.NodeLatest

// MetricsQueryResponse 表示时序查询 API 响应结构（列式结构，08-field-map.md §4）。
type MetricsQueryResponse struct {
	NodeID string                `json:"node_id"`
	Source string                `json:"source"`
	FromMs int64                 `json:"from_ms"`
	ToMs   int64                 `json:"to_ms"`
	StepMs int64                 `json:"step_ms"`
	TsMs   []int64               `json:"ts_ms"`
	Series map[string][]*float64 `json:"series"`
}

// MetricStreamEvent 表示 SSE 推送的实时指标事件（12-api-spec.md §6）。
type MetricStreamEvent struct {
	NodeID     string  `json:"node_id"`
	TsMs       int64   `json:"ts_ms"`
	CpuPct     float64 `json:"cpu_pct"`
	MemUsed    int64   `json:"mem_used"`
	NetUpBps   int64   `json:"net_up_bps"`
	NetDownBps int64   `json:"net_down_bps"`
}

// NodeStateStreamEvent 表示 SSE 推送的节点状态变更事件（12-api-spec.md §6）。
type NodeStateStreamEvent struct {
	NodeID       string `json:"node_id"`
	ConnState    string `json:"conn_state"`
	LastSeenAtMs int64  `json:"last_seen_at_ms"`
}

// StreamEvent 表示内部广播流事件。
type StreamEvent struct {
	Event string
	Data  string
}

// ApiError 定义标准 API 错误载荷（12-api-spec.md §1）。
type ApiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ApiErrorResponse 定义标准 API 错误响应封装。
type ApiErrorResponse struct {
	Error ApiError `json:"error"`
}
