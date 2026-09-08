# 16 云侧监控指标

**从云厂商 API 拉指标，补上 agent 覆盖不到的地方。第二期。**

---

## 1. 为什么需要它

agent 采集已经很完整了，但有三种情况它天然拿不到：

1. ★ **被保活停掉的实例**（P2-04）—— 机器关着，agent 不在跑，
   界面上只能显示 `--`。但你恰恰想知道它停之前用了多少
2. **没装 agent 的资源** —— RDS、SLB、OSS、公网带宽包，永远装不上 agent
3. ★ **账号级 CDT 流量没有历史** —— P2-04 每轮查一次当前值用于决策，
   但**不落库**。于是看不到「这个月烧得多快」，而这正是保活阈值该定多少的依据

★ **这不是要取代 agent。** agent 是 5 秒粒度、指标全、免费；
云监控通常 1 分钟粒度、指标少、调用要计费。**能用 agent 的一律用 agent。**

---

## 2. 表

`metric_series.node_id` 是 `NOT NULL`，云侧资源塞不进去。单独一张：

```
cloud_samples(cloud_account_id, res_ref str(191), metric_code str(40),
              ts_ms ts, value f64,
              PK(cloud_account_id, res_ref, metric_code, ts_ms))
  ix_cloud_samples_ts (ts_ms)
```

- `res_ref` 用云厂商侧 id（`i-xxx`、账号级指标用 `_account`），
  ★ **不用 `cloud_resource_id`** —— 资源在本地被删了，历史数据不该跟着消失
- 照 `02-database.md` §1 的可移植 SQL 规范，禁 Oracle 方言

### 粒度与保留期

★ **不做多级 rollup。** 云监控本来就是 1 分钟起步、数据量比 agent 小两个数量级，
再分层是过度设计。

- 原始粒度：5 分钟（拉取时按此聚合）
- 保留期：**90 天**，走 `02-database.md` §6 已有的分块清理

---

## 3. Provider 契约扩展

`01-architecture.md` §3.3 加一个方法：

| 方法 | 方向 | 说明 |
|---|---|---|
| `metric.list` | dashd → provider | 给定资源、指标、时间范围，返回时序点 |

```json
{
  "res_ref": "i-xxx",
  "metric_code": "cpu_pct",
  "points": [{"ts_ms": 1788831072000, "value": 12.5}]
}
```

`metric_code` **沿用 `08-field-map.md` 里已有的命名**（`cpu_pct`、`mem_used`、
`net_up_bps`、`net_down_bps`、`traffic_month_up` …）。
★ **不要为云侧新造一套指标名** —— 同一个 CPU 使用率在两处叫不同名字，
图表和告警规则就得写两遍。provider 负责把厂商的名字翻译成我们的。

---

## 4. 拉取

- 走 Job 引擎（P2-03），job kind = `cloud.metric.sync`
- 频率：默认 **10 分钟**一轮，可配
- ★ **只拉「有人看」的**：默认只拉被保活规则管着的实例 + 账号级 CDT 流量。
  全量拉是要花钱的，且大部分资源根本没人看
- 增量：记录每个 `(res_ref, metric_code)` 的 `last_at_ms`，只拉之后的点
- ★ 云监控 API 有限流，**串行 + 退避**，失败写事件不阻塞其他资源

---

## 5. 界面

- 资源详情页：云侧指标曲线，★ **与 agent 曲线并排但明确区分来源**
  （标注「云监控 · 5 分钟粒度」），不要混在同一条线里让人分不清
- 账号页：**CDT 流量月度曲线** + 当前阈值横线 —— 这是本文档最实用的一张图
- 缺数据显示 `--`，断点断线不连成直线（`13-ui-spec.md` 既有规矩）

---

## 6. 与告警的关系

`cloud_samples` 可以作为 `07-monitoring.md` §2 告警引擎的又一个数据源，
但 **第二期先不接**。先把数据攒起来、图能看，再谈告警规则要怎么写。
