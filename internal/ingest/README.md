# internal/ingest

指标数据接收、批量聚合、时序降采样（Rollup）与生命周期保留清理。

## 职责边界

- 接收来自 agent 上报的时序指标数据（WebSocket 与 HTTP 回退端点）。
- 内存缓冲并执行批量落库（任务 11）。
- 定时驱动分级 rollup 聚合（1m / 1h / 1d 降采样），按 `10-schema-spec.md` 展开 44 列并进行加权平均计算。
- 启动时自动补跑停机期间缺失的 rollup 桶（分 1 小时块推进）。
- 执行数据生命周期保留期清理（从 `settings` 表读取天数，按 1 小时有界区间分块 DELETE，不使用 limit 语句，每轮间歇休眠以防长事务与 undo 膨胀）。

## 对外接口

- `RollupService`：分级 Rollup 核心服务。
  - `Rollup1m(ctx, fromMs, toMs)`：1 分钟降采样（raw → 1m）。
  - `Rollup1h(ctx, fromMs, toMs)`：1 小时降采样（1m → 1h，加权平均）。
  - `Rollup1d(ctx, fromMs, toMs)`：1 天降采样（1h → 1d，加权平均）。
  - `CatchUp(ctx, nowMs)`：启动时补跑历史缺失桶。
- `RetentionService`：保留期生命周期清理服务。
  - `Clean(ctx, nowMs)`：根据配置执行全表清理。
  - `CleanTable(ctx, table, timeCol, cutoffMs)`：单表分块清理。
- `Scheduler`：内部任务定时调度器。
  - 1m：每分钟第 10 秒（窗口 `[上一分钟起点, 上一分钟终点)`）。
  - 1h：每小时第 2 分钟（窗口 `[上一小时起点, 上一小时终点)`）。
  - 1d：每天 00:05 UTC（窗口 `[昨天 00:00, 今天 00:00)`）。
  - 清理：每小时执行一次。
  - `Start(ctx)` / `Stop()`：调度器生命周期管理。

## 依赖谁

- `internal/db`（事务与方言执行）
- `internal/protocol`
- `internal/logx`
