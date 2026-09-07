# internal/ingest

指标数据接收、批量落库、流量计数、时序降采样（Rollup）与生命周期保留清理。

## 职责边界

- 接收来自 agent 上报的时序指标数据（WebSocket 与 HTTP 回退端点，通过 `Ingest` 入口）。
- 内存环形缓冲与双缓冲刷写机制（`RingBuffer`，默认容量 4096 行，满载淘汰最老数据，永不阻塞采集链路）。
- 定时（每 5 秒）或定量（达到 80% 缓冲水位）批量落库至 ADB（`Writer`），单次 flush 严格限制为两条批量语句（`sample_host` 与 `sample_dim`）。
- 批量落库失败重试 3 次（1s / 2s / 4s 退避），仍失败则丢弃最老批次并受限触发 `system.db_unreachable` 告警事件，丢批与丢行计数暴露于 `/healthz`。
- 纯内存最新值缓存（`LatestCache`），供前端实时监控视图直接读取，完全不查数据库，在数据库断连期间仍全程可用。
- 计数器 reset 感知与启动恢复（`CounterManager`，`delta = cur >= prev ? cur - prev : cur`），服务启动时从 `sample_host` 最近一小时恢复 prev；无 prev 时写 NULL，防止重启虚假巨大增量。
- 维度指标映射与内存缓存（`SeriesManager`），`(node_id, metric_code, dim_key) → series_id`，首次出现时 Upsert 入库，稳态下不产生任何时序元数据查询。
- 定时驱动分级 rollup 聚合（1m / 1h / 1d 降采样，展开 44 列并进行加权平均计算，`RollupService`）。
- 启动时自动补跑停机期间缺失的 rollup 桶（`CatchUp`）。
- 执行数据生命周期保留期清理（按 1 小时有界区间分块 DELETE，不使用 limit 语句，`RetentionService`）。

## 对外接口

- `Service`：时序接收与批量落库核心协调服务。
  - `Ingest(nodeID, effectiveTs, m)`：接收 Agent 指标上报，计算流量增量、刷新最新值缓存、提取维度时序并推入环形缓冲。
  - `Latest()`：获取纯内存最新值缓存（`LatestCache`）。
  - `Buffer()`：获取环形双缓冲区（`RingBuffer`）。
  - `Writer()`：获取批量落库写入器（`Writer`）。
  - `Counter()`：获取计数器状态管理器（`CounterManager`）。
  - `Series()`：获取维度时序序列管理器（`SeriesManager`）。
  - `DroppedBatches()` / `DroppedRows()`：获取累计掉批与丢行指标。
  - `HandleHealthz(w, r)`：HTTP GET `/healthz` 端点处理器，返回 JSON 状态与丢弃统计。
  - `Start(ctx)` / `Stop()`：服务启动与优雅终止。
- `RingBuffer`：定长环形双缓冲区。
  - `Push(host, dims) (dropped bool)`：无锁或微秒级入队，满则淘汰最老数据。
  - `Swap() *Batch`：原子取走待落库批次并重置缓冲。
- `LatestCache`：内存最新值缓存。
  - `Get(nodeID)` / `Set(nodeID, latest)`：读写单节点最新数据。
  - `UpdateFromMetrics(nodeID, m, deltaUp, deltaDown)`：增量更新并触发变更回调。
  - `OnUpdate(fn)`：注册最新值变更监听器（供 SSE 实时流广播对接）。
- `CounterManager`：网络流量累计计数器管理。
  - `RecoverAll(ctx, nowMs)`：启动时从库中恢复最近 1 小时各节点 prev。
  - `CalculateDelta(ctx, nodeID, tsMs, curUp, curDown)`：计算重置感知后的流量增量。
- `SeriesManager`：维度序列映射管理器。
  - `GetOrCreate(ctx, nodeID, metricCode, dimKey, tsMs)`：读内存缓存或首现回库 Upsert。
- `RollupService`：分级 Rollup 核心服务（1m / 1h / 1d）。
- `RetentionService`：保留期生命周期清理服务。
- `Scheduler`：定时调度器（驱动 Rollup 与 Retention）。

## 依赖谁

- `internal/db`（BatchInsert 批量写入、Upsert 与方言执行）
- `internal/protocol`（协议结构体定义）
- `internal/events`（发布 `system.db_unreachable` 事件）
- `internal/logx`（脱敏与结构化日志）
- `internal/ulid`（生成 26 位 Crockford Base32 ULID）
