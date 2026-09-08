# internal/jobs

Job 任务调度与执行引擎（P2-03）。

## 职责边界

- 提供通用异步任务运行时：可持久化、可断点恢复、可重试、可观测。
- 保证每个 Step 的幂等执行，失败重试从断点继续，已完成的 Step 绝不重跑。
- 单实例互斥控制：同一 `target_kind` + `target_id` 上的同类任务互斥排队或拒绝，禁止并发冲突。
- 崩溃恢复：服务端崩溃/重启（如 `kill -9`）后，`running` 状态任务自动认领并恢复执行，消除僵尸任务。
- 可控资源消耗：固定容量的 Worker 池（默认 4），单任务总超时保护（默认 10 分钟）。
- 实时推送与审计：支持 SSE 进度与日志流式推送，终态发布 `job.succeeded` 与 `job.failed` 事件并记录审计日志。

## 对外接口

- `Engine`:
  - `NewEngine(db, registry, cfg)`: 创建引擎实例
  - `Start(ctx) error` / `Stop()`: 启动（含崩溃恢复与调度循环）与优雅停止
  - `Submit(ctx, req SubmitRequest) (*Job, error)`: 提交任务
  - `Cancel(ctx, jobID string) error`: 取消正在运行或待处理的任务
  - `Retry(ctx, jobID string) (*Job, error)`: 重试失败或已取消的任务
  - `Store()`: 获取底层数据库 Store
  - `Registry()`: 获取任务类型注册表
  - `Broadcaster()`: 获取实时事件广播通道
- `Registry`:
  - `Register(def JobDefinition)`: 注册任务定义及各步骤处理函数
  - `Get(kind string) (JobDefinition, bool)`: 获取已注册定义
  - `List() []JobDefinition`: 列出所有任务类型

## 依赖谁

- `dash/internal/db`: 数据库操作与事务支持
- `dash/internal/events`: 终态事件发布（`job.succeeded` / `job.failed`）
- `dash/internal/audit`: 关键生命周期动作的审计日志记录
- `dash/internal/logx`: 结构化脱敏日志
- `dash/internal/ulid`: 主键 ULID 生成
