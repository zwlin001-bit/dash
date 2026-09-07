# internal/api/metrics

时序数据查询 API、最新采样值缓存与 SSE 实时数据推送。

## 职责边界

- **时序查询 (`GET /api/v1/nodes/{id}/metrics`)**：
  - 根据时间跨度 `to_ms - from_ms` 自动选表（`sample_host`、`sample_host_1m`、`sample_host_1h`、`sample_host_1d`）。
  - 执行主键范围扫描单表查询（禁止 join，不裸写方言分页）。
  - 对超过 `max_points`（默认 400，上限 1000）的数据点在应用层执行保留峰值的窗口抽稀。
  - 返回列式 JSON 数据结构，格式与前端图表组件入参完全对齐。
- **最新采样值 (`GET /api/v1/nodes/{id}/latest`)**：
  - 读取纯内存最新值缓存（`LatestStore`），完全不查库，与落库链路解耦。
- **实时事件推送 (`GET /api/v1/metrics/stream` & `GET /api/v1/stream`)**：
  - SSE 协议实时推送 `metrics` 和 `node_state` 事件。
  - 每 25 秒发送 `ping` 事件保活，防止反代断开。
  - 连接断开时安全注销并回收 goroutine 与 channel，杜绝连接泄漏。
  - 数据库不可用时仍能持续推送内存最新值。

## 对外接口

- `Module`：实现 `app.Module` 接口（`Name() string`、`Register(*app.App) error`）。
- `LatestStore`：
  - `NewLatestStore() *LatestStore`
  - `SetLatest(nodeID string, latest NodeLatest)`
  - `GetLatest(nodeID string) (NodeLatest, bool)`
  - `BroadcastMetrics(nodeID string, latest NodeLatest)`
  - `BroadcastNodeState(nodeID string, connState string, lastSeenMs int64)`
  - `Subscribe() (<-chan StreamEvent, func())`
  - `ActiveSubscribers() int`
- `QueryEngine`：
  - `NewQueryEngine(database *db.DB) *QueryEngine`
  - `QueryMetrics(ctx context.Context, nodeID string, fromMs, toMs int64, fieldsParam string, maxPoints int) (*MetricsQueryResponse, error)`
- `Handler`：
  - `NewHandler(database *db.DB, store *LatestStore) *Handler`
  - `RegisterRoutes(mux *http.ServeMux)`

## 依赖谁

- `dash/internal/db`
- `dash/internal/app`
- 标准库 `net/http`
