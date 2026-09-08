# internal/cloudmetric

云侧监控指标模块（dash P2-10 / docs/16-cloud-metrics.md）。

## 职责边界

- 从云厂商 API（通过 Provider 插件）定时增量拉取云监控指标与账号级 CDT 流量历史。
- 仅针对**被保活规则纳管的实例**和**账号级 CDT 流量**拉取，防止云监控 API 成本与调用量线性膨胀。
- 采用串行 + 退避策略调用云监控 API，单点失败不阻塞其他资源，失败隔离写入事件，不影响保活与账单主流程。
- 将采集点以原始 5 分钟粒度持久化至 `cloud_samples` 表（保留期 90 天，走分块清理）。
- 对外提供标准列式时序查询 API，供资源详情页与守卫页图表展示。

## 对外接口

- `POST /api/v1/cloud-metrics/sync`: 手动或定时触发一轮增量指标拉取
- `GET /api/v1/cloud-resources/{id}/metrics`: 实例级云监控指标曲线查询
- `GET /api/v1/cloud-accounts/{id}/metrics`: 账号级 CDT 流量月度历史曲线查询
- Job 注册：`cloud.metric.sync`

## 依赖关系

- `internal/db`: 数据库可移植 SQL 与方言支持
- `internal/credentials`: 凭据解密
- `internal/provider`: Provider 插件客户端与 `metric.list` 契约
- `internal/jobs`: Job 引擎注册与执行
- `internal/events`: 错误与告警事件写入
- `internal/app`: 模块生命周期注册
