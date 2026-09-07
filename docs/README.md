# dash 设计文档

阅读顺序：

| 文档 | 内容 |
|---|---|
| [`../PRINCIPLES.md`](../PRINCIPLES.md) | **工程原则（硬性约束）** |
| [`01-architecture.md`](01-architecture.md) | 整体架构、模块边界、Provider 插件契约、Job 引擎 |
| [`02-database.md`](02-database.md) | 可移植 SQL 规范、类型映射、表结构、时序数据分层 |
| [`03-agent.md`](03-agent.md) | Agent 设计：采集分级、资源预算、兼容性、安装 |
| [`04-protocol.md`](04-protocol.md) | Agent ↔ Server 协议（JSON-RPC over WebSocket） |
| [`05-deployment.md`](05-deployment.md) | `setup.sh` 部署流程、证书、域名维护 |
| [`06-speedtest.md`](06-speedtest.md) | 三网测速模块（电信/联通/移动 + 自定义）·第三期 |
| [`07-monitoring.md`](07-monitoring.md) | Ping 探测、告警引擎、通知渠道、到期与流量提醒 ·第二期 |
| [`08-field-map.md`](08-field-map.md) | **跨层指标字段总表**：/proc → 协议 → 表列 → rollup → 图表 |
| [`09-events-notify.md`](09-events-notify.md) | 统一事件总线、通知渠道、路由规则、消息模板 |
| `adr/` | 关键决策记录 |
| [`agy/`](agy/README.md) | **给 AGY（反重力）的实施说明**。第一期任务清单：[`agy/tasks/`](agy/tasks/README.md) |

## 参考来源

Agent 上报与时序存储的设计参考了 [komari](https://github.com/komari-monitor/komari)
及 [komari-agent](https://github.com/komari-monitor/komari-agent)。
借鉴与偏离的具体点在 `01-architecture.md` 与 `03-agent.md` 中逐条说明。
