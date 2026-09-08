# notify

消息与通知子系统（P2-01 / 09-events-notify.md §3–§6）。

## 职责边界

- **渠道管理**：支持 Telegram 与 Webhook 两种渠道驱动，支持扩展更多驱动。
- **凭据管理**：信封加密（AES-256-GCM），主密钥来自 `/etc/dash/master.key`，机密绝不明文入库、绝不进日志。
- **路由匹配**：按 `event_pattern` 通配与 `min_severity` 过滤，多规则命中按渠道去重。
- **静默期与节流**：支持免打扰时段（quiet hours）与去重窗口（throttle_s）。
- **模板引擎**：基于 `text/template`，严格函数白名单；渲染失败自动降级为可读标题，绝不吞掉告警。
- **投递重试**：异步投递队列与指数退避重试（3 次），不可重试错误立即失败并产生告警。
- **非阻塞发布**：`events.Emit()` 发布耗时不受第三方服务或投递网络状况影响。

## 对外接口

- `NewModule() *Module`
- `Store`: `CreateChannel`, `UpdateChannel`, `DeleteChannel`, `ListChannels`, `CreateRule`, `ListRules`, `ListDeliveries` 等
- `Dispatcher`: `Dispatch(e Event)`, `TestSendChannel(ctx, channelID, text)`

## 依赖谁

- `dash/internal/crypto` 信封加密
- `dash/internal/db` 数据库访问
- `dash/internal/events` 事件总线
- `dash/internal/notify/sender` 渠道驱动
- `dash/internal/notify/templates` 内嵌默认模板
- `dash/internal/logx` 脱敏日志
