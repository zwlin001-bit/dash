package protocol

// 下行方法名常量
const (
	MethodServerConfig = "server.config"

	// 第二期方法名，本期只留常量占位，不定义 params 结构体
	MethodServerExec      = "server.exec"
	MethodServerUpgrade   = "server.upgrade"
	MethodServerPingTask  = "server.ping_task"
	MethodServerSpeedtest = "server.speedtest"
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
