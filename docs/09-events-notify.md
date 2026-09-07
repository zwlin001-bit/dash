# 09 事件与消息通知

**所有模块的事件和告警走同一条管道。** 模块只负责「发出事件」，
存不存、提不提醒、推不推 Telegram，全由配置决定，模块不关心。

---

## 1. 分层

```
模块      →  emit(event)  →  事件入库  →  路由匹配  →  渲染模板  →  投递
监控/告警                    events      notify_rules  templates/  Job 引擎
节点/清单                                              （文件）     ↓
任务/云/代理                                                    Telegram / Webhook
```

- **模块只调 `emit()`**，不知道也不该知道有没有 TG
- 加一个通知渠道、改一条路由、换一个模板，**都不用改任何模块的代码**

---

## 2. 事件

### 2.1 类型编码

`<模块>.<对象>.<动作>`，全小写。第一期就会产生的：

| event_type | 默认级别 | 默认处置 |
|---|---|---|
| `node.online` | info | store |
| `node.offline` | warning | store+notify |
| `node.enrolled` | info | store |
| `node.removed` | warning | store |
| `auth.login_failed` | warning | store |
| `system.db_unreachable` | critical | store+notify |

第二期起陆续加入：`alert.firing` / `alert.resolved` / `billing.expiring` /
`billing.traffic_exceeded` / `agent.upgrade_failed` / `job.failed` /
`speedtest.completed` / `cloud.sync_failed` / `proxy.deployed` …

**事件类型由代码定义，界面不能新增**——事件是代码发出来的，凭空建一个类型没有意义。
界面能改的是它的**级别**和**处置方式**。

### 2.2 处置方式（用户说的「不同消息类型处理不同」）

| disposition | 行为 |
|---|---|
| `drop` | 不入库。给高频噪音留的出口 |
| `store` | 只入库，进时间线，界面可查 |
| `store+ui` | 入库 + 界面未读提醒 |
| `store+notify` | 入库 + 按路由规则外发 |

每个类型有内置默认值，**用户可在界面逐类型覆盖**。

### 2.3 表

```
event_types(event_type str(64) PK, display_name str(64),
            default_severity str(16), severity str(16),
            default_disposition str(16), disposition str(16),
            description text NULL, is_builtin bool, updated_at_ms)

events(id, event_type str(64), severity str(16), source_module str(32),
       target_kind str(32) NULL, target_id str(26) NULL,
       title str(255), payload_json text NULL,
       dedup_key str(128) NULL,
       is_read bool, occurred_at_ms ts, created_at_ms)
  ix_events_occurred (occurred_at_ms)
  ix_events_type     (event_type, occurred_at_ms)
  ix_events_target   (target_kind, target_id, occurred_at_ms)
```

`payload_json` 里放模板要用的变量。保留期 90 天，走已有的分块清理机制。

---

## 3. 通知渠道（多个 Telegram 号）

```
notify_channels(id, name str(64), channel_kind str(32),
                credential_id NULL,      -- bot_token 等机密走信封加密
                config_json text,
                is_enabled bool, created_at_ms, updated_at_ms)
```

**一个 TG 号 = 一条 `notify_channels` 记录。想配几个配几个。**

| channel_kind | config_json | 机密 |
|---|---|---|
| `telegram` | `{"chat_id":"…","thread_id":null,"parse_mode":"HTML"}` | `bot_token` → `credentials` |
| `webhook` | `{"url":"…","method":"POST","headers":{…}}` | 需要时同上 |

**bot_token 绝不明文存在 `notify_channels` 里**，走 `credentials` 表信封加密。

---

## 4. 路由规则（xx 类型 → xx TG 号）

```
notify_rules(id, name str(64), is_enabled bool,
             event_pattern str(64),        -- 精确 'node.offline' 或通配 'node.*' / '*'
             min_severity str(16),         -- 低于这个级别不发
             notify_channel_id,
             template_name str(64) NULL,   -- 留空则用 event_type 同名模板
             throttle_s i32,               -- 同一 (pattern,target) 节流窗口
             quiet_start_min i32 NULL,     -- 免打扰起（当天第几分钟，UTC 偏移由 settings 定）
             quiet_end_min i32 NULL,
             display_order i32, created_at_ms, updated_at_ms)
  ix_notify_rules_enabled (is_enabled)
```

- **多对多**：一个类型可以配多个渠道（同时发给两个 TG 号），
  一个渠道也可以接多个类型。这就是「xx 类型由 xx TG 号通知」的落点
- `event_pattern` 只支持末尾 `*` 通配，**不做正则**——正则会让人写出自己都看不懂的规则
- `throttle_s`：同一 `(rule, target)` 在窗口内只发一条。节点反复上下线时救命
- `quiet_*`：免打扰时段内的通知**攒着，不丢**，出时段合并成一条发出

```
notify_deliveries(id, event_id, notify_rule_id, notify_channel_id,
                  delivery_state str(16),      -- pending/sent/failed/throttled/quiet_held
                  attempt_no i32, error_msg text NULL,
                  rendered_text text,          -- ★ 渲染后的完整文本
                  sent_at_ms ts NULL, created_at_ms, updated_at_ms)
  ix_deliveries_event (event_id)
```

★ **`rendered_text` 必须存。** 「为什么我没收到通知」是这类系统最常见的问题，
存了渲染结果才能一眼看出是没匹配规则、被节流了、还是发送失败。

---

## 5. 消息模板（外置成文件）

**模板是文件，不是代码，也不在数据库里。改模板不用改代码、不用发版。**

```
/etc/dash/templates/
  telegram/
    _default.tmpl          兜底
    node.offline.tmpl
    alert.firing.tmpl
    billing.expiring.tmpl
  webhook/
    _default.tmpl
```

- 查找顺序：`notify_rules.template_name` 指定的 → `<event_type>.tmpl` → `_default.tmpl`
- **内置默认模板随二进制发布**（`go:embed`），安装时释放到上面这个目录。
  用户改了就用用户的，删了就回落内置——**保证「一条命令部署」不需要用户先写模板**
- **热重载**：文件改动后自动生效，不重启进程
- 界面提供**模板预览 + 用真实事件试发**，不用为了验证一个模板去等一次真实告警

### 5.1 语法

Go `text/template`，**函数白名单**，不允许任意函数调用。

```
🔴 <b>{{.NodeName}}</b> 离线

分组：{{.GroupName}}
最后在线：{{fmtTime .LastSeenAtMs}}
已离线：{{fmtDur .OfflineSeconds}}
{{if .PublicIP}}IP：{{.PublicIP}}{{end}}
```

可用函数（就这些，要加得改代码）：

| 函数 | 用途 |
|---|---|
| `fmtTime` | 毫秒时间戳 → 可读时间 |
| `fmtDur` | 秒数 → `2小时13分` |
| `fmtBytes` | 字节 → `1.2 GB` |
| `fmtPct` | 小数 → `85.3%` |
| `default` | 空值兜底 |

### 5.2 变量

**通用变量所有模板都有**：`.EventType` `.Severity` `.Title` `.OccurredAtMs`
`.TargetKind` `.TargetId` `.SiteDomain`

**类型特有变量来自 `payload_json`**，每个事件类型要在
`docs/09-events-notify.md` 本节列出它提供哪些变量——
**新增事件类型时必须同步补上，否则没人知道模板里能写什么。**

| event_type | 特有变量 |
|---|---|
| `node.online` | `.NodeName` `.GroupName` `.PublicIP` `.OfflineSeconds` |
| `node.offline` | `.NodeName` `.GroupName` `.PublicIP` `.LastSeenAtMs` |
| `node.enrolled` | `.NodeName` `.PublicIP` `.OSName` `.Arch` |
| `node.removed` | `.NodeName` `.Operator` |
| `auth.login_failed` | `.Username` `.IP` `.FailCount` |
| `system.db_unreachable` | `.ErrorMsg` `.FailedSeconds` `.DroppedBatches` |

### 5.3 渲染失败

模板写错（引用了不存在的变量、语法错）**不允许把通知吞掉**：
回落到 `_default.tmpl`，并产生一条 `system.template_error` 事件。
**通知系统自己坏掉是必须被通知到的。**

---

## 6. 投递

**投递用 `notify_deliveries` 自己做重试队列，不走 Job 引擎。**
Job 引擎是给「多 step、异构、长耗时」的操作用的（建机器、装代理）；
通知投递是单步、同构、高频的，塞进 Job 引擎是错配，也会让通知模块无谓地依赖它。

- `notify_deliveries` 就是队列：`delivery_state` + `attempt_no` + `next_retry_at_ms`
- 失败重试 3 次，指数退避 10s / 60s / 300s，超次数标 `failed` 并产生
  `system.notify_failed` 事件
- 第三方（TG API）挂掉**不能拖垮事件写入**——`emit()` 永远非阻塞
- Telegram 对同一 chat 有速率限制，发送要**串行 + 限速**（每 chat 一个发送队列）
- 每次尝试都更新 `notify_deliveries`，含渲染后的完整文本

---

## 7. 对第一期的影响

事件总线要**第一期就埋进去**，否则第一期的模块（节点上下线、注册、删除）
到第二期都要回头改一遍。

**第一期只做最薄的一层**：`events` / `event_types` 两张表 + 一个 `emit()` 接口 +
已有模块接入 + 界面上一个事件时间线。
**不做**渠道、路由、模板、投递——那些是第二期。
