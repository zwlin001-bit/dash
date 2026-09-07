# internal/api

控制台 REST API 与实时通信端点。

## 职责边界

- 提供前端控制台所需的 REST API（节点管理、指标查询、测速结果、系统设置等）。
- 提供 SSE（Server-Sent Events）实时事件推送流（异步 Job 进度、节点状态变更）。
- 承载 agent 的接入端点：WebSocket 升级端点（`/api/agent/v1/rpc`）与 HTTP 回退上报端点（`/api/agent/v1/report`）。
- 严格进行请求参数校验、鉴权与审计日志记录。

## 对外接口

- `NewModule() app.Module`：创建 Web 服务端子模块，供 `cmd/dashd` 装配。
- `Handler() http.Handler`：返回内嵌 SPA 页面与静态资源路由 Handler。
- `RegisterRoutes(mux *http.ServeMux)`：注册静态资源与 SPA 路由至 mux。

## 依赖谁

- `internal/config`
- `internal/db`
- `internal/control`
- `internal/ingest`
- `internal/inventory`
- `internal/protocol`
- `internal/logx`
