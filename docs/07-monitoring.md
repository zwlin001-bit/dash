# 07 监控告警与网络探测

> **第二期内容。** 第一期只做采集、存储、查询、展示（见 `agy/tasks/`）。
> 本文件的表（`ping_tasks`、`alert_rule_channels`）与 `alert_rules.rule_kind` 列
> **第一期不要建**，留给第二期的迁移编号。


监控链路的采集、存储、查询在 `02-database.md` §5 和 `03-agent.md`。
**本文件补齐监控的另一半：网络质量探测、告警评估、通知下发、到期与流量提醒。**

---

## 1. 网络质量探测（Ping）

跟三网测速（`06-speedtest.md`）的区别：**测速是重量级、按需触发；探测是轻量级、常态运行。**

| | Ping 探测 | 三网测速 |
|---|---|---|
| 频率 | 默认 60 秒一次，常态 | 手动或按小时/天 |
| 成本 | 10 次 TCP 握手，几乎为零 | 传输几十 MB |
| 用途 | 连通性、延迟趋势、丢包告警 | 带宽评估、线路选型 |

### 1.1 表结构

```
ping_tasks(id, name str(64), is_enabled bool,
           probe_kind str(16),            -- 复用测速的探测注册表，本期只有 tcping
           endpoint str(255),             -- host:port
           interval_s i32,                -- 默认 60
           probe_count i32,               -- 默认 5
           timeout_ms i32,                -- 单次握手超时，默认 3000
           scope_kind str(16), scope_ref str(26) NULL,   -- all / group / tag / node
           display_order i32, note text NULL,
           created_at_ms, updated_at_ms)
  ix_ping_tasks_enabled (is_enabled)
```

结果**不单独建表**，直接写窄表复用已有的 rollup 与图表：

| metric_code | dim_key | 含义 |
|---|---|---|
| `net.rtt_ms` | ping_task id | 握手延迟中位数 |
| `net.loss_pct` | ping_task id | 丢包率 |

### 1.2 执行方式

**agent 自己按周期跑，不由服务端逐次下发**——30 台机器 × 每分钟下发一次是没必要的网络开销。

```
连接建立 / 任务变更  →  server.ping_task 下发全量任务清单（全量替换，不做增量）
agent 本地按 interval_s 自行调度
每轮结束              →  agent.ping_result 上报
```

全量替换而不是增量同步，是因为增量同步的状态一致性问题不值得为几条任务承担。

### 1.3 资源约束

- 复用 `agent/speedtest/` 的探测注册表，`tcping` 实现直接共用
- **用 TCP 握手，不用 ICMP**（agent 非 root 运行）
- 所有 ping 任务**串行执行**，与测速共用同一个探测闸门（同一时刻全局只有一个探测在跑）
- 单轮所有任务的总耗时超过 `interval_s` 时，**跳过这一轮并打日志**，不允许堆积

---

## 2. 告警引擎

### 2.1 规则模型

```
alert_rules(id, name str(64), is_enabled bool,
            rule_kind str(16),            -- ★ metric / offline / expiry / traffic
            scope_kind str(16), scope_ref str(26) NULL,
            metric_code str(40) NULL,     -- rule_kind=metric 时必填
            compare_op str(4),            -- gt / lt / gte / lte
            threshold f64,
            duration_s i32,               -- 持续多久才触发，去抖
            severity str(16),             -- info / warning / critical
            silence_s i32,                -- 触发后多久内不重复通知
            created_at_ms, updated_at_ms)

alert_rule_channels(alert_rule_id, notify_channel_id, PK(alert_rule_id, notify_channel_id))

alert_events(id, alert_rule_id, node_id, event_state str(16),
             fired_at_ms ts, resolved_at_ms ts NULL,
             peak_value f64 NULL, detail text NULL,
             notified_at_ms ts NULL,
             created_at_ms, updated_at_ms)
```

**四种 `rule_kind` 共用同一套规则表和状态机**，`threshold` 的语义按类型解释：

| rule_kind | threshold 含义 | 数据来源 |
|---|---|---|
| `metric` | 指标阈值（如 CPU > 90） | `sample_host_1m` / `sample_dim_1m` |
| `offline` | 离线多少秒算异常（如 300） | `nodes.last_seen_at_ms` |
| `expiry` | 剩余天数少于多少（如 7） | `node_billing.expires_at_ms` |
| `traffic` | 月度流量用量占比超过多少（如 0.8） | `sample_host_1h` 的 `traffic_*_sum` 对比 `node_billing.traffic_limit` |

用一套表装四种规则，而不是四张表——它们的生命周期、去抖、静默、通知逻辑完全一样，
分开会写四遍同样的状态机。

### 2.2 评估

**从 `_1m` rollup 读，不从 raw 读。**
raw 是 5 秒粒度，CPU 瞬时尖峰会造成大量误报；1 分钟平均值才是「持续高负载」的正确判据。

调度：`metric` 类每分钟评估一次；`offline` 每 30 秒；`expiry` / `traffic` 每小时。

状态机：

```
        越界
  ok ─────────▶ pending ──── 持续满 duration_s ────▶ firing
   ▲              │                                    │
   │              │ 恢复正常                            │ 恢复正常
   └──────────────┘                                    │
   └────────────────── resolved ◀───────────────────────┘
```

- `pending` 状态**不发通知**，只有转 `firing` 才发
- `firing` → `resolved` 也发一条恢复通知
- `silence_s` 内同一 `(rule, node)` 不重复通知，但事件照样记录
- **进程重启后要从 `alert_events` 恢复 firing 状态**，否则重启会导致重复告警和丢失恢复通知

### 2.3 作用域

`scope_kind` 决定规则作用在哪些节点：`all` / `group`（分组 id）/ `tag`（标签 id）/ `node`（单机）。
标签作用域是这里用得最多的——比如「所有打了 `proxy` 标签的机器，丢包率 > 5% 告警」。

---

## 3. 通知渠道

```
notify_channels(id, name str(64), channel_kind str(32),
                config_json text, is_enabled bool,
                created_at_ms, updated_at_ms)
```

本期实现两种，`channel_kind` 可扩展：

| kind | config_json | 说明 |
|---|---|---|
| `webhook` | `{"url":"…","method":"POST","headers":{…},"body_template":"…"}` | 通用出口，能接几乎所有第三方 |
| `telegram` | `{"bot_token":"…","chat_id":"…"}` | 常用 |

**发送走 Job 引擎**（`01-architecture.md` §3.4），不在评估循环里同步发：

- 通知失败可重试（默认 3 次，指数退避）
- 有持久化记录和审计
- 第三方服务挂掉不会拖垮告警评估

`config_json` 里的 token 等机密**走 `credentials` 表信封加密**，不明文存在 `notify_channels` 里。

消息模板变量：`{{node_name}}` `{{rule_name}}` `{{severity}}` `{{value}}` `{{threshold}}`
`{{fired_at}}` `{{event_state}}`。模板渲染**不允许执行任意表达式**，只做变量替换。

---

## 4. 到期与流量提醒

这两项是 VPS 管理最实用的功能，但它们不是「监控指标」，容易被漏掉。

### 4.1 到期提醒

- 数据源：`node_billing.expires_at_ms`
- 规则：`rule_kind='expiry'`，`threshold` = 提前多少天提醒
- 建议默认建三条规则：提前 30 天（info）、7 天（warning）、1 天（critical）
- `is_auto_renew=1` 的节点**默认不提醒**，可在规则里开关

### 4.2 流量提醒

- 数据源：当前计费周期内 `sample_host_1h` 的 `traffic_up_sum` / `traffic_down_sum` 累加
- 计费周期起点：`node_billing.traffic_reset_day`（每月几号重置）
- 统计口径按 `node_billing.traffic_limit_kind`：

| kind | 含义 |
|---|---|
| `sum` | 上行 + 下行 |
| `max` | 上行、下行取大者 |
| `min` | 取小者 |
| `up` / `down` | 只算单向 |

- 规则：`rule_kind='traffic'`，`threshold` = 用量占比（0.8 表示用到 80% 提醒）

**注意**：流量统计依赖 `02-database.md` §5.7 的计数器 reset 处理。
那一步做错，机器一重启这里就会立刻误报「流量超限」。
