package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"dash/internal/audit"
	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/logx"
	"dash/internal/ulid"
)

// Config 配置 Job 引擎运行参数。
type Config struct {
	Workers        int           // worker 数量，默认 4
	DefaultTimeout time.Duration // 默认单任务总超时时间，默认 10 分钟
	PollInterval   time.Duration // 待执行任务轮询间隔，默认 500ms
}

// DefaultConfig 返回默认配置。
func DefaultConfig() Config {
	return Config{
		Workers:        4,
		DefaultTimeout: 10 * time.Minute,
		PollInterval:   500 * time.Millisecond,
	}
}

// Engine 是 Job 引擎的核心运行时。
type Engine struct {
	db          *db.DB
	store       *Store
	registry    *Registry
	broadcaster *Broadcaster
	cfg         Config

	workerSem     chan struct{}
	notifyCh      chan struct{}
	activeWorkers int32

	targetMu       sync.Mutex
	runningTargets map[string]string // targetKey -> jobID

	cancelMu      sync.Mutex
	activeCancels map[string]context.CancelFunc // jobID -> cancelFunc

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEngine 创建并初始化 Engine。
func NewEngine(database *db.DB, registry *Registry, cfg Config) *Engine {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 10 * time.Minute
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if registry == nil {
		registry = NewRegistry()
	}

	initEventTypes()

	return &Engine{
		db:             database,
		store:          NewStore(database),
		registry:       registry,
		broadcaster:    NewBroadcaster(),
		cfg:            cfg,
		workerSem:      make(chan struct{}, cfg.Workers),
		notifyCh:       make(chan struct{}, 1),
		runningTargets: make(map[string]string),
		activeCancels:  make(map[string]context.CancelFunc),
	}
}

func initEventTypes() {
	events.RegisterType(events.TypeDef{
		Type:               "job.succeeded",
		DisplayName:        "任务执行成功",
		DefaultSeverity:    "info",
		DefaultDisposition: "store",
		Description:        "后台异步任务顺利完成所有步骤",
	})
	events.RegisterType(events.TypeDef{
		Type:               "job.failed",
		DisplayName:        "任务执行失败",
		DefaultSeverity:    "critical",
		DefaultDisposition: "store+notify",
		Description:        "后台异步任务执行失败或超时",
	})
}

// Store 返回关联的持久化存储。
func (e *Engine) Store() *Store {
	return e.store
}

// Registry 返回关联的任务注册表。
func (e *Engine) Registry() *Registry {
	return e.registry
}

// Broadcaster 返回关联的事件广播器。
func (e *Engine) Broadcaster() *Broadcaster {
	return e.broadcaster
}

// ActiveWorkers 返回当前并发运行的任务数。
func (e *Engine) ActiveWorkers() int {
	return int(atomic.LoadInt32(&e.activeWorkers))
}

// Start 启动 Job 引擎，包括崩溃恢复与调度器循环。
func (e *Engine) Start(ctx context.Context) error {
	e.ctx, e.cancel = context.WithCancel(ctx)

	// 1. 崩溃恢复：处理上次进程宕机/kill遗留的 running 任务 (验收 3)
	if err := e.recoverRunningJobs(e.ctx); err != nil {
		logx.Warn("failed to recover running jobs on start", "err", err)
	}

	// 2. 启动调度循环
	e.wg.Add(1)
	go e.dispatchLoop()

	logx.Info("job engine started", "workers", e.cfg.Workers, "default_timeout", e.cfg.DefaultTimeout)
	return nil
}

// Stop 停止 Job 引擎并等待正在运行的任务退出。
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	logx.Info("job engine stopped")
}

// recoverRunningJobs 查找所有状态为 running 的孤儿任务并恢复为 pending (验收 3)。
func (e *Engine) recoverRunningJobs(ctx context.Context) error {
	runningJobs, err := e.store.FindRunningJobs(ctx)
	if err != nil {
		return fmt.Errorf("query running jobs for recovery: %w", err)
	}

	for _, j := range runningJobs {
		logx.Warn("recovering interrupted running job", "job_id", j.ID, "kind", j.JobKind)

		// 检查 steps，如有 running 步骤重置为 pending
		steps, err := e.store.GetSteps(ctx, j.ID)
		if err == nil {
			for _, st := range steps {
				if st.StepState == StepRunning {
					st.StepState = StepPending
					st.LogText += "\n[系统恢复] 检测到进程意外中断，本步骤将重新执行"
					_ = e.store.UpdateStep(ctx, st)
				}
			}
		}

		// 重置 job 为 pending，以便被调度器认领
		if err := e.store.ResetInterruptedJob(ctx, j.ID); err != nil {
			logx.Error("failed to reset interrupted job", "job_id", j.ID, "err", err)
		} else {
			_ = audit.Log(ctx, e.db, audit.Entry{
				ActorKind:  "system",
				Action:     "job.recovered",
				TargetKind: "job",
				TargetID:   j.ID,
				Detail:     fmt.Sprintf("kind=%s recovered after restart", j.JobKind),
				Result:     "ok",
			})
		}
	}

	if len(runningJobs) > 0 {
		e.triggerDispatch()
	}
	return nil
}

// Submit 提交一个新任务。
func (e *Engine) Submit(ctx context.Context, req SubmitRequest) (*Job, error) {
	def, ok := e.registry.Get(req.Kind)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownJobKind, req.Kind)
	}

	// 单实例校验：同一个 target 上同类任务不许并发 (验收 4)
	if req.TargetKind != "" && req.TargetID != "" {
		if req.RejectIfBusy {
			hasActive, err := e.store.HasActiveJobOnTarget(ctx, req.TargetKind, req.TargetID, req.Kind)
			if err != nil {
				return nil, fmt.Errorf("check target active job: %w", err)
			}
			if hasActive {
				return nil, ErrTargetBusy
			}
		}
	}

	maxAttempt := req.MaxAttempt
	if maxAttempt <= 0 {
		maxAttempt = def.MaxAttempt
	}
	if maxAttempt <= 0 {
		maxAttempt = 1
	}

	jobID := ulid.New()
	now := time.Now().UnixMilli()

	// 预先构造初始 steps
	var steps []*JobStep
	for i, s := range def.Steps {
		steps = append(steps, &JobStep{
			ID:        ulid.New(),
			JobID:     jobID,
			StepIndex: i,
			Name:      s.Name,
			StepState: StepPending,
		})
	}

	job := &Job{
		ID:            jobID,
		JobKind:       req.Kind,
		JobState:      StatePending,
		TargetKind:    req.TargetKind,
		TargetID:      req.TargetID,
		Params:        req.Params,
		Attempt:       0,
		MaxAttempt:    maxAttempt,
		ScheduledAtMs: now,
		CreatedAtMs:   now,
		UpdatedAtMs:   now,
		Steps:         steps,
	}

	if err := e.store.CreateJob(ctx, job, steps); err != nil {
		return nil, fmt.Errorf("create job in store: %w", err)
	}

	_ = audit.Log(ctx, e.db, audit.Entry{
		ActorKind:  "user",
		ActorID:    req.CreatedBy,
		Action:     "job.create",
		TargetKind: "job",
		TargetID:   job.ID,
		Detail:     fmt.Sprintf("kind=%s target=%s:%s", job.JobKind, job.TargetKind, job.TargetID),
		Result:     "ok",
	})

	e.triggerDispatch()
	return job, nil
}

// Cancel 取消任务执行 (验收 6)。
func (e *Engine) Cancel(ctx context.Context, jobID string) error {
	j, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}

	if j.JobState == StateSucceeded || j.JobState == StateFailed || j.JobState == StateCancelled {
		return nil // 终态无需处理
	}

	// 1. 若正在运行，调用其 CancelFunc 真正中断执行
	e.cancelMu.Lock()
	cancelFn, running := e.activeCancels[jobID]
	e.cancelMu.Unlock()

	if running && cancelFn != nil {
		cancelFn()
	}

	// 2. 更新数据库状态为 cancelled
	if err := e.store.CancelJob(ctx, jobID); err != nil {
		return err
	}

	// 3. 广播取消状态
	e.broadcaster.Publish(jobID, StreamEvent{
		Event: "job",
		Data: map[string]any{
			"id":        jobID,
			"job_state": StateCancelled,
		},
	})

	_ = audit.Log(ctx, e.db, audit.Entry{
		ActorKind:  "system",
		Action:     "job.cancelled",
		TargetKind: "job",
		TargetID:   jobID,
		Result:     "ok",
	})

	return nil
}

// Retry 重试任务 (验收 2)。
func (e *Engine) Retry(ctx context.Context, jobID string) (*Job, error) {
	j, err := e.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}

	if j.JobState != StateFailed && j.JobState != StateCancelled {
		return nil, fmt.Errorf("%w: only failed or cancelled jobs can be retried", ErrInvalidState)
	}

	if err := e.store.ResetJobForRetry(ctx, jobID); err != nil {
		return nil, err
	}

	_ = audit.Log(ctx, e.db, audit.Entry{
		ActorKind:  "system",
		Action:     "job.retry",
		TargetKind: "job",
		TargetID:   jobID,
		Result:     "ok",
	})

	e.triggerDispatch()
	return e.store.GetJob(ctx, jobID)
}

func (e *Engine) triggerDispatch() {
	select {
	case e.notifyCh <- struct{}{}:
	default:
	}
}

// dispatchLoop 调度器主循环。
func (e *Engine) dispatchLoop() {
	defer e.wg.Done()

	ticker := time.NewTicker(e.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.tryDispatchBatch()
		case <-e.notifyCh:
			e.tryDispatchBatch()
		}
	}
}

// tryDispatchBatch 尝试拉取待执行任务并派发给 worker。
func (e *Engine) tryDispatchBatch() {
	for {
		// 检查 worker 池容量 (验收 8: 始终 <= workers)
		select {
		case e.workerSem <- struct{}{}:
			// 成功获取到一个 worker 槽位
		default:
			// worker 池已满，等待下次派发
			return
		}

		// 寻找下一个可调度的 candidate
		job := e.claimNextJob()
		if job == nil {
			// 没有可调度的任务，归还槽位并退出当前批次
			<-e.workerSem
			return
		}

		// 启动 worker 执行
		atomic.AddInt32(&e.activeWorkers, 1)
		e.wg.Add(1)
		go func(j *Job) {
			defer func() {
				e.releaseTargetLock(j)
				atomic.AddInt32(&e.activeWorkers, -1)
				<-e.workerSem
				e.wg.Done()
				e.triggerDispatch() // 唤醒调度器检查剩余任务
			}()

			e.runJob(j)
		}(job)
	}
}

// claimNextJob 寻找并原子认领一个符合并发条件的 pending 任务。
func (e *Engine) claimNextJob() *Job {
	candidates, err := e.store.FindPendingCandidates(e.ctx, 50)
	if err != nil || len(candidates) == 0 {
		return nil
	}

	for _, cand := range candidates {
		// 单实例互斥检查：同 target 不允许并发跑 (验收 4)
		if !e.tryAcquireTargetLock(cand) {
			continue
		}

		// 原子认领任务状态为 running
		claimed, err := e.store.ClaimJob(e.ctx, cand.ID)
		if err != nil || claimed == nil {
			// 认领失败（已被其他并发抢占或已被取消），释放锁
			e.releaseTargetLock(cand)
			continue
		}

		return claimed
	}

	return nil
}

func (e *Engine) tryAcquireTargetLock(j *Job) bool {
	if j.TargetKind == "" || j.TargetID == "" {
		return true // 无 target 不受互斥限制
	}

	targetKey := j.TargetKind + ":" + j.TargetID
	e.targetMu.Lock()
	defer e.targetMu.Unlock()

	if _, busy := e.runningTargets[targetKey]; busy {
		return false // 当前 target 正在被另一个 job 执行，排队等待
	}

	e.runningTargets[targetKey] = j.ID
	return true
}

func (e *Engine) releaseTargetLock(j *Job) {
	if j.TargetKind == "" || j.TargetID == "" {
		return
	}

	targetKey := j.TargetKind + ":" + j.TargetID
	e.targetMu.Lock()
	defer e.targetMu.Unlock()

	if owner, ok := e.runningTargets[targetKey]; ok && owner == j.ID {
		delete(e.runningTargets, targetKey)
	}
}

// runJob 执行单个任务的完整生命周期。
func (e *Engine) runJob(job *Job) {
	def, ok := e.registry.Get(job.JobKind)
	if !ok {
		e.failJob(job, fmt.Sprintf("unknown job kind: %s", job.JobKind))
		return
	}

	timeout := def.Timeout
	if timeout <= 0 {
		timeout = e.cfg.DefaultTimeout
	}

	// 构造带超时与取消能力的 Context (验收 5 & 6)
	runCtx, cancel := context.WithTimeout(e.ctx, timeout)
	defer cancel()

	e.cancelMu.Lock()
	e.activeCancels[job.ID] = cancel
	e.cancelMu.Unlock()

	defer func() {
		e.cancelMu.Lock()
		delete(e.activeCancels, job.ID)
		e.cancelMu.Unlock()
	}()

	// 广播 job running 状态
	e.broadcaster.Publish(job.ID, StreamEvent{
		Event: "job",
		Data: map[string]any{
			"id":            job.ID,
			"job_state":     StateRunning,
			"started_at_ms": job.StartedAtMs,
			"attempt":       job.Attempt,
		},
	})

	// 获取已存在的 steps 状态 (断点继续 / 幂等跳过，验收 2)
	steps, err := e.store.GetSteps(runCtx, job.ID)
	if err != nil {
		e.failJob(job, fmt.Sprintf("fetch job steps: %v", err))
		return
	}

	stepMap := make(map[int]*JobStep)
	for _, s := range steps {
		stepMap[s.StepIndex] = s
	}

	sharedData := make(map[string]any)
	var finalResult map[string]any

	for i, stepDef := range def.Steps {
		select {
		case <-runCtx.Done():
			e.handleContextDone(job, runCtx.Err(), timeout)
			return
		default:
		}

		st, exists := stepMap[i]
		if !exists {
			st = &JobStep{
				ID:        ulid.New(),
				JobID:     job.ID,
				StepIndex: i,
				Name:      stepDef.Name,
				StepState: StepPending,
			}
		}

		// ★ 断点续跑：若本步骤已成功执行过，直接跳过不重跑 (验收 2)
		if st.StepState == StepSucceeded {
			skipMsg := fmt.Sprintf("[断点跳过] 步骤 %d (%s) 此前已成功执行，跳过本步骤", i+1, stepDef.Name)
			logx.Info(skipMsg, "job_id", job.ID, "step", i)
			e.broadcaster.Publish(job.ID, StreamEvent{
				Event: "log",
				Data: map[string]any{
					"job_id":     job.ID,
					"step_index": i,
					"log":        skipMsg,
				},
			})
			continue
		}

		// 检查非幂等限制
		if !stepDef.Idempotent && job.Attempt > 1 && st.StepState != StepPending {
			e.failJob(job, fmt.Sprintf("step %d (%s) is not idempotent and cannot be retried", i+1, stepDef.Name))
			return
		}

		// 开始执行当前步骤
		now := time.Now().UnixMilli()
		st.StepState = StepRunning
		st.StartedAtMs = &now
		_ = e.store.UpdateStep(runCtx, st)

		e.broadcaster.Publish(job.ID, StreamEvent{
			Event: "step",
			Data:  st,
		})

		stepCtx := &StepContext{
			Context:    runCtx,
			JobID:      job.ID,
			JobKind:    job.JobKind,
			TargetKind: job.TargetKind,
			TargetID:   job.TargetID,
			Params:     job.Params,
			Attempt:    job.Attempt,
			StepIndex:  i,
			StepName:   stepDef.Name,
			Log: func(format string, args ...any) {
				msg := fmt.Sprintf(format, args...)
				st.LogText += msg + "\n"
				_ = e.store.AppendStepLog(context.Background(), st.ID, msg)
				e.broadcaster.Publish(job.ID, StreamEvent{
					Event: "log",
					Data: map[string]any{
						"job_id":     job.ID,
						"step_index": i,
						"log":        msg,
					},
				})
			},
			SetSharedData: func(k string, v any) {
				sharedData[k] = v
			},
			GetSharedData: func(k string) (any, bool) {
				v, ok := sharedData[k]
				return v, ok
			},
			SetResult: func(res map[string]any) {
				finalResult = res
			},
		}

		stepErr := stepDef.Run(stepCtx)
		finishNow := time.Now().UnixMilli()
		st.FinishedAtMs = &finishNow

		if stepErr != nil {
			st.StepState = StepFailed
			_ = e.store.UpdateStep(context.Background(), st)
			e.broadcaster.Publish(job.ID, StreamEvent{
				Event: "step",
				Data:  st,
			})

			if errors.Is(stepErr, context.Canceled) || runCtx.Err() == context.Canceled {
				e.cancelJobRecord(job)
				return
			}
			if errors.Is(stepErr, context.DeadlineExceeded) || runCtx.Err() == context.DeadlineExceeded {
				e.timeoutJobRecord(job, timeout)
				return
			}

			e.failJob(job, fmt.Sprintf("step %d (%s) failed: %v", i+1, stepDef.Name, stepErr))
			return
		}

		// 步骤执行成功
		st.StepState = StepSucceeded
		_ = e.store.UpdateStep(runCtx, st)
		e.broadcaster.Publish(job.ID, StreamEvent{
			Event: "step",
			Data:  st,
		})
	}

	// 所有步骤全部成功完成
	var resultJSON string
	if finalResult != nil {
		if d, err := json.Marshal(finalResult); err == nil {
			resultJSON = string(d)
		}
	}

	if err := e.store.UpdateJobTerminalState(context.Background(), job.ID, StateSucceeded, resultJSON, ""); err != nil {
		logx.Error("failed to update job succeeded state", "job_id", job.ID, "err", err)
	}

	e.broadcaster.Publish(job.ID, StreamEvent{
		Event: "job",
		Data: map[string]any{
			"id":          job.ID,
			"job_state":   StateSucceeded,
			"result_json": resultJSON,
		},
	})

	// 发出 job.succeeded 事件 (验收 7)
	events.Emit(context.Background(), events.Event{
		Type:       "job.succeeded",
		Source:     "jobs",
		TargetKind: job.TargetKind,
		TargetID:   job.TargetID,
		Title:      fmt.Sprintf("任务 %s 顺利完成", job.JobKind),
		Payload: map[string]any{
			"job_id":      job.ID,
			"job_kind":    job.JobKind,
			"target_kind": job.TargetKind,
			"target_id":   job.TargetID,
			"attempt":     job.Attempt,
		},
	})

	_ = audit.Log(context.Background(), e.db, audit.Entry{
		ActorKind:  "system",
		Action:     "job.succeeded",
		TargetKind: "job",
		TargetID:   job.ID,
		Detail:     fmt.Sprintf("kind=%s succeeded", job.JobKind),
		Result:     "ok",
	})
}

func (e *Engine) handleContextDone(job *Job, err error, timeout time.Duration) {
	if errors.Is(err, context.Canceled) {
		e.cancelJobRecord(job)
	} else if errors.Is(err, context.DeadlineExceeded) {
		e.timeoutJobRecord(job, timeout)
	}
}

func (e *Engine) cancelJobRecord(job *Job) {
	_ = e.store.UpdateJobTerminalState(context.Background(), job.ID, StateCancelled, "", "job cancelled by user")
	e.broadcaster.Publish(job.ID, StreamEvent{
		Event: "job",
		Data: map[string]any{
			"id":        job.ID,
			"job_state": StateCancelled,
		},
	})
}

func (e *Engine) timeoutJobRecord(job *Job, timeout time.Duration) {
	errMsg := fmt.Sprintf("job timed out after %v", timeout)
	e.failJob(job, errMsg)
}

func (e *Engine) failJob(job *Job, errMsg string) {
	_ = e.store.UpdateJobTerminalState(context.Background(), job.ID, StateFailed, "", errMsg)

	e.broadcaster.Publish(job.ID, StreamEvent{
		Event: "job",
		Data: map[string]any{
			"id":         job.ID,
			"job_state":  StateFailed,
			"error_text": errMsg,
		},
	})

	// 发出 job.failed 事件 (验收 7)
	events.Emit(context.Background(), events.Event{
		Type:       "job.failed",
		Source:     "jobs",
		TargetKind: job.TargetKind,
		TargetID:   job.TargetID,
		Title:      fmt.Sprintf("任务 %s 执行失败: %s", job.JobKind, errMsg),
		Payload: map[string]any{
			"job_id":      job.ID,
			"job_kind":    job.JobKind,
			"target_kind": job.TargetKind,
			"target_id":   job.TargetID,
			"attempt":     job.Attempt,
			"error":       errMsg,
		},
	})

	_ = audit.Log(context.Background(), e.db, audit.Entry{
		ActorKind:  "system",
		Action:     "job.failed",
		TargetKind: "job",
		TargetID:   job.ID,
		Detail:     fmt.Sprintf("kind=%s error=%s", job.JobKind, errMsg),
		Result:     "failed",
	})
}
