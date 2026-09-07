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
| [`06-speedtest.md`](06-speedtest.md) | 三网测速模块（电信/联通/移动 + 自定义） |
| `adr/` | 关键决策记录 |
| [`agy/`](agy/README.md) | **给 AGY（反重力）的实施任务清单与验收标准**（派发队列见 [`agy/02-queue.md`](agy/02-queue.md)） |

## 参考来源

Agent 上报与时序存储的设计参考了 [komari](https://github.com/komari-monitor/komari)
及 [komari-agent](https://github.com/komari-monitor/komari-agent)。
借鉴与偏离的具体点在 `01-architecture.md` 与 `03-agent.md` 中逐条说明。
