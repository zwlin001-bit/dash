# internal/ingest

指标数据接收、批量聚合与时序留存。

## 职责边界

- 接收来自 agent 上报的时序指标数据（WebSocket 与 HTTP 回退端点）。
- 内存缓冲并执行 3 秒批量聚合落库（避免单条写入打满 ADB）。
- 定时驱动分级 rollup 聚合（1m / 1h / 1d 降采样）。
- 执行数据生命周期保留期清理（按时间分块 DELETE，不使用 LIMIT）。

## 对外接口

- `Ingester`：数据接收与缓冲队列接口。
- `Start(ctx context.Context) error` / `Stop() error`：批处理器生命周期管理。

## 依赖谁

- `internal/protocol`
- `internal/db`
- `internal/logx`
