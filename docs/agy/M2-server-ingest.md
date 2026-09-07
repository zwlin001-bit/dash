# M2 服务端接收与存储

设计文档：[`../02-database.md`](../02-database.md) §5、[`../04-protocol.md`](../04-protocol.md)

---

## T2.1 Agent 连接管理

**交付物**：`internal/control/`——WS 端点、连接注册表、在线状态、能力协商。

**约束**
- `/api/agent/v1/rpc`，token 校验后建立连接；同一 node 重复连接时**踢掉旧连接**
- `agent.hello` 应答里下发采集参数（`interval_fast_s` 等），**服务端是采集配置的权威**
- 90 秒无任何消息判离线，更新 `nodes.conn_state` 与 `last_seen_at_ms`
- 时钟偏差 `server_ts_ms - ts_ms` 超过 60 秒时以服务端时间入库，并在节点上打标记

**验收**：kill -9 掉 agent，90 秒内界面显示离线；agent 重启后自动恢复在线。

---

## T2.2 批量落库

**交付物**：`internal/ingest/`——ring buffer + 5 秒批量 flush + 最新值内存缓存。

**约束（照抄这里，别自己发挥）**
- **绝不一条上报一次 INSERT**。ADB 在网络另一端，往返延迟是主要成本
- flush 失败重试 3 次（退避 1s/2s/4s），仍失败丢弃最老一批并计数告警。
  **监控数据不值得为了不丢而阻塞采集链路**
- 前端「实时」视图读内存最新值缓存，**不查库**——数据库短暂不可用时实时视图仍正常
- 计数器 reset 处理（`02-database.md` §5.7）：
  ```
  delta = cur >= prev ? cur - prev : cur
  ```
  per-node 的 `prev` 存内存，进程重启时从数据库最近一行恢复

**验收**
- 30 个模拟 agent 以 5 秒间隔上报，持续 10 分钟，`sample_host` 行数 = 30 × 120 ± 少量边界
- 中途断开数据库 30 秒，恢复后写入继续，实时视图全程可用
- 模拟一台机器重启（`net_total_up` 归零），`traffic_up` 不出现负值或异常巨值

---

## T2.3 Rollup 与保留期

**交付物**：`internal/ingest/rollup.go` + `retention.go`，由 dashd 内部调度器驱动。

**约束**
- rollup 用**可移植 SQL 在数据库内完成**，`INSERT ... SELECT ... GROUP BY FLOOR(ts_ms/60000)`，
  不把数据拉到应用层
- **只处理已关闭的时间桶**
- 幂等：先按 bucket 区间 `DELETE` 再 `INSERT`，一个事务
- `_1h` 从 `_1m` 聚合，`_1d` 从 `_1h` 聚合，**不回读 raw**
- 逐级聚合的 avg 必须加权：`SUM(x_avg * sample_cnt) / SUM(sample_cnt)`
- 保留期清理按**时间区间**分块 DELETE（每次一小时），**不用 `LIMIT`**，每轮之间 sleep
- 保留期参数存 `settings`，可改，默认见 `02-database.md` §5.6

**验收**
- 灌 3 天模拟数据，跑 rollup，抽查 `_1m` 的 avg/max/min 与 raw 直接算的结果一致
- 重复跑 rollup 结果不变（幂等）
- 清理任务跑完，`sample_host` 里没有超出保留期的行，且过程中没有长事务

---

## T2.4 查询 API

**交付物**：`internal/api/`——节点列表、节点详情、时序查询、SSE 实时推送。

**约束**
- 时序查询按跨度自动选表（`02-database.md` §6），返回点数上限 400
- **禁止在时序表上做 join**
- 所有列表接口用 `dialect.Paginate`，不裸写 `LIMIT` / `FETCH`

**验收**：查 6 小时、3 天、60 天、1 年四个跨度，分别命中 raw / 1m / 1h / 1d，
返回点数都 ≤ 400，单次查询 < 500ms。
