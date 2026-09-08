# P2-03 Job 引擎

> 第二期 · 任务 03 ｜ 前置：20 ✅ ｜ 分支：`agy/p2-03-jobs`

> ★ **完成后必须自己 `git push -u origin <分支名>`**，并确认
> `git log --oneline -1 origin/<分支名>` 能看到你的提交。只提交到本地等于没交付。

## 为什么必须先有它

`PRINCIPLES.md` P4.4 是硬性约束：

> 所有耗时/易失败的操作（建机器、装代理、同步云资源）走**统一的 Job 引擎**：
> 可持久化、可重试、可恢复、有审计。**不允许在 HTTP 请求里同步干这些事。**

P2-02 的「同步云资源」和 P2-04 的「启停 ECS 并等待状态确认」都是这类操作 ——
启停一台 ECS 要等 45–90 秒才能确认到位，**在 HTTP 请求里干这个必然超时**。

★ **不要让 P2-02 和 P2-04 各自造一个小调度器。** 那会造出两套重试语义、
两套失败处理，以后再合并的代价远大于现在做一次。

## 目标

一个够用的 job 引擎：**可持久化、可恢复、可重试、可观测。**

## 交付物

```
migrations/0004_jobs.{mysql,oracle}.sql
internal/jobs/            引擎、注册表、worker 池、恢复逻辑
internal/api/jobs/        job 查询与取消 API + SSE 进度推送
web/src/pages/Jobs/       任务列表与详情
```

## 约束

### 表

照 `02-database.md` §5：

```
jobs(id, job_kind str(48), job_state str(16),         -- pending/running/succeeded/failed/cancelled
     target_kind str(32) NULL, target_id str(26) NULL,
     params_json text NULL, result_json text NULL,
     error_text text NULL,
     attempt i32, max_attempt i32,
     scheduled_at_ms ts, started_at_ms ts NULL, finished_at_ms ts NULL,
     created_at_ms, updated_at_ms)
  ix_jobs_state_sched (job_state, scheduled_at_ms)
  ix_jobs_target (target_kind, target_id)

job_steps(id, job_id, step_index i32, name str(64),
          step_state str(16), log_text text NULL,
          started_at_ms ts NULL, finished_at_ms ts NULL)
  ix_job_steps_job (job_id, step_index)
```

### 核心语义

- ★ **每个 step 必须幂等**：失败重试从断点继续，**已完成的 step 不重跑**。
  这是「启停 ECS」能安全重试的前提 —— 重复调 `StartInstance` 是安全的，
  但「扣一次费」这类操作不是，registry 里要能声明 step 是否可重入
- 进程重启后，`running` 状态的 job 要能**被认领回来继续跑**（或标记为需重试），
  不许变成永远卡住的僵尸
- ★ **单实例执行**：同一个 `target_kind`+`target_id` 上同类 job **不许并发**。
  两个 job 同时对一台 ECS 一个开一个关是灾难
- 取消：`cancelled` 状态要能中断正在跑的 job（context 取消），不是只改个标记

### 资源预算

★ **worker 池大小可配，默认 4。** `PRINCIPLES.md` P4.5 允许服务端吃得多一点，
但不是无限——一个 job 卡住不能拖垮整个引擎，每个 job 要有**总超时**（默认 10 分钟）。

### 可观测

- 进度和日志走 SSE 推前端（`GET /api/v1/jobs/{id}/stream`），复用任务 13 的 SSE 基建
- job 终态发事件：`job.succeeded` / `job.failed`，接到 P2-01 的通知管道
- 每个 job 的关键动作写 `audit_log`

## 验收

1. 注册一个测试 job kind，跑 3 个 step → 列表页能看到实时进度
2. 让第 2 个 step 失败 → 重试时**从第 2 步开始**，第 1 步不重跑（日志能证明）
3. ★ job 跑到一半 `kill -9` dashd → 重启后该 job **被恢复**，不是永远 running
4. 对同一 target 并发提交两个同类 job → **第二个被拒绝或排队**，绝不同时跑
5. 提交一个死循环 job → 10 分钟后**被超时终止**并标记 failed
6. 取消一个正在跑的 job → 进程内真的停下来（不是只改状态）
7. `job.failed` 事件能推到 Telegram（需 P2-01，未合并时用桩验证 `Emit` 被调用）
8. 并发提交 50 个 job，worker=4 → **同时运行数始终 ≤ 4**

## 边界

- 不做 cron 式定时 job（保活自己有循环，见 P2-04）
- 不做跨节点分布式调度，单 dashd 进程即可

---

# 验收记录

## 第 1 轮 · 2026-09-08 · ✅ 通过

分支 `agy/p2-03-jobs`，提交 `093e03f`。

八条验收项**每条都有同名测试**（`TestAcceptance1_…` ~ `TestAcceptance8_…`），全部实跑通过。
抽查了断言强度，不是走过场：

| 验收项 | 实测 |
|---|---|
| 1 三步进度 | ✅ |
| 2 断点重试 | ✅ **断言的是 `Steps[0].FinishedAtMs` 前后不变** —— 真验了第 1 步没重跑，不是只看最终状态 |
| 3 崩溃恢复 | ✅ `TestAcceptance3_CrashRecovery` |
| 4 同 target 不并发 | ✅ 断言 `errors.Is(err2, jobs.ErrTargetBusy)`，且覆盖了 RejectIfBusy 与排队两种模式 |
| 5 总超时 | ✅ |
| 6 取消 | ✅ |
| 7 事件与审计 | ✅ `engine.go:90,97` 发 `job.succeeded`/`job.failed`；`engine.go:177,256` 写 `audit_log` |
| 8 worker 池上限 | ✅ 并发提交 50 个，断言 `maxObs > 4` 失败**且** `maxObs` 必须 > 1（防止「串行跑完也算过」） |

其他：
- 迁移用 `0004_jobs`，与 P2-01 的 `0003`、P2-02 的 `0005` 正好错开
- SQL 方言 lint、import 纪律 lint、`go vet` 全过
- 前端 `Jobs.tsx` 对 `jobs`/`steps` 都做了空值守卫，不会重演 P1-27 的崩页

### 次要 · 列表信封形状

`GET /api/v1/jobs` 返回 `{jobs, total}`，`12-api-spec.md` §3 要求 `{items, total, page, page_size}`。
`ListJobs` 在 `total == 0` 时提前返回 `[]*Job{}`，前端也有 `!jobsData?.jobs` 守卫，
**不会崩**，但形状与规格不一致。与 P2-01 的同类偏差**一并并入 P1-27 的对账**。

**任务 P2-03 关闭。**
