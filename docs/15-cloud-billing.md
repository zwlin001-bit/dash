# 15 云账单聚合

**多个云账号的账单拉回来、按维度汇总、超预算告警。第二期。**

★ 与 `node_billing` 是两回事：那是**人工录入**的 VPS 月费与到期日（`10-schema-spec.md` §2），
这里是**从云厂商 API 拉回来的真实账单**。两者都要，互不替代。

---

## 1. 为什么单独做

`P2-02` 已经能查单实例当月账单（BSS `DescribeInstanceBill`），但那只是一个数字。
实际要回答的问题它答不了：

- 这个月一共花了多少？比上个月多还是少？
- 钱花在哪台机器 / 哪个资源类型上？
- 打了 `proxy` 标签的那批机器，合计多少钱？
- 快超预算了吗？

这些都需要**把账单落库、留历史、按维度汇总**。

---

## 2. 表结构

★ 照 `02-database.md` §1 的可移植 SQL 规范，禁 Oracle 方言。

```
bill_periods(id, cloud_account_id, period str(7),        -- "2026-09"
             currency str(8),
             total_amount f64,                            -- 应付
             pretax_amount f64 NULL, discount_amount f64 NULL,
             sync_state str(16),                          -- pending/syncing/ok/failed
             synced_at_ms ts NULL, error_text text NULL,
             created_at_ms, updated_at_ms)
  ux_bill_periods (cloud_account_id, period)
  ix_bill_periods_period (period)

bill_items(id, cloud_account_id, period str(7),
           res_kind str(32),                              -- instance / disk / ip / bandwidth / other
           res_ref str(191) NULL,                         -- 云厂商侧资源 id
           cloud_resource_id NULL,                        -- 关联到本地资源，可空
           item_name str(128) NULL,
           product_code str(64) NULL,
           currency str(8), amount f64,
           usage_text str(64) NULL,                       -- "720 小时"，展示用
           created_at_ms, updated_at_ms)
  ix_bill_items_period (cloud_account_id, period)
  ix_bill_items_res (cloud_resource_id)
  ix_bill_items_ref (res_ref)

bill_budgets(id, scope_kind str(16), scope_ref str(26) NULL,   -- all / account / tag
             period_kind str(8),                               -- month
             currency str(8), amount f64,
             warn_ratio f64,                                    -- 0.8 = 用到 80% 预警
             is_enabled bool,
             created_at_ms, updated_at_ms)
```

### 为什么 `period` 是字符串

`"2026-09"` 直接可比、可排序、可当唯一键，且**跨时区没有歧义**。
存时间戳反而要每次算月份边界，而云厂商的账期本来就是按它自己的时区划的。

### `cloud_resource_id` 可空

账单里有大量**不对应任何实例**的条目（公网带宽包、快照、OSS 存储）。
★ **不要为了外键完整性把它们丢掉** —— 那正是「钱花哪儿了」最容易被忽略的部分。
关联不上就留空，`res_kind` 记为 `other`，界面上照样展示。

---

## 3. 币种

★ **原币存储，不做自动汇率折算。**

一个账号一种币，`bill_periods.currency` 记它。跨账号汇总时：

- 同币种直接相加
- 不同币种**分组显示**，不许偷偷折算成人民币

汇率是会变的，折算过的历史数字既不可复现也不可对账。
用户要看合计时，界面上并排列出「CNY 1234.56 / USD 78.90」即可。

---

## 4. 同步

- 走 Job 引擎（`P2-03`），job kind = `bill.sync`
- 触发：每天一次定时 + 界面手动
- ★ **当月账单是变动的**，每次同步覆盖当月；**已关账的历史月份只拉一次**，
  拉到就不再动（`sync_state='ok'` 且 `period` < 当月即跳过）
- 默认回补最近 **12 个月**，可配

### 阿里云 API

| 用途 | Action | 说明 |
|---|---|---|
| 月度总览 | `QueryBillOverview` | 拿 `bill_periods` 那一行 |
| 明细 | `QueryInstanceBill` | 拿 `bill_items`，**分页** |

endpoint 沿用 `P2-02` 的 `cloud_accounts.account_site`（中国站 / 国际站）。

★ **账单 API 的限流比 ECS 严得多。** 回补 12 个月要串行加退避，
不许并发轰。同步失败写 `error_text`，不阻塞其他账号。

---

## 5. Provider 契约扩展

`01-architecture.md` §3.3 的契约加一个方法：

| 方法 | 方向 | 说明 |
|---|---|---|
| `bill.list` | dashd → provider | 给定账期，返回归一化账单 |

返回形状：

```json
{
  "period": "2026-09",
  "currency": "CNY",
  "total_amount": 1234.56,
  "pretax_amount": 1200.00,
  "items": [
    {"res_kind":"instance","res_ref":"i-xxx","item_name":"ecs.t6-c1m1.large",
     "product_code":"ecs","amount":24.0,"usage_text":"720 小时"}
  ]
}
```

★ 新增 provider 只要实现这个方法就能接入账单，**不改 dashd**。
GCP / AWS / Oracle 后续同理。

---

## 6. 预算告警

★ **不新造一套告警机制**，复用 `07-monitoring.md` §2 的告警引擎，
加一个 `rule_kind = 'budget'`：

| rule_kind | threshold 含义 | 数据来源 |
|---|---|---|
| `budget` | 当月用量占预算的比例（0.8 = 80%） | `bill_periods.total_amount` ÷ `bill_budgets.amount` |

评估频率跟 `expiry` / `traffic` 一样每小时一次。
去抖、静默、恢复通知全部沿用既有状态机，**一行都不用重写**。

事件类型（`09-events-notify.md` §2.1 登记）：

| event_type | 默认级别 | 默认处置 |
|---|---|---|
| `billing.budget_warning` | warning | store+notify |
| `billing.budget_exceeded` | critical | store+notify |
| `billing.sync_failed` | error | store+notify |

---

## 7. 界面

账单页：

- 顶部：当月合计（按币种分组）、环比上月、预算进度条
- 趋势：最近 12 个月柱状图，**按币种分开画**，不要叠在一起
- 明细表：按资源类型 / 单资源 / 标签三种维度切换汇总
- ★ **关联不上本地资源的条目单列一组**，标为「未关联资源」——
  这部分最容易失控（忘记删的公网 IP、快照）

缺数据显示 `--`，不用 `0` 顶替（`13-ui-spec.md` 的既有规矩）。

---

## 8. 保留期

账单数据量很小（每账号每月几十到几百行），保留 **24 个月**。
走 `02-database.md` §6 已有的分块清理机制。
