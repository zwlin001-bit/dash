package jobs

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("job not found")
	ErrTargetBusy     = errors.New("target is already busy with another job")
	ErrInvalidState   = errors.New("invalid job state transition")
	ErrUnknownJobKind = errors.New("unknown job kind")
	ErrNonRetryable   = errors.New("job step is not idempotent and cannot be retried")
)

// JobState 表示 Job 的运行状态。
type JobState string

const (
	StatePending   JobState = "pending"
	StateRunning   JobState = "running"
	StateSucceeded JobState = "succeeded"
	StateFailed    JobState = "failed"
	StateCancelled JobState = "cancelled"
)

// StepState 表示 Step 的运行状态。
type StepState string

const (
	StepPending   StepState = "pending"
	StepRunning   StepState = "running"
	StepSucceeded StepState = "succeeded"
	StepFailed    StepState = "failed"
	StepSkipped   StepState = "skipped"
)

// Job 对应 jobs 表中的一行记录。
type Job struct {
	ID            string         `json:"id"`
	JobKind       string         `json:"job_kind"`
	JobState      JobState       `json:"job_state"`
	TargetKind    string         `json:"target_kind,omitempty"`
	TargetID      string         `json:"target_id,omitempty"`
	ParamsJSON    string         `json:"params_json,omitempty"`
	Params        map[string]any `json:"params,omitempty"`
	ResultJSON    string         `json:"result_json,omitempty"`
	Result        map[string]any `json:"result,omitempty"`
	ErrorText     string         `json:"error_text,omitempty"`
	Attempt       int            `json:"attempt"`
	MaxAttempt    int            `json:"max_attempt"`
	ScheduledAtMs int64          `json:"scheduled_at_ms"`
	StartedAtMs   *int64         `json:"started_at_ms,omitempty"`
	FinishedAtMs  *int64         `json:"finished_at_ms,omitempty"`
	CreatedAtMs   int64          `json:"created_at_ms"`
	UpdatedAtMs   int64          `json:"updated_at_ms"`

	Steps []*JobStep `json:"steps,omitempty"`
}

// JobStep 对应 job_steps 表中的一行记录。
type JobStep struct {
	ID           string    `json:"id"`
	JobID        string    `json:"job_id"`
	StepIndex    int       `json:"step_index"`
	Name         string    `json:"name"`
	StepState    StepState `json:"step_state"`
	LogText      string    `json:"log_text,omitempty"`
	StartedAtMs  *int64    `json:"started_at_ms,omitempty"`
	FinishedAtMs *int64    `json:"finished_at_ms,omitempty"`
	CreatedAtMs  int64     `json:"created_at_ms"`
	UpdatedAtMs  int64     `json:"updated_at_ms"`
}

// StepContext 传递给 Step 的执行函数。
type StepContext struct {
	context.Context
	JobID      string
	JobKind    string
	TargetKind string
	TargetID   string
	Params     map[string]any
	Attempt    int
	StepIndex  int
	StepName   string

	// Log 输出日志并实时持久化广播
	Log func(format string, args ...any)
	// SetSharedData / GetSharedData 允许在同一个 Job 的各 Step 间传递数据
	SetSharedData func(key string, val any)
	GetSharedData func(key string) (any, bool)
	// SetResult 保存最终任务结果
	SetResult func(result map[string]any)
}

// StepDef 定义单个步骤的规格。
type StepDef struct {
	Name       string                       `json:"name"`
	Idempotent bool                         `json:"idempotent"` // 是否幂等，为 true 时重试或恢复可重新执行
	Run        func(ctx *StepContext) error `json:"-"`
}

// JobDefinition 定义一种任务类型及其步骤流程。
type JobDefinition struct {
	Kind        string        `json:"kind"`
	Description string        `json:"description"`
	Timeout     time.Duration `json:"timeout"` // 总超时时间，默认 10 分钟
	MaxAttempt  int           `json:"max_attempt"` // 默认 1
	Steps       []StepDef     `json:"steps"`
}

// SubmitRequest 提交任务请求。
type SubmitRequest struct {
	Kind         string         `json:"kind"`
	TargetKind   string         `json:"target_kind,omitempty"`
	TargetID     string         `json:"target_id,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	MaxAttempt   int            `json:"max_attempt,omitempty"`
	Timeout      time.Duration  `json:"-"`
	RejectIfBusy bool           `json:"reject_if_busy,omitempty"`
	CreatedBy    string         `json:"created_by,omitempty"`
}

// Filter 任务查询过滤条件。
type Filter struct {
	Kind       string   `json:"kind,omitempty"`
	State      JobState `json:"state,omitempty"`
	TargetKind string   `json:"target_kind,omitempty"`
	TargetID   string   `json:"target_id,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	Offset     int      `json:"offset,omitempty"`
}
