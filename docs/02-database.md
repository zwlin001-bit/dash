# 02 数据库设计

后端：**Oracle 自治数据库（ADB）**。目标：**未来可平迁 MySQL 8**，迁移工作量限制在「换一份 DDL + 换 dialect 包的三个模板」。

---

## 1. 可移植性规范

### 1.1 分层：DDL 可分方言，DML 必须通吃

```
migrations/0007_add_alerts.oracle.sql   ← 允许方言
migrations/0007_add_alerts.mysql.sql    ← 允许方言
internal/**/*.go 里的所有 SQL           ← 必须两边都能跑
internal/db/dialect/{oracle,mysql}.go   ← 唯一允许的三处运行时差异
```

### 1.2 抽象类型 → 方言映射

DDL 由同一份抽象 schema 生成，映射表如下。**只允许用这些类型。**

| 抽象类型 | Oracle | MySQL 8 | 说明 |
|---|---|---|---|
| `id` | `VARCHAR2(26)` | `VARCHAR(26)` | ULID。**不用 CHAR**，避免 Oracle 空格补齐比较语义 |
| `str(n)` | `VARCHAR2(n CHAR)` | `VARCHAR(n)` | `CHAR` 语义，n 按字符数算，中文安全 |
| `text` | `CLOB` | `LONGTEXT` | JSON、备注、日志正文 |
| `i32` | `NUMBER(10)` | `INT` | |
| `i64` | `NUMBER(19)` | `BIGINT` | **Oracle 没有 `BIGINT`，不要写** |
| `ts` | `NUMBER(19)` | `BIGINT` | epoch 毫秒，列名后缀 `_at_ms` / `_ms` |
| `f64` | `BINARY_DOUBLE` | `DOUBLE` | 指标值。**不要用 `NUMBER`/`DOUBLE PRECISION`**，Oracle 会退化成软件浮点，聚合慢一个数量级 |
| `bool` | `NUMBER(1)` | `TINYINT` | 只存 0/1，列名 `is_*` / `has_*` |
| `bin` | `BLOB` | `LONGBLOB` | 加密后的凭据 |

补充规则：
- `f64` 列**禁止存 NaN / Infinity**（两边语义不同）。采集端遇到异常值写 NULL。
- 数值列一律显式 `NOT NULL` + 应用赋默认值，或显式可空——不要靠 DB 默认值。

### 1.3 标识符

- 全小写 snake_case，**永远不加引号**（Oracle 不加引号会折叠成大写，MySQL 保持小写；只要两边都不加引号，行为一致）
- 长度 ≤ 30 字符（Oracle 11g 限制 30，MySQL 限制 64，取小）
- 表名复数，关联表 `a_b`，索引 `ix_<表>_<列缩写>`，唯一索引 `ux_<表>_<列缩写>`

**保留字黑名单**（Oracle 与 MySQL 8 并集里的常见坑，绝对不要作为表名或列名）：

```
access  add     all     alter   and     any     as      asc     audit
between by      char    check   cluster column  comment compress connect
create  current date    decimal default delete  desc    distinct drop
else    end     exclusive exists file  float   for     from    grant
group   groups  having  identified immediate in  increment index
initial insert  integer intersect interval into is      key     keys
level   like    lock    long    mode    modify  not     nowait  null
number  of      offline on      online  option  or      order   partition
public  range   rank    raw     read    rename  resource revoke row
rows    select  session set     share   signal  size    smallint start
successful synonym system table then   to      trigger uid     union
unique  update  usage   user    validate values  varchar view    where
window  with    write
```

常见替换：`user`→`account_user`、`group`→`node_group`、`size`→`size_bytes`、
`comment`→`note`、`start`/`end`→`started_at_ms`/`ended_at_ms`、`level`→`severity`、
`mode`→`run_mode`、`status` 可用（非保留字）、`type` 可用但推荐 `kind`。

### 1.4 允许的三处方言差异

**全工程只有 `internal/db/dialect` 这一个包知道当前是什么数据库。** 出现第四处 = 设计错误。

**① 占位符改写**

```
业务代码统一写:  SELECT ... WHERE node_id = ? AND ts_ms >= ?
oracle 改写为 :  SELECT ... WHERE node_id = :1 AND ts_ms >= :2
mysql  原样透传
```

**② 分页**

```
oracle:  <base sql> OFFSET :n ROWS FETCH NEXT :m ROWS ONLY
mysql :  <base sql> LIMIT ? OFFSET ?
```

**③ upsert**（`MERGE` 仅允许出现在这里）

```
oracle:
  MERGE INTO t d USING (SELECT :1 k, :2 v FROM dual) s ON (d.k = s.k)
  WHEN MATCHED THEN UPDATE SET d.v = s.v
  WHEN NOT MATCHED THEN INSERT (k, v) VALUES (s.k, s.v)

mysql:
  INSERT INTO t (k, v) VALUES (?, ?) ON DUPLICATE KEY UPDATE v = VALUES(v)
```

**批量写入**同属 ③ 的范畴：Oracle 走 go-ora 的数组绑定（一次 Exec 传切片），
MySQL 走多行 `VALUES (...),(...)`。对上暴露同一个 `BatchInsert(table, cols, rows)` 接口。

### 1.5 其余硬性禁令

| 禁止 | 替代 |
|---|---|
| 触发器 / 存储过程 / 函数 / 序列 / 物化视图 / DB 定时任务 | 全部在应用层做 |
| 分区表 | 按时间分层表（`sample_host` / `_1m` / `_1h` / `_1d`） |
| `JSON_VALUE` / `JSON_TABLE` / MySQL JSON 函数 | JSON 存 `text` 列，应用层解析。**不对 JSON 内部字段做查询条件** |
| `RETURNING` | 主键应用生成，插入前就知道 |
| 外键 `ON DELETE CASCADE` 依赖 | 声明外键可以，但删除逻辑必须在应用层显式写全，不依赖级联 |
| `SELECT *` | 显式列名 |
| 依赖隐式提交 / 自动提交语义 | 显式事务边界 |
| 空字符串与 NULL 的区别 | 可选字符串一律可空，应用层 `NULL == ""` |

### 1.6 驱动与连接

- Oracle：**`github.com/sijms/go-ora/v2`（纯 Go）**，不用 godror——godror 需要 Oracle Instant Client（C 依赖），会毁掉「单二进制」的部署简单性。
- **ADB 已关闭 mTLS，用连接串（TLS-only）直连，不使用 wallet。**
  连接串与账号写在 `/etc/dash/config.toml`（`0600`），密码支持从环境变量注入。
- 连接池：`MaxOpenConns` 默认 20，`MaxIdleConns` 10，`ConnMaxLifetime` 30min（ADB 会主动断长连接）。
- **ADB 在网络另一端，往返延迟是主要成本**：所有热路径写入必须批量化（见 §5.4）。

### 1.7 迁移

- `migrations/NNNN_<name>.<dialect>.sql`，编号四位递增，**只增不改**
- `schema_migrations(version_no i32 PK, name str(80), applied_at_ms ts, checksum str(64))`
- dashd 启动时检查并按序执行，失败即拒绝启动
- 每个编号必须同时提供 `.oracle.sql` 与 `.mysql.sql`，**即使现在只跑 Oracle**——这是保证可迁移性不腐化的唯一有效手段

---

## 2. 通用列约定

每张业务表都有：

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | `id` | ULID 主键 |
| `created_at_ms` | `ts` | |
| `updated_at_ms` | `ts` | |

时序表例外（见 §5），用复合主键，不带 `id`。

---

## 3. 核心表：账号与系统

```
account_users(id, username str(64) UNIQUE, passwd_hash str(255),
              is_admin bool, totp_secret str(255) NULL,
              last_login_at_ms ts NULL, created_at_ms, updated_at_ms)

user_sessions(id, user_id, token_hash str(64) UNIQUE, user_agent text NULL,
              ip str(64) NULL, expires_at_ms ts, created_at_ms)

api_tokens(id, user_id, name str(64), token_hash str(64) UNIQUE,
           scopes str(255), expires_at_ms ts NULL, revoked_at_ms ts NULL,
           created_at_ms, updated_at_ms)

settings(setting_key str(64) PK, setting_val text NULL, updated_at_ms ts)
  -- 域名存这里: key='site.domain'。setup.sh 首次安装写入，界面可改（见 05-deployment.md）

audit_log(id, actor_kind str(16), actor_id str(26) NULL, action str(64),
          target_kind str(32) NULL, target_id str(26) NULL,
          detail text NULL, result str(16), ip str(64) NULL, created_at_ms ts)
  ix_audit_log_created (created_at_ms)
  ix_audit_log_target  (target_kind, target_id)

credentials(id, name str(64), cred_kind str(32),          -- aliyun_ak / gcp_sa / ssh_key
            enc_payload bin, enc_key_id str(32), enc_nonce str(64),
            created_at_ms, updated_at_ms)
  -- 信封加密。主密钥在 /etc/dash/master.key（0600），绝不入库
```

---

## 4. 核心表：节点与资产

```
nodes(id, name str(64), display_order i32,
      node_group_id NULL, is_hidden bool,
      agent_token_hash str(64) UNIQUE NULL,
      agent_version str(32) NULL,
      last_seen_at_ms ts NULL,
      conn_state str(16),                    -- online / offline / never
      provider_code str(32) NULL,            -- 关联到哪个云
      cloud_resource_id NULL,
      note text NULL,
      created_at_ms, updated_at_ms)
  ux_nodes_token (agent_token_hash)
  ix_nodes_group (node_group_id)

node_groups(id, name str(64), display_order i32, created_at_ms, updated_at_ms)

tags(id, name str(48) UNIQUE, color str(16) NULL, created_at_ms, updated_at_ms)
node_tags(node_id, tag_id, PK(node_id, tag_id))
  ix_node_tags_tag (tag_id)
  -- 取代 komari 的分号拼接字符串：可索引、可反查、改标签不用读改写

node_facts(node_id PK, arch str(16), os_name str(64), os_version str(48),
           kernel str(64), virt str(32) NULL,
           cpu_model str(128) NULL, cpu_cores i32, cpu_threads i32,
           mem_total i64, swap_total i64, disk_total i64,
           gpu_model str(128) NULL,
           ipv4 str(64) NULL, ipv6 str(128) NULL, region str(64) NULL,
           boot_at_ms ts NULL, facts_hash str(32), updated_at_ms ts)
  -- 静态信息。agent 只在 facts_hash 变化时上报（03-agent.md §4）

node_billing(node_id PK, currency str(8), price f64,
             cycle_days i32, is_auto_renew bool,
             expires_at_ms ts NULL,
             traffic_limit i64 NULL, traffic_limit_kind str(8) NULL,
             traffic_reset_day i32 NULL,
             updated_at_ms ts)
```

### 4.1 云资产

```
providers(provider_code str(32) PK, display_name str(64),
          exec_path str(255), is_enabled bool,
          config_json text NULL, created_at_ms, updated_at_ms)

cloud_accounts(id, provider_code, name str(64), credential_id,
               default_region str(64) NULL, is_enabled bool,
               last_sync_at_ms ts NULL, created_at_ms, updated_at_ms)

cloud_resources(id, cloud_account_id, provider_code,
                res_kind str(32),               -- instance / disk / ip / firewall
                res_ref str(191),               -- 云厂商侧 id
                name str(128) NULL, region str(64) NULL, status str(32) NULL,
                public_ips str(255) NULL, private_ips str(255) NULL,
                specs_json text NULL, billing_json text NULL, attrs_json text NULL,
                node_id NULL,
                synced_at_ms ts, is_deleted bool,
                created_at_ms, updated_at_ms)
  ux_cloud_res_ref (cloud_account_id, res_kind, res_ref)
  ix_cloud_res_node (node_id)
```

### 4.2 代理管理

```
proxy_nodes(id, node_id, proxy_kind str(32),      -- 具体协议实现
            listen_port i32, is_enabled bool,
            health_state str(16), last_check_at_ms ts NULL,
            created_at_ms, updated_at_ms)

proxy_configs(id, proxy_node_id, version_no i32,
              config_json text, is_active bool,
              created_by str(26) NULL, created_at_ms, updated_at_ms)
  ux_proxy_cfg_ver (proxy_node_id, version_no)

proxy_deployments(id, proxy_node_id, proxy_config_id, job_id,
                  deploy_state str(16), message text NULL,
                  started_at_ms ts, ended_at_ms ts NULL, created_at_ms, updated_at_ms)
```

### 4.3 三网测速

`speedtest_targets` / `speedtest_tasks` / `speedtest_runs` 三张表，
结构与内置种子数据见 [`06-speedtest.md`](06-speedtest.md) §2。
测速趋势复用 `metric_series` + `sample_dim`，不另建时序表。

### 4.4 Job 引擎

```
jobs(id, job_kind str(48), job_state str(16),
     target_kind str(32) NULL, target_id str(26) NULL,
     params_json text NULL, result_json text NULL, error_msg text NULL,
     progress i32, attempt_no i32, max_attempts i32,
     scheduled_at_ms ts, started_at_ms ts NULL, ended_at_ms ts NULL,
     lease_owner str(64) NULL, lease_until_ms ts NULL,
     created_by str(26) NULL, created_at_ms, updated_at_ms)
  ix_jobs_state_sched (job_state, scheduled_at_ms)
  ix_jobs_target (target_kind, target_id)

job_steps(id, job_id, step_no i32, step_name str(64), step_state str(16),
          log_text text NULL, started_at_ms ts NULL, ended_at_ms ts NULL,
          created_at_ms, updated_at_ms)
  ux_job_steps_no (job_id, step_no)
```

`lease_owner` / `lease_until_ms` 是抢占租约，保证同一 job 不被并发执行；单实例部署也保留，
因为它同时解决「进程崩溃后 job 卡在 running」的恢复问题。

### 4.5 告警

> **第二期内容，第一期不建这些表。** 规则模型、四种 `rule_kind`、评估状态机、
> 通知渠道、到期与流量提醒的完整设计见 [`07-monitoring.md`](07-monitoring.md)，
> 那里还补充了 `ping_tasks` 与 `alert_rule_channels` 两张表。


```
alert_rules(id, name str(64), is_enabled bool,
            scope_kind str(16), scope_ref str(26) NULL,   -- all / group / tag / node
            metric_code str(40), compare_op str(4),        -- gt / lt / gte / lte
            threshold f64, duration_s i32, severity str(16),
            silence_s i32, created_at_ms, updated_at_ms)

alert_events(id, alert_rule_id, node_id, event_state str(16),   -- firing / resolved
             fired_at_ms ts, resolved_at_ms ts NULL,
             peak_value f64 NULL, detail text NULL, created_at_ms, updated_at_ms)
  ix_alert_events_node (node_id, fired_at_ms)

notify_channels(id, name str(64), channel_kind str(32),
                config_json text, is_enabled bool, created_at_ms, updated_at_ms)
```

### 4.6 云账单

> **第二期内容 ([15-cloud-billing.md](15-cloud-billing.md))。** 云厂商 API 账单拉取、按维度汇总与预算告警。

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
  ix_bill_budgets_scope (scope_kind, is_enabled)
```

---

## 5. 时序数据设计

**这是整个数据库设计的核心，也是上一版最不满意的地方。**

### 5.1 为什么不照抄 komari

komari 用归一化星型模型（`metric_defs` / `labels` / `series` / `resolutions` / `rollups`），
一次图表查询要 join 四张表，rollup 里还塞了 t-digest 的 BLOB。
那是给「指标种类不确定、多维度、大规模」准备的。dash 的实际情况是
**30 台机器、指标种类基本固定、5 秒间隔**——用那套模型是纯粹的复杂度税，而且 BLOB 摘要不可移植。

### 5.2 混合模型

| 数据形态 | 存法 | 理由 |
|---|---|---|
| **主机级固定指标**（CPU、内存、负载、网速、总流量……约 15 项） | **宽表**，一行一次采样 | 30 台 × 5s = 6 行/秒；一次图表查询 = 一次单表范围扫描；存储比窄表省 5 倍 |
| **带维度的指标**（每块磁盘、每张网卡、每个 GPU、每个 ping 目标） | **窄表**，series 键 + 时间 + 值 | 维度数量不固定，不能进宽表 |

加一项主机级指标 = 一次 `ALTER TABLE ADD COLUMN`（Oracle / MySQL 8 都是在线操作，几乎零成本）+ 一条迁移。
这是可以接受的代价，换来的是查询和存储的巨大简化。

### 5.3 表结构

**主机级宽表**（raw 与三级 rollup 列形状保持一致，方便统一查询代码）

```
sample_host(
  node_id id, ts_ms ts,
  cpu_pct f64, mem_used i64, swap_used i64,
  load1 f64, load5 f64, load15 f64,
  disk_used i64,
  net_up_bps i64, net_down_bps i64,
  net_total_up i64, net_total_down i64,     -- 累计计数器
  traffic_up i64, traffic_down i64,         -- 本次区间增量（reset 感知后）
  proc_count i32, tcp_count i32, udp_count i32,
  uptime_s i64,
  PK(node_id, ts_ms)
)
```

主键 `(node_id, ts_ms)` 就是查询模式（「某台机最近 N 小时」），无需额外索引。
Oracle 上建议建成 IOT（index-organized table）——但这属于 DDL 层面的方言优化，
写在 `.oracle.sql` 里，MySQL 的 InnoDB 聚簇主键天然等价。

rollup 表列形状：每个 f64/i64 指标展开为 `<col>_avg` / `<col>_max` / `<col>_min`，
计数器类（`net_total_*`）只保留 `_last`，增量类（`traffic_*`）只保留 `_sum`。

```
sample_host_1m(node_id, bucket_ms, sample_cnt i32, <指标聚合列...>, PK(node_id, bucket_ms))
sample_host_1h(同上)
sample_host_1d(同上)
```

**维度窄表**

```
metric_series(id, node_id, metric_code str(40), dim_key str(128),
              dim_json text NULL, first_at_ms ts, last_at_ms ts,
              created_at_ms, updated_at_ms)
  ux_metric_series (node_id, metric_code, dim_key)
  -- dim_key 例: "/dev/vda1" / "eth0" / "gpu0" / ping 任务 id

sample_dim(series_id id, ts_ms ts, val f64, PK(series_id, ts_ms))
sample_dim_1m(series_id, bucket_ms, sample_cnt i32,
              val_avg f64, val_min f64, val_max f64, val_last f64,
              PK(series_id, bucket_ms))
sample_dim_1h(同上)
sample_dim_1d(同上)
```

`metric_defs(metric_code PK, display_name, unit, value_kind, agg_kind, retention_days)`
只用于窄表指标的元信息与前端展示，**不参与查询 join**（前端拿一次缓存起来）。

### 5.4 写入路径

```
agent ──WS──▶ ingest 接收 ──▶ 内存 ring buffer ──▶ 每 5s 批量 flush ──▶ ADB
                                    │
                                    └─▶ 最新值缓存（前端实时视图直接读内存，不查库）
```

- **绝不一条上报一次 INSERT**。ADB 在网络另一端，往返延迟是主要成本。
- 批量大小：30 台 × 5s = 约 30 行主机样本 + 约 15 行维度样本，一次 flush 两条批量语句。
- flush 失败：重试 3 次（退避 1s/2s/4s），仍失败则丢弃最老的一批并计入 `ingest_drop` 指标 + 告警。**监控数据不值得为了不丢而阻塞采集链路。**
- 前端的「实时」视图读内存里的最新值缓存，与落库解耦；即使数据库短暂不可用，实时视图仍然正常。

### 5.5 Rollup

**用可移植 SQL 在数据库内完成，不把数据拉到应用层。**
`FLOOR(ts_ms / 60000) * 60000` 在 Oracle 和 MySQL 上语义一致，这是关键。

```sql
INSERT INTO sample_host_1m
  (node_id, bucket_ms, sample_cnt, cpu_pct_avg, cpu_pct_max, cpu_pct_min,
   mem_used_avg, mem_used_max, net_total_up_last, traffic_up_sum, ...)
SELECT node_id,
       FLOOR(ts_ms / 60000) * 60000,
       COUNT(*),
       AVG(cpu_pct), MAX(cpu_pct), MIN(cpu_pct),
       AVG(mem_used), MAX(mem_used),
       MAX(net_total_up),          -- 计数器在桶内单调，MAX 即 last
       SUM(traffic_up),
       ...
FROM sample_host
WHERE ts_ms >= ? AND ts_ms < ?
GROUP BY node_id, FLOOR(ts_ms / 60000)
```

- 调度：1m rollup 每分钟跑一次，处理「上一分钟已关闭的桶」；1h 每小时；1d 每天。
- **只处理已关闭的时间桶**，避免半个桶被写两次。
- 幂等：先 `DELETE FROM ... WHERE bucket_ms >= ? AND bucket_ms < ?` 再 `INSERT`，整体一个事务。比 upsert 简单且可移植。
- `_1h` 从 `_1m` 聚合，`_1d` 从 `_1h` 聚合（逐级，不回读 raw）。
  注意 avg 的逐级聚合要用 `SUM(x_avg * sample_cnt) / SUM(sample_cnt)` 加权，不能直接 `AVG(x_avg)`。

### 5.6 保留期与清理

| 表 | 保留 | 30 台 / 5s 的估算体量 |
|---|---|---|
| `sample_host` | 3 天 | 518k 行/天 × 3 ≈ 220 MB |
| `sample_host_1m` | 30 天 | 43k 行/天 × 30 ≈ 320 MB |
| `sample_host_1h` | 400 天 | 720 行/天 ≈ 65 MB |
| `sample_host_1d` | 永久 | 可忽略 |
| `sample_dim` | 3 天 | ≈ 50 MB |
| `sample_dim_1m/1h/1d` | 同上比例 | ≈ 80 MB |

**稳态约 750 MB**，ADB Always Free（20 GB）绰绰有余，留足了扩到 100 台的空间。
保留期全部配置化（存 `settings`），改配置不改表。

清理方式（可移植，不需要 `LIMIT`）：

```sql
DELETE FROM sample_host WHERE ts_ms >= ? AND ts_ms < ?
```

每次删一个小时的区间，循环推进到保留边界为止，每轮之间 sleep，避免长事务和 undo 膨胀。
**不用 `TRUNCATE PARTITION`、不用 `DELETE ... LIMIT`。**

### 5.7 计数器 reset 处理

`net_total_up/down` 是单调累计值，机器重启会归零。服务端在写入时对比上一次值：

```
delta = cur >= prev ? cur - prev : cur      // 回绕/重启：本次值即增量
```

per-node 的 `prev` 保存在内存（进程重启后由数据库最近一行恢复）。
`traffic_up/down` 存的就是这个 `delta`，因此月度流量统计 = 对区间做 `SUM`，不受重启影响。
**这是 komari 唯一值得完整照搬的服务端逻辑。**

---

## 6. 查询模式

前端图表请求形如「node X，最近 6 小时，返回 ≤ 400 个点」。选表规则：

| 时间跨度 | 用表 |
|---|---|
| ≤ 6 小时 | `sample_host`（5s 原始，必要时在应用层抽稀） |
| ≤ 3 天 | `sample_host_1m` |
| ≤ 60 天 | `sample_host_1h` |
| 更长 | `sample_host_1d` |

单条查询固定形状：

```sql
SELECT ts_ms, cpu_pct, mem_used, ...
FROM sample_host
WHERE node_id = ? AND ts_ms >= ? AND ts_ms < ?
ORDER BY ts_ms
```

走主键，两边数据库都是范围扫描，无需额外索引。
**禁止在时序表上做 join。** 需要节点名等信息，前端自己拼。
