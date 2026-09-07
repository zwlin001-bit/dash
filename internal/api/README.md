# internal/api

控制台 REST API 与实时通信端点。

## 职责边界

- 提供前端控制台所需的 REST API（节点管理、指标查询、测速结果、系统设置等）。
- 提供 SSE（Server-Sent Events）实时事件推送流（异步 Job 进度、节点状态变更）。
- 承载 agent 的接入端点：WebSocket 升级端点（`/api/agent/v1/rpc`）与 HTTP 回退上报端点（`/api/agent/v1/report`）。
- 严格进行请求参数校验、鉴权与审计日志记录。

## 对外接口

- `RegisterRoutes(mux *http.ServeMux, app *dashd.App)`：注册所有 HTTP/WebSocket 路由。

## 依赖谁

- `internal/config`
- `internal/db`
- `internal/control`
- `internal/ingest`
- `internal/inventory`
- `internal/protocol`
- `internal/logx`
