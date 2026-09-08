# internal/api/jobs

Job 引擎 HTTP API 与 SSE 进度流推送模块（P2-03）。

## 职责边界

- 提供 Job 任务列表查询、提交、状态详情、取消与重试 HTTP API。
- 提供 `GET /api/v1/jobs/{id}/stream` SSE 端点，实时推送任务及步骤状态与日志。
- 实现 `app.Module` 规范，供 `dashd` 启动时进行依赖注入与优雅关闭装配。

## 路由列表

- `GET /api/v1/jobs`: 任务列表查询（支持按 kind、state、target_kind、target_id 过滤与分页）
- `POST /api/v1/jobs`: 提交新任务
- `GET /api/v1/jobs/kinds`: 获取当前可用任务类型定义清单
- `GET /api/v1/jobs/{id}`: 获取单个任务详情（含所有 steps 及日志）
- `POST /api/v1/jobs/{id}/cancel`: 取消任务
- `POST /api/v1/jobs/{id}/retry`: 重试任务
- `GET /api/v1/jobs/{id}/stream`: SSE 实时任务进度与日志流

## 依赖谁

- `dash/internal/jobs`: 核心 Job 调度与执行引擎
- `dash/internal/app`: 应用容器与中间件注入
