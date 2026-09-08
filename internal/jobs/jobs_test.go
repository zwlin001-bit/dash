package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/jobs"
	"dash/internal/migrate"
)

func setupTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("Skipping test: MySQL test DB not accessible: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, _ = d.Exec(ctx, "SELECT GET_LOCK('dash_jobs_test', 30)")
	t.Cleanup(func() {
		_, _ = d.Exec(context.Background(), "SELECT RELEASE_LOCK('dash_jobs_test')")
	})

	mig := migrate.New(d, "../../migrations")
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("migration up failed: %v", err)
	}

	// Clean tables
	_, _ = d.Exec(ctx, "DELETE FROM job_steps")
	_, _ = d.Exec(ctx, "DELETE FROM jobs")
	_, _ = d.Exec(ctx, "DELETE FROM events")
	_, _ = d.Exec(ctx, "DELETE FROM audit_log")

	return d
}

// 验收 1: 注册一个测试 job kind，跑 3 个 step → 列表页能看到实时进度
func TestAcceptance1_ThreeStepsProgress(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	reg := jobs.NewRegistry()
	cfg := jobs.Config{
		Workers:        2,
		DefaultTimeout: 10 * time.Second,
		PollInterval:   20 * time.Millisecond,
	}
	engine := jobs.NewEngine(d, reg, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine failed: %v", err)
	}
	defer engine.Stop()

	// 提交内置 test.three_steps 任务
	job, err := engine.Submit(ctx, jobs.SubmitRequest{
		Kind: "test.three_steps",
		Params: map[string]any{
			"step1_delay_ms": 50,
			"step2_delay_ms": 50,
			"step3_delay_ms": 50,
		},
	})
	if err != nil {
		t.Fatalf("submit job failed: %v", err)
	}

	// 订阅 SSE 进度流
	subCh, unsub := engine.Broadcaster().Subscribe(job.ID)
	defer unsub()

	var receivedEvents []string
	done := make(chan struct{})

	go func() {
		for ev := range subCh {
			receivedEvents = append(receivedEvents, ev.Event)
			if ev.Event == "job" {
				if m, ok := ev.Data.(map[string]any); ok {
					if m["job_state"] == jobs.StateSucceeded {
						close(done)
						return
					}
				}
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("job timed out without completing")
	}

	// 验证数据库状态
	finalJob, err := engine.Store().GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job failed: %v", err)
	}
	if finalJob.JobState != jobs.StateSucceeded {
		t.Fatalf("expected state succeeded, got %s", finalJob.JobState)
	}
	if len(finalJob.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(finalJob.Steps))
	}
	for i, st := range finalJob.Steps {
		if st.StepState != jobs.StepSucceeded {
			t.Fatalf("step %d expected succeeded, got %s", i, st.StepState)
		}
		if st.LogText == "" {
			t.Fatalf("step %d missing log text", i)
		}
	}
	if len(receivedEvents) == 0 {
		t.Fatalf("expected to receive stream events")
	}
}

// 验收 2: 让第 2 个 step 失败 → 重试时从第 2 步开始，第 1 步不重跑（日志能证明）
func TestAcceptance2_IdempotentStepRetry(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	reg := jobs.NewRegistry()
	cfg := jobs.Config{
		Workers:        2,
		DefaultTimeout: 10 * time.Second,
		PollInterval:   20 * time.Millisecond,
	}
	engine := jobs.NewEngine(d, reg, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine failed: %v", err)
	}
	defer engine.Stop()

	// 步骤 2 仅初次失败，重试成功
	job, err := engine.Submit(ctx, jobs.SubmitRequest{
		Kind: "test.three_steps",
		Params: map[string]any{
			"step1_delay_ms":    20,
			"step2_delay_ms":    20,
			"step3_delay_ms":    20,
			"fail_once_at_step": 2,
		},
	})
	if err != nil {
		t.Fatalf("submit job failed: %v", err)
	}

	// 等待初次失败
	var failedJob *jobs.Job
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		j, _ := engine.Store().GetJob(ctx, job.ID)
		if j != nil && j.JobState == jobs.StateFailed {
			failedJob = j
			break
		}
	}
	if failedJob == nil {
		t.Fatalf("job did not fail as expected")
	}

	// 验证：步骤 1 成功，步骤 2 失败
	if failedJob.Steps[0].StepState != jobs.StepSucceeded {
		t.Fatalf("step 1 should be succeeded, got %s", failedJob.Steps[0].StepState)
	}
	if failedJob.Steps[1].StepState != jobs.StepFailed {
		t.Fatalf("step 2 should be failed, got %s", failedJob.Steps[1].StepState)
	}
	step1FinishTime := *failedJob.Steps[0].FinishedAtMs

	// 触发重试 (验收 2)
	retriedJob, err := engine.Retry(ctx, job.ID)
	if err != nil {
		t.Fatalf("retry job failed: %v", err)
	}
	if retriedJob.JobState != jobs.StatePending {
		t.Fatalf("expected state pending on retry, got %s", retriedJob.JobState)
	}

	// 等待重试完成
	var finalJob *jobs.Job
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		j, _ := engine.Store().GetJob(ctx, job.ID)
		if j != nil && j.JobState == jobs.StateSucceeded {
			finalJob = j
			break
		}
	}
	if finalJob == nil {
		t.Fatalf("retried job did not succeed")
	}

	// 验证：第 1 步没有被重跑（finished_at_ms 不变）
	if *finalJob.Steps[0].FinishedAtMs != step1FinishTime {
		t.Fatalf("step 1 was re-executed! old finish: %d, new finish: %d", step1FinishTime, *finalJob.Steps[0].FinishedAtMs)
	}
	// 验证：第 2 步和第 3 步均成功
	if finalJob.Steps[1].StepState != jobs.StepSucceeded {
		t.Fatalf("step 2 should be succeeded after retry, got %s", finalJob.Steps[1].StepState)
	}
	if finalJob.Steps[2].StepState != jobs.StepSucceeded {
		t.Fatalf("step 3 should be succeeded after retry, got %s", finalJob.Steps[2].StepState)
	}
}

// 验收 3: ★ job 跑到一半 kill -9 dashd → 重启后该 job 被恢复，不是永远 running
func TestAcceptance3_CrashRecovery(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	reg := jobs.NewRegistry()
	step2Started := make(chan struct{})
	blockStep2 := make(chan struct{})

	reg.Register(jobs.JobDefinition{
		Kind: "test.crash_recovery",
		Steps: []jobs.StepDef{
			{
				Name:       "Step 1 Fast",
				Idempotent: true,
				Run: func(ctx *jobs.StepContext) error {
					ctx.Log("Step 1 done")
					return nil
				},
			},
			{
				Name:       "Step 2 Hang",
				Idempotent: true,
				Run: func(ctx *jobs.StepContext) error {
					close(step2Started)
					<-blockStep2 // 模拟正在执行并被强杀
					return nil
				},
			},
		},
	})

	cfg := jobs.Config{
		Workers:        2,
		DefaultTimeout: 10 * time.Second,
		PollInterval:   20 * time.Millisecond,
	}

	engine1 := jobs.NewEngine(d, reg, cfg)
	ctx1, cancel1 := context.WithCancel(context.Background())
	if err := engine1.Start(ctx1); err != nil {
		t.Fatalf("start engine1 failed: %v", err)
	}

	job, err := engine1.Submit(ctx1, jobs.SubmitRequest{
		Kind: "test.crash_recovery",
	})
	if err != nil {
		t.Fatalf("submit job failed: %v", err)
	}

	// 等待 Step 2 正在运行
	select {
	case <-step2Started:
	case <-time.After(3 * time.Second):
		t.Fatalf("step 2 did not start")
	}

	// 验证此时任务在 DB 中为 running
	dbJob, _ := engine1.Store().GetJob(ctx1, job.ID)
	if dbJob.JobState != jobs.StateRunning {
		t.Fatalf("expected running, got %s", dbJob.JobState)
	}

	// ★ 模拟 kill -9 dashd：进程瞬间中止，数据库保留当时的 running 现场
	cancel1()
	close(blockStep2)
	engine1.Stop()

	// 确保数据库现场为当时的 running 状态（模拟崩溃断电）
	_, _ = d.Exec(context.Background(), "UPDATE jobs SET job_state = 'running', finished_at_ms = NULL WHERE id = ?", job.ID)
	_, _ = d.Exec(context.Background(), "UPDATE job_steps SET step_state = 'running', finished_at_ms = NULL WHERE job_id = ? AND step_index = 1", job.ID)

	// 确认数据库里此时处于 running 僵尸状态
	dbJobAfterKill, _ := engine1.Store().GetJob(context.Background(), job.ID)
	if dbJobAfterKill.JobState != jobs.StateRunning {
		t.Fatalf("expected running before recovery, got %s", dbJobAfterKill.JobState)
	}

	// ★ 重启新进程 (dashd 重启，创建新 engine2 并启动)
	reg2 := jobs.NewRegistry()
	reg2.Register(jobs.JobDefinition{
		Kind: "test.crash_recovery",
		Steps: []jobs.StepDef{
			{
				Name:       "Step 1 Fast",
				Idempotent: true,
				Run: func(ctx *jobs.StepContext) error {
					return nil
				},
			},
			{
				Name:       "Step 2 Hang",
				Idempotent: true,
				Run: func(ctx *jobs.StepContext) error {
					ctx.Log("Step 2 completed after crash recovery!")
					return nil
				},
			},
		},
	})

	engine2 := jobs.NewEngine(d, reg2, cfg)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	if err := engine2.Start(ctx2); err != nil {
		t.Fatalf("engine2 start failed: %v", err)
	}
	defer engine2.Stop()

	// 等待恢复后的任务自动执行并完成
	var recoveredJob *jobs.Job
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		j, _ := engine2.Store().GetJob(ctx2, job.ID)
		if j != nil && j.JobState == jobs.StateSucceeded {
			recoveredJob = j
			break
		}
	}
	if recoveredJob == nil {
		t.Fatalf("interrupted job was not recovered and completed! state: %v", dbJobAfterKill.JobState)
	}

	if recoveredJob.JobState != jobs.StateSucceeded {
		t.Fatalf("expected succeeded after recovery, got %s", recoveredJob.JobState)
	}
}

// 验收 4: 对同一 target 并发提交两个同类 job → 第二个被拒绝或排队，绝不同时跑
func TestAcceptance4_SingleInstanceTargetConcurrency(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	reg := jobs.NewRegistry()
	var runningCount int32
	var maxSimultaneousOnTarget int32

	reg.Register(jobs.JobDefinition{
		Kind: "test.target_lock",
		Steps: []jobs.StepDef{
			{
				Name:       "Mutual Exclude Step",
				Idempotent: true,
				Run: func(ctx *jobs.StepContext) error {
					curr := atomic.AddInt32(&runningCount, 1)
					if curr > atomic.LoadInt32(&maxSimultaneousOnTarget) {
						atomic.StoreInt32(&maxSimultaneousOnTarget, curr)
					}
					time.Sleep(100 * time.Millisecond)
					atomic.AddInt32(&runningCount, -1)
					return nil
				},
			},
		},
	})

	cfg := jobs.Config{
		Workers:        4,
		DefaultTimeout: 10 * time.Second,
		PollInterval:   20 * time.Millisecond,
	}
	engine := jobs.NewEngine(d, reg, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine failed: %v", err)
	}
	defer engine.Stop()

	// Part A: RejectIfBusy 模式 → 第二个直接被拒绝 (ErrTargetBusy)
	j1, err := engine.Submit(ctx, jobs.SubmitRequest{
		Kind:         "test.target_lock",
		TargetKind:   "ecs",
		TargetID:     "i-target-001",
		RejectIfBusy: true,
	})
	if err != nil {
		t.Fatalf("submit j1 failed: %v", err)
	}

	// 紧接着提交第二个同 target 任务，要求 reject_if_busy
	_, err2 := engine.Submit(ctx, jobs.SubmitRequest{
		Kind:         "test.target_lock",
		TargetKind:   "ecs",
		TargetID:     "i-target-001",
		RejectIfBusy: true,
	})
	if !errors.Is(err2, jobs.ErrTargetBusy) {
		t.Fatalf("expected ErrTargetBusy, got %v", err2)
	}

	// 等待 j1 完成
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		j, _ := engine.Store().GetJob(ctx, j1.ID)
		if j != nil && j.JobState == jobs.StateSucceeded {
			break
		}
	}

	// Part B: Queue 模式 → 并发提交两个同 target 任务，绝不同时跑
	atomic.StoreInt32(&runningCount, 0)
	atomic.StoreInt32(&maxSimultaneousOnTarget, 0)

	jA, errA := engine.Submit(ctx, jobs.SubmitRequest{
		Kind:       "test.target_lock",
		TargetKind: "ecs",
		TargetID:   "i-target-queue",
	})
	jB, errB := engine.Submit(ctx, jobs.SubmitRequest{
		Kind:       "test.target_lock",
		TargetKind: "ecs",
		TargetID:   "i-target-queue",
	})
	if errA != nil || errB != nil {
		t.Fatalf("submit queue jobs failed: %v, %v", errA, errB)
	}

	// 等待两个任务全部成功
	for i := 0; i < 60; i++ {
		time.Sleep(30 * time.Millisecond)
		jAState, _ := engine.Store().GetJob(ctx, jA.ID)
		jBState, _ := engine.Store().GetJob(ctx, jB.ID)
		if jAState != nil && jAState.JobState == jobs.StateSucceeded &&
			jBState != nil && jBState.JobState == jobs.StateSucceeded {
			break
		}
	}

	// 确认在同一个 target 上同时运行的最大任务数始终严格 <= 1
	maxSim := atomic.LoadInt32(&maxSimultaneousOnTarget)
	if maxSim > 1 {
		t.Fatalf("violation: concurrent execution on same target! max was %d", maxSim)
	}
}

// 验收 5: 提交一个死循环 job → 超时后被终止并标记 failed
func TestAcceptance5_JobTimeout(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	reg := jobs.NewRegistry()
	reg.Register(jobs.JobDefinition{
		Kind:    "test.infinite_loop",
		Timeout: 200 * time.Millisecond, // 测试环境设置 200ms
		Steps: []jobs.StepDef{
			{
				Name:       "Infinite Loop Step",
				Idempotent: true,
				Run: func(ctx *jobs.StepContext) error {
					// 持续死循环或阻塞等待
					<-ctx.Done()
					return ctx.Err()
				},
			},
		},
	})

	cfg := jobs.Config{
		Workers:        2,
		DefaultTimeout: 200 * time.Millisecond,
		PollInterval:   20 * time.Millisecond,
	}
	engine := jobs.NewEngine(d, reg, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine failed: %v", err)
	}
	defer engine.Stop()

	job, err := engine.Submit(ctx, jobs.SubmitRequest{
		Kind: "test.infinite_loop",
	})
	if err != nil {
		t.Fatalf("submit job failed: %v", err)
	}

	// 等待超时终止
	var timedOutJob *jobs.Job
	for i := 0; i < 50; i++ {
		time.Sleep(30 * time.Millisecond)
		j, _ := engine.Store().GetJob(ctx, job.ID)
		if j != nil && j.JobState == jobs.StateFailed {
			timedOutJob = j
			break
		}
	}
	if timedOutJob == nil {
		t.Fatalf("job was not terminated on timeout")
	}

	if timedOutJob.JobState != jobs.StateFailed {
		t.Fatalf("expected state failed, got %s", timedOutJob.JobState)
	}
	if timedOutJob.Steps[0].StepState != jobs.StepFailed {
		t.Fatalf("expected step state failed, got %s", timedOutJob.Steps[0].StepState)
	}
}

// 验收 6: 取消一个正在跑的 job → 进程内真的停下来（不是只改状态）
func TestAcceptance6_JobCancellation(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	reg := jobs.NewRegistry()
	stepStarted := make(chan struct{})
	stepActuallyStopped := make(chan struct{})

	reg.Register(jobs.JobDefinition{
		Kind: "test.cancellation",
		Steps: []jobs.StepDef{
			{
				Name:       "Cancellable Step",
				Idempotent: true,
				Run: func(ctx *jobs.StepContext) error {
					close(stepStarted)
					select {
					case <-ctx.Done():
						close(stepActuallyStopped)
						return ctx.Err()
					case <-time.After(10 * time.Second):
						return nil
					}
				},
			},
		},
	})

	cfg := jobs.Config{
		Workers:        2,
		DefaultTimeout: 10 * time.Second,
		PollInterval:   20 * time.Millisecond,
	}
	engine := jobs.NewEngine(d, reg, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine failed: %v", err)
	}
	defer engine.Stop()

	job, err := engine.Submit(ctx, jobs.SubmitRequest{
		Kind: "test.cancellation",
	})
	if err != nil {
		t.Fatalf("submit job failed: %v", err)
	}

	// 等待 Step 正在跑
	select {
	case <-stepStarted:
	case <-time.After(3 * time.Second):
		t.Fatalf("step did not start")
	}

	// 触发取消 (验收 6)
	if err := engine.Cancel(ctx, job.ID); err != nil {
		t.Fatalf("cancel job failed: %v", err)
	}

	// 验证进程内 Context 真的被取消，goroutine 真的停下来
	select {
	case <-stepActuallyStopped:
		// 验证通过
	case <-time.After(2 * time.Second):
		t.Fatalf("step was not stopped in process! context cancel was ignored")
	}

	// 验证数据库状态
	finalJob, _ := engine.Store().GetJob(ctx, job.ID)
	if finalJob.JobState != jobs.StateCancelled {
		t.Fatalf("expected state cancelled, got %s", finalJob.JobState)
	}
}

// 验收 7: job.failed / job.succeeded 事件发出
func TestAcceptance7_EventsAndAudit(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	// 初始化全局事件总线
	eventStore := events.NewStore(d, 256)
	eventStore.Start()
	events.SetDefaultStore(eventStore)
	defer eventStore.Stop()

	reg := jobs.NewRegistry()
	reg.Register(jobs.JobDefinition{
		Kind: "test.fail_event",
		Steps: []jobs.StepDef{
			{
				Name:       "Fail Step",
				Idempotent: true,
				Run: func(ctx *jobs.StepContext) error {
					return errors.New("deliberate failure for event check")
				},
			},
		},
	})

	cfg := jobs.Config{
		Workers:        2,
		DefaultTimeout: 5 * time.Second,
		PollInterval:   20 * time.Millisecond,
	}
	engine := jobs.NewEngine(d, reg, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine failed: %v", err)
	}
	defer engine.Stop()

	job, err := engine.Submit(ctx, jobs.SubmitRequest{
		Kind:       "test.fail_event",
		TargetKind: "node",
		TargetID:   "node-test-01",
	})
	if err != nil {
		t.Fatalf("submit job failed: %v", err)
	}

	// 等待失败
	for i := 0; i < 50; i++ {
		time.Sleep(30 * time.Millisecond)
		j, _ := engine.Store().GetJob(ctx, job.ID)
		if j != nil && j.JobState == jobs.StateFailed {
			break
		}
	}

	// 等待事件总线 flush
	time.Sleep(100 * time.Millisecond)

	// 验证 events 表中记录了 job.failed
	rows, err := d.Query(ctx, "SELECT event_type, title, source_module FROM events WHERE event_type = 'job.failed'")
	if err != nil {
		t.Fatalf("query events failed: %v", err)
	}
	defer rows.Close()

	var eventCount int
	for rows.Next() {
		var et, title, src string
		if err := rows.Scan(&et, &title, &src); err != nil {
			t.Fatalf("scan event failed: %v", err)
		}
		if src != "jobs" {
			t.Fatalf("expected source 'jobs', got %s", src)
		}
		eventCount++
	}
	if eventCount == 0 {
		t.Fatalf("expected job.failed event to be recorded in events table")
	}

	// 验证 audit_log 记录
	var auditCount int
	_ = d.QueryRow(ctx, "SELECT COUNT(*) FROM audit_log WHERE target_id = ?", job.ID).Scan(&auditCount)
	if auditCount == 0 {
		t.Fatalf("expected audit_log record for job")
	}
}

// 验收 8: 并发提交 50 个 job，worker=4 → 同时运行数始终 ≤ 4
func TestAcceptance8_WorkerPoolLimit(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	reg := jobs.NewRegistry()
	var currentWorkers int32
	var maxObservedWorkers int32

	reg.Register(jobs.JobDefinition{
		Kind: "test.pool_capacity",
		Steps: []jobs.StepDef{
			{
				Name:       "Pool Test Step",
				Idempotent: true,
				Run: func(ctx *jobs.StepContext) error {
					c := atomic.AddInt32(&currentWorkers, 1)
					for {
						m := atomic.LoadInt32(&maxObservedWorkers)
						if c <= m {
							break
						}
						if atomic.CompareAndSwapInt32(&maxObservedWorkers, m, c) {
							break
						}
					}

					time.Sleep(20 * time.Millisecond)
					atomic.AddInt32(&currentWorkers, -1)
					return nil
				},
			},
		},
	})

	cfg := jobs.Config{
		Workers:        4,
		DefaultTimeout: 30 * time.Second,
		PollInterval:   10 * time.Millisecond,
	}
	engine := jobs.NewEngine(d, reg, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine failed: %v", err)
	}
	defer engine.Stop()

	// 并发提交 50 个任务
	const jobCount = 50
	var wg sync.WaitGroup
	var submittedJobs []*jobs.Job
	var subMu sync.Mutex

	for i := 0; i < jobCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			j, err := engine.Submit(ctx, jobs.SubmitRequest{
				Kind:       "test.pool_capacity",
				TargetKind: "node",
				TargetID:   fmt.Sprintf("node-worker-%d", idx), // 确保不同 target
			})
			if err != nil {
				t.Errorf("submit %d failed: %v", idx, err)
				return
			}
			subMu.Lock()
			submittedJobs = append(submittedJobs, j)
			subMu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(submittedJobs) != jobCount {
		t.Fatalf("expected %d submitted jobs, got %d", jobCount, len(submittedJobs))
	}

	// 等待全部 50 个任务完成
	for {
		var pendingOrRunning int
		_ = d.QueryRow(ctx, "SELECT COUNT(*) FROM jobs WHERE job_state IN ('pending', 'running')").Scan(&pendingOrRunning)
		if pendingOrRunning == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 确认最大并发数始终 <= 4
	maxObs := atomic.LoadInt32(&maxObservedWorkers)
	t.Logf("Observed max concurrent workers: %d (configured limit: 4)", maxObs)
	if maxObs > 4 {
		t.Fatalf("worker pool limit exceeded! max observed: %d, allowed: 4", maxObs)
	}
	if maxObs < 2 {
		t.Fatalf("worker pool did not run concurrently, max was %d", maxObs)
	}
}
