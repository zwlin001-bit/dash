package alert

// Valid rule kinds
const (
	RuleKindMetric  = "metric"
	RuleKindOffline = "offline"
	RuleKindExpiry  = "expiry"
	RuleKindTraffic = "traffic"
	RuleKindBudget  = "budget"
)

// Valid scope kinds
const (
	ScopeKindAll   = "all"
	ScopeKindGroup = "group"
	ScopeKindTag   = "tag"
	ScopeKindNode  = "node"
)

// Valid compare operations
const (
	CompareOpGT  = "gt"
	CompareOpLT  = "lt"
	CompareOpGTE = "gte"
	CompareOpLTE = "lte"
)

// Valid severities
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Valid event states
const (
	EventStateFiring   = "firing"
	EventStateResolved = "resolved"
)

// AlertRule represents a configured alerting rule (07-monitoring.md §2.1).
type AlertRule struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	IsEnabled        bool     `json:"is_enabled"`
	RuleKind         string   `json:"rule_kind"`  // metric / offline / expiry / traffic / budget
	ScopeKind        string   `json:"scope_kind"` // all / group / tag / node
	ScopeRef         string   `json:"scope_ref,omitempty"`
	MetricCode       string   `json:"metric_code,omitempty"`
	CompareOp        string   `json:"compare_op"` // gt / lt / gte / lte
	Threshold        float64  `json:"threshold"`
	DurationS        int      `json:"duration_s"`         // 去抖持续时间
	Severity         string   `json:"severity"`           // info / warning / critical
	SilenceS         int      `json:"silence_s"`          // 静默期（秒）
	IncludeAutoRenew bool     `json:"include_auto_renew"` // 到期提醒是否包含自动续费节点
	ExtraJSON        string   `json:"extra_json,omitempty"`
	ChannelIDs       []string `json:"channel_ids,omitempty"` // 绑定的通知渠道 ID 列表
	CreatedAtMs      int64    `json:"created_at_ms"`
	UpdatedAtMs      int64    `json:"updated_at_ms"`
}

// AlertEvent represents an incident lifecycle record in alert_events (07-monitoring.md §2.1).
type AlertEvent struct {
	ID           string   `json:"id"`
	AlertRuleID  string   `json:"alert_rule_id"`
	NodeID       string   `json:"node_id"`
	EventState   string   `json:"event_state"` // firing / resolved
	FiredAtMs    int64    `json:"fired_at_ms"`
	ResolvedAtMs *int64   `json:"resolved_at_ms,omitempty"`
	PeakValue    *float64 `json:"peak_value,omitempty"`
	Detail       string   `json:"detail,omitempty"`
	NotifiedAtMs *int64   `json:"notified_at_ms,omitempty"`
	CreatedAtMs  int64    `json:"created_at_ms"`
	UpdatedAtMs  int64    `json:"updated_at_ms"`

	// Enriched fields for API display
	RuleName string `json:"rule_name,omitempty"`
	NodeName string `json:"node_name,omitempty"`
	RuleKind string `json:"rule_kind,omitempty"`
	Severity string `json:"severity,omitempty"`
}

// EventFilter defines query filters for listing alert events.
type EventFilter struct {
	RuleID string
	NodeID string
	State  string
	FromMs int64
	ToMs   int64
	Limit  int
	Offset int
}
