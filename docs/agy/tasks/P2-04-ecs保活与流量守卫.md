# P2-04 ECS 保活与 CDT 流量守卫

> 第二期 · 任务 04 ｜ 前置：**P2-01 通知投递、P2-02 阿里云 provider、P2-03 Job 引擎（三个都要）**
> ｜ 分支：`agy/p2-04-ecs-guard`

> ★ **完成后必须自己 `git push -u origin <分支名>`**，并确认
> `git log --oneline -1 origin/<分支名>` 能看到你的提交。只提交到本地等于没交付。

## 目标

**按流量和时间表自动启停阿里云 ECS，省钱且不超额。**

功能取自 [`Felix666-ship-It/aliyun-guard`](https://github.com/Felix666-ship-It/aliyun-guard)，
★ **借鉴功能设计与踩过的坑，不整体搬运它的实现**（理由见 §0）。
实现全部基于 dash 现有的 provider 契约、Job 引擎、事件总线。

---

## 0. 审计结论：哪些能拿，哪些不能

样式表已确认可以照抄（见 P1-28）。**后端不整体搬运源码**——
理由不是法务，是它的技术栈（Python + Flask + sing-box + 自解压 installer）
与 dash 的 provider 契约、Job 引擎、单二进制部署完全不兼容，搬过来只会两头不讨好。
下面这张表是审计后的取舍，**照它执行**：

| 它有的 | 我们怎么办 |
|---|---|
| CDT 流量守卫 + ECS 保活决策逻辑 | ✅ **借鉴思路**，用我们的 provider + Job 引擎重写 |
| 每实例独立日程（每日开关机时间） | ✅ 借鉴 |
| 月初流量清零后的补充检测 | ✅ 借鉴，这个坑很实在 |
| dry-run 演练模式 | ✅ 借鉴 |
| 危险操作二次确认 | ✅ 借鉴 |
| Telegram Bot 直连 / 反代 | ❌ **不做**。我们有 P2-01 的通知管道，不再造一套 |
| sing-box + VLESS/VMess/SS/Trojan 节点解析 | ❌ **坚决不做**。这是给 TG 出网用的代理层，与本模块无关，且大幅扩大攻击面 |
| watchdog 心跳守护进程 | ❌ 不做。systemd/OpenRC 已经在管 dashd |
| AES 备份 + S3/R2 同步 | ❌ 不做。备份是独立议题 |
| Flask web panel / 交互式终端面板 | ❌ 不做。我们有自己的控制台 |
| `install.sh`（630 KB 自解压脚本，`wget \| sh` 安装） | ❌ **绝不引入**。我们有 `setup.sh` |

★ **不许把这个仓库加成 git submodule、依赖、或 vendor 目录。**

---

## 1. 数据模型

```
guard_rules(id, cloud_resource_id, is_enabled bool,
            actions_enabled bool,                    -- false = 只监控只告警，不真的启停
            traffic_limit_gb f64 NULL,               -- 账号级 CDT 阈值，NULL = 不限
            traffic_action str(16),                  -- stop / notify_only
            schedule_enabled bool,
            schedule_start str(5) NULL,              -- "08:30"，24 小时制
            schedule_stop  str(5) NULL,
            schedule_tz str(64),                     -- IANA 时区，如 Asia/Shanghai
            last_eval_at_ms ts NULL,
            last_action str(32) NULL, last_action_at_ms ts NULL,
            created_at_ms, updated_at_ms)
  ux_guard_rules_res (cloud_resource_id)

guard_cycles(id, cloud_account_id, started_at_ms ts, duration_ms i32,
             cdt_used_gb f64 NULL, cdt_error text NULL,
             evaluated i32, acted i32, failed i32,
             created_at_ms)
  ix_guard_cycles_started (started_at_ms)
```

★ **阈值挂在规则上但流量是账号级的**（见 P2-02 §3 第 1 条）。
同一账号下多个实例各自设阈值是允许的 —— 谁的阈值先被越过谁先停。
**界面上必须把这一点说清楚**，否则用户会以为每台机器有独立配额。

## 2. 评估循环

```
每 <interval> 秒（默认 60，可配）：
  按 cloud_account 分组
    ├─ 查一次 CDT 账号流量          ← 每账号每轮 1 次，不是每实例 1 次
    ├─ 按 (账号, region) 批量查 ECS 状态   ← 每批 ≤ 100
    ├─ 逐实例决策
    └─ 需要动作 → 提交 Job（P2-03）→ 记录 guard_cycles → emit 事件
```

★ **循环本身不直接调云 API 做动作。** 查询在循环里（快、只读），
启停一律提交 job（慢、要等状态确认、要能重试）。

### 决策优先级

**日程 > 流量 > 保活**，逐条判断，命中即停：

| # | 条件 | 动作 |
|---|---|---|
| 1 | 日程启用 且 当前处于计划关机时段 且 实例 Running | **停止** |
| 2 | 日程启用 且 当前处于计划运行时段 且 实例 Stopped 且 流量未超 | **启动** |
| 3 | 流量 ≥ 阈值 且 实例 Running | **停止** |
| 4 | 流量 < 阈值 且 实例 Stopped 且 不在计划关机时段 | **启动**（保活） |
| 5 | 其他 | 不动 |

### ★ 四条安全性质（一条都不能少）

1. **计划关机只依赖「ECS 状态可读」这一件事。**
   CDT 查询失败、BSS 查询失败，都**不许**让一台本该关机的实例继续开着。
   失败要照常记录和告警，但关机动作照做
2. **反过来不成立：流量读不到时不许启动实例。**
   读不到流量就等于不知道有没有超额，**默认按「不安全」处理，本轮不启动**
3. **过渡状态不动手。** ECS 状态是 `Starting` / `Stopping` 时本轮跳过，
   不要在状态机中间插一脚
4. ★ **同一实例上一个 job 没结束前不提交新 job**（P2-03 已保证，这里要显式依赖它）

### 动作确认

启停后要轮询确认到位，**不是提交完就当成功**：
- 启动：等待至 `Running`，默认 90 秒预算，5 秒一轮
- 停止：等待至 `Stopped`，默认 45 秒预算，5 秒一轮
- 超预算未到位 → job 标记 `warning`（不是 failed），事件里写清「已提交但未确认」

## 3. 月初流量重置

阿里云 CDT 配额每月 1 日 0 点（**账号所在时区**）清零。

★ **不能干等下一个常规周期。** 跨月后要做一次补充检测：
尽快读到归零的流量，把因为超额而停掉、且规则允许启动的实例**拉起来**。

- 记录「上次看到的月份键」，跨月即触发补检
- ★ **补检要考虑账单统计延迟**：刚过 0 点时 CDT 可能还返回上月数字。
  做法是**跨月后的前 30 分钟每 5 分钟重试一次**，读到明显下降才认为已重置，
  而不是无脚本地信第一个读数

## 4. 事件

新增事件类型（照 `09-events-notify.md` §2.1 注册，含默认级别与处置）：

| event_type | 默认级别 | 默认处置 |
|---|---|---|
| `cloud.guard.traffic_warning` | warning | store+notify |
| `cloud.guard.instance_stopped` | warning | store+notify |
| `cloud.guard.instance_started` | info | store+ui |
| `cloud.guard.action_failed` | error | store+notify |
| `cloud.guard.traffic_reset` | info | store+ui |
| `cloud.guard.cycle_error` | error | store+notify |

- `payload_json` 里带 `instance_name` / `region` / `cdt_used_gb` / `traffic_limit_gb` /
  `status_before` / `status_after`，供 P2-01 的模板用
- ★ **`dedup_key` 必须设**：`guard:<rule_id>:<event_type>:<月份键>`。
  循环每分钟一轮，没有去重会把人的手机推爆
- ★ **达阈值前先预警**：默认 80% 发一次 `traffic_warning`，阈值可配。
  只在停机那一刻才通知，用户来不及做任何事

## 5. 界面

守卫页（`web/src/pages/CloudGuard/`）：

- 账号级流量条：已用 / 阈值 / 百分比，**≥80% 橙、≥100% 红**（沿用 `--warn`/`--err`）
- 实例卡片：状态、本月账单、规则开关、日程、下一次日程事件时间
- 最近周期列表：耗时、评估数、动作数、失败数
- ★ **「演练一次」按钮**：跑一轮 dry-run，把「本轮会做什么」列出来但不真的执行。
  这是用户敢开自动化的前提
- ★ **手动强制启动**（流量已超时）需要**二次确认**，弹窗里写明当前用量与阈值，
  并写 `audit_log`

★ 缺失数据显示 `--`，不用 `0` 顶替（`13-ui-spec.md` 与 P1-23 的既有规矩）。

## 6. 安全

- ★ **保活会真的关掉生产机器。** 新建规则**默认 `actions_enabled = false`**，
  用户必须显式打开才会执行动作
- 每一次启停写 `audit_log`：谁（system/user）、对哪台、为什么（触发条件）、结果
- 错误信息里的 AK/SK 一律过 `logx.Redact`

## 验收

1. 配一台真实 ECS + 阈值设成**低于当前用量** → 一个周期内**实例被停**，
   Telegram 收到 `instance_stopped`，控制台上状态确实是「已停止」
2. 阈值改回高于用量 → 下一周期**实例被拉起**并确认 `Running`
3. ★ `actions_enabled = false` 时 → **只发事件不动手**，实例状态不变
4. ★ 演练模式 → 列出「会停止 X、会启动 Y」，**云上状态零变化**
5. 设一个 5 分钟后关机的日程 → 到点**准时停机**；关掉日程开关 → **不会立刻改变当前状态**，
   只是不再受时间约束（这是刻意的行为，写进界面说明）
6. ★ **把 CDT 权限去掉**（模拟查询失败）→ 计划关机**照常执行**；
   同时**不许启动任何实例**；`cycle_error` 事件推出来
7. ★ 把系统时间调到下月 1 日 0 点 → 触发重置补检，流量归零后被停的实例**被拉起**
8. 一个账号 20 台实例跨 3 region 跑一轮 → **CDT 调用 1 次、DescribeInstances 3 次**
9. 同一实例连续 10 个周期都超阈值 → **只推 1 条通知**（去重生效）
10. 用量到 80% → 收到 `traffic_warning`；到 100% → 收到 `instance_stopped`，两条都收到
11. 手动强制启动 → **有二次确认弹窗**，`audit_log` 里有记录
12. 全仓库 grep `sing-box` / `vless` / `vmess` / `telegram_proxy` → **0 命中**

## 边界

- 只做阿里云 ECS。GCP 保活等 provider 补齐后再说
- 不做实例创建 / 销毁
- 不做按小时的复杂日程（每日一组开关机时间即可）
- 不接管 `node_billing` 的到期提醒（那是 `07-monitoring.md` 的事）
