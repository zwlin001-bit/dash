# internal/api/events

事件控制台 HTTP API 接口层（第一期 · 任务 20 / 12-api-spec.md §7）。

## 职责边界

- 提供事件时间线查询 API（支持类型、级别、节点、已读与时间范围过滤）
- 提供批量标记已读接口
- 提供未读角标计数接口
- 提供事件类型策略查询与在线调整接口（修改 severity / disposition）

## 对外接口

- `RegisterRoutes(mux *http.ServeMux, s *events.Store)`: 注册所有事件相关 HTTP 路由

## 依赖关系

- 依赖 `internal/events`: 事件存储与类型策略管理
- 依赖 Go 标准库 `net/http` (Go 1.22+ 模式匹配路由)
