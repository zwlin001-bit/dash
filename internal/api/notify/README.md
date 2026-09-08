# api/notify

通知渠道、路由规则与投递日志 HTTP API。

## 职责边界

- 渠道管理：`GET / POST / PUT / DELETE /api/v1/notify/channels`
- 测试消息：`POST /api/v1/notify/channels/{id}/test`
- 规则管理：`GET / POST / PUT / DELETE /api/v1/notify/rules`
- 投递记录：`GET /api/v1/notify/deliveries`
- 机密保护：所有接口返回均只展示脱敏后尾号（如 `••••1234`），API 响应与日志绝不出现明文机密。

## 依赖谁

- `dash/internal/notify` 领域模型与持久化层。
- `dash/internal/app` 路由鉴权中间件。
