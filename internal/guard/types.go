package guard


// GuardRule 对应 guard_rules 表 (P2-04 §1)。
type GuardRule struct {
	ID              string   `json:"id"`
	CloudResourceID string   `json:"cloud_resource_id"`
	IsEnabled       bool     `json:"is_enabled"`
	ActionsEnabled  bool     `json:"actions_enabled"`
	TrafficLimitGB  *float64 `json:"traffic_limit_gb,omitempty"`
	TrafficAction   string   `json:"traffic_action"` // "stop" | "notify_only"
	ScheduleEnabled bool     `json:"schedule_enabled"`
	ScheduleStart   *string  `json:"schedule_start,omitempty"` // "08:30"
	ScheduleStop    *string  `json:"schedule_stop,omitempty"`  // "20:00"
	ScheduleTZ      string   `json:"schedule_tz"`              // IANA时区，默认 "Asia/Shanghai"
	LastEvalAtMs    *int64   `json:"last_eval_at_ms,omitempty"`
	LastAction      *string  `json:"last_action,omitempty"`
	LastActionAtMs  *int64   `json:"last_action_at_ms,omitempty"`
	CreatedAtMs     int64    `json:"created_at_ms"`
	UpdatedAtMs     int64    `json:"updated_at_ms"`
}

// GuardCycle 对应 guard_cycles 表 (P2-04 §1)。
type GuardCycle struct {
	ID             string   `json:"id"`
	CloudAccountID string   `json:"cloud_account_id"`
	StartedAtMs    int64    `json:"started_at_ms"`
	DurationMs     int      `json:"duration_ms"`
	CDTUsedGB      *float64 `json:"cdt_used_gb,omitempty"`
	CDTError       string   `json:"cdt_error,omitempty"`
	Evaluated      int      `json:"evaluated"`
	Acted          int      `json:"acted"`
	Failed         int      `json:"failed"`
	CreatedAtMs    int64    `json:"created_at_ms"`
}

// EvaluationAction 表示决策动作。
type EvaluationAction string

const (
	ActionStop  EvaluationAction = "stop"
	ActionStart EvaluationAction = "start"
	ActionNoop  EvaluationAction = "noop"
)

// EvaluationItem 表示单个实例在周期内的评估结果。
type EvaluationItem struct {
	AccountID          string           `json:"account_id"`
	AccountName        string           `json:"account_name"`
	ResourceID         string           `json:"resource_id"`
	ResourceName       string           `json:"resource_name"`
	ResourceRef        string           `json:"resource_ref"`
	Region             string           `json:"region"`
	CurrentStatus      string           `json:"current_status"`
	ProposedAction     EvaluationAction `json:"proposed_action"`
	Reason             string           `json:"reason"`
	CDTUsedGB          float64          `json:"cdt_used_gb"`
	TrafficLimitGB     *float64         `json:"traffic_limit_gb,omitempty"`
	UsagePercent       float64          `json:"usage_percent"`
	ActionsEnabled     bool             `json:"actions_enabled"`
	InScheduleStop     bool             `json:"in_schedule_stop"`
	InScheduleRun      bool             `json:"in_schedule_run"`
	NextScheduleAction string           `json:"next_schedule_action,omitempty"`
	NextScheduleTimeMs *int64           `json:"next_schedule_time_ms,omitempty"`
	WouldExecute       bool             `json:"would_execute"`
	JobSubmitted       bool             `json:"job_submitted,omitempty"`
	JobID              string           `json:"job_id,omitempty"`
	Error              string           `json:"error,omitempty"`
}

// EvaluateResult 表示一次完整评估的结果。
type EvaluateResult struct {
	CycleID        string           `json:"cycle_id"`
	StartedAtMs    int64            `json:"started_at_ms"`
	DurationMs     int              `json:"duration_ms"`
	IsDryRun       bool             `json:"is_dry_run"`
	EvaluatedCount int              `json:"evaluated_count"`
	ActedCount     int              `json:"acted_count"`
	FailedCount    int              `json:"failed_count"`
	Items          []EvaluationItem `json:"items"`
}

// RuleUpdateRequest 表示更新规则的请求负载。
type RuleUpdateRequest struct {
	IsEnabled       *bool    `json:"is_enabled,omitempty"`
	ActionsEnabled  *bool    `json:"actions_enabled,omitempty"`
	TrafficLimitGB  *float64 `json:"traffic_limit_gb,omitempty"`
	TrafficAction   *string  `json:"traffic_action,omitempty"`
	ScheduleEnabled *bool    `json:"schedule_enabled,omitempty"`
	ScheduleStart   *string  `json:"schedule_start,omitempty"`
	ScheduleStop    *string  `json:"schedule_stop,omitempty"`
	ScheduleTZ      *string  `json:"schedule_tz,omitempty"`
}

// ForceStartRequest 表示手动强制开机请求。
type ForceStartRequest struct {
	Confirm bool   `json:"confirm"`
	Reason  string `json:"reason"`
}

// InstanceOverview 供前端总览展现的实例信息。
type InstanceOverview struct {
	ResourceID         string     `json:"resource_id"`
	ResourceName       string     `json:"resource_name"`
	ResourceRef        string     `json:"resource_ref"`
	Region             string     `json:"region"`
	Status             string     `json:"status"`
	PublicIPs          []string   `json:"public_ips"`
	PrivateIPs         []string   `json:"private_ips"`
	BillingInfo        string     `json:"billing_info,omitempty"`
	Rule               *GuardRule `json:"rule"`
	NextScheduleAction string     `json:"next_schedule_action,omitempty"`
	NextScheduleTimeMs *int64     `json:"next_schedule_time_ms,omitempty"`
}

// AccountOverview 供前端展现的账号总览。
type AccountOverview struct {
	AccountID      string             `json:"account_id"`
	AccountName    string             `json:"account_name"`
	ProviderCode   string             `json:"provider_code"`
	DefaultRegion  string             `json:"default_region"`
	AccountSite    string             `json:"account_site"`
	CDTUsedGB      *float64           `json:"cdt_used_gb,omitempty"`
	TrafficLimitGB *float64           `json:"traffic_limit_gb,omitempty"`
	UsagePercent   float64            `json:"usage_percent"`
	CDTError       string             `json:"cdt_error,omitempty"`
	Instances      []InstanceOverview `json:"instances"`
}

// OverviewResponse 守卫页面首屏数据响应。
type OverviewResponse struct {
	Accounts       []AccountOverview `json:"accounts"`
	RecentCycles   []GuardCycle      `json:"recent_cycles"`
	TotalInstances int               `json:"total_instances"`
	GuardedCount   int               `json:"guarded_count"`
	ActionsEnabled int               `json:"actions_enabled_count"`
	CurrentTimeMs  int64             `json:"current_time_ms"`
}
