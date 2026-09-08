package protocol

// 下行方法名常量
const (
	MethodServerConfig = "server.config"

	MethodServerExec      = "server.exec"
	MethodServerUpgrade   = "server.upgrade"
	MethodServerPingTask  = "server.ping_task"
	MethodServerSpeedtest = "server.speedtest"
	MethodServerReload    = "server.reload_actions"
)

// ServerConfigParams 是 server.config 的参数（notification，运行中变更采集参数）。
// 全部可选：只下发变了的项。
type ServerConfigParams struct {
	IntervalFastS     *int  `json:"interval_fast_s,omitempty"`
	IntervalSlowS     *int  `json:"interval_slow_s,omitempty"`
	FactsMaxIntervalS *int  `json:"facts_max_interval_s,omitempty"`
	CollectConns      *bool `json:"collect_conns,omitempty"`
	NeedFacts         *bool `json:"need_facts,omitempty"`
}

// ServerExecParams 是 server.exec 的参数（request）。
// ★ 没有 mode 字段，没有 cmd/shell/script 字段。
// action 只能是硬编码白名单注册表里的键；收到其余字段一律回 -32602。
type ServerExecParams struct {
	RequestID string                 `json:"request_id"`
	Action    string                 `json:"action"`
	Args      map[string]interface{} `json:"args"`
	TimeoutS  int                    `json:"timeout_s"`
}
