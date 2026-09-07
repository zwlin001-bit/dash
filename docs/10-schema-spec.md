# 10 表结构规格

**这是建表的唯一权威。** `02-database.md` 讲的是「为什么这么设计」，本文件是「照着写就行」。
任务 04（建表）、11（落库）、12（rollup）、13（查询）、14（清单）、20（事件）都以本文件为准。

---

## 0. 通用规则

**抽象类型 → 方言**（完整说明见 `02-database.md` §1.2）：

| 抽象 | Oracle | MySQL 8 |
|---|---|---|
| `id` | `VARCHAR2(26)` | `VARCHAR(26)` |
| `str(n)` | `VARCHAR2(n CHAR)` | `VARCHAR(n)` |
| `text` | `CLOB` | `LONGTEXT` |
| `i32` | `NUMBER(10)` | `INT` |
| `i64` / `ts` | `NUMBER(19)` | `BIGINT` |
| `f64` | `BINARY_DOUBLE` | `DOUBLE` |
| `bool` | `NUMBER(1)` | `TINYINT` |
| `bin` | `BLOB` | `LONGBLOB` |

**逐条照做**：

1. **不声明任何外键约束。** 关系全部由应用层维护。
   理由：Oracle/MySQL 的 FK 行为有差异、删除顺序会耦合、时序表插入会变慢。
   `02-database.md` 里凡是提到外键的地方，以本条为准
2. **可空列不给默认值**；非空列必须给默认值或由应用赋值，**不依赖数据库默认**
3. 除非本文件写了 `DEFAULT`，否则**不要加数据库默认值**
4. `bool` 列只存 0/1，非空，默认 0
5. 所有 `created_at_ms` / `updated_at_ms` 非空，由应用赋值
6. 索引名照本文件写的，**不要自己起名**
7. 唯一索引建在可空列上时，两个数据库都允许多个 NULL —— 这是有意的（如 `agent_token_hash`）
8. 时序表**只有主键，不建额外索引**。主键就是查询模式

---

## 1. 系统表

### `schema_migrations`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| version_no | i32 | 否 | 迁移编号 |
| name | str(80) | 否 | |
| checksum | str(64) | 否 | 文件内容哈希，防止已执行的迁移被改 |
| applied_at_ms | ts | 否 | |

`PK(version_no)`

### `settings`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| setting_key | str(64) | 否 | 如 `site.domain`、`retention.raw_days` |
| setting_val | text | 是 | |
| updated_at_ms | ts | 否 | |

`PK(setting_key)`

### `account_users`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | id | 否 | |
| username | str(64) | 否 | |
| passwd_hash | str(255) | 否 | bcrypt 或 argon2id |
| is_admin | bool | 否 | 默认 0 |
| totp_secret | str(255) | 是 | 第一期不启用，先留列 |
| last_login_at_ms | ts | 是 | |
| created_at_ms | ts | 否 | |
| updated_at_ms | ts | 否 | |

`PK(id)` · `UX ux_users_username (username)`

### `user_sessions`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | id | 否 | |
| user_id | id | 否 | |
| token_hash | str(64) | 否 | 只存哈希 |
| user_agent | text | 是 | |
| ip | str(64) | 是 | |
| expires_at_ms | ts | 否 | |
| created_at_ms | ts | 否 | |

`PK(id)` · `UX ux_sessions_token (token_hash)` · `IX ix_sessions_user (user_id)`

### `audit_log`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | id | 否 | |
| actor_kind | str(16) | 否 | `user` / `agent` / `system` |
| actor_id | str(26) | 是 | |
| action | str(64) | 否 | 如 `node.delete` |
| target_kind | str(32) | 是 | |
| target_id | str(26) | 是 | |
| detail | text | 是 | JSON |
| result | str(16) | 否 | `ok` / `failed` |
| ip | str(64) | 是 | |
| created_at_ms | ts | 否 | |

`PK(id)` · `IX ix_audit_created (created_at_ms)` · `IX ix_audit_target (target_kind, target_id)`

---

## 2. 节点与清单

### `node_groups`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | id | 否 | |
| name | str(64) | 否 | |
| display_order | i32 | 否 | 默认 0 |
| created_at_ms | ts | 否 | |
| updated_at_ms | ts | 否 | |

`PK(id)` · `UX ux_groups_name (name)`

### `tags`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | id | 否 | |
| name | str(48) | 否 | |
| color | str(16) | 是 | |
| created_at_ms | ts | 否 | |
| updated_at_ms | ts | 否 | |

`PK(id)` · `UX ux_tags_name (name)`

### `node_tags`
| 列 | 类型 | 空 |
|---|---|---|
| node_id | id | 否 |
| tag_id | id | 否 |

`PK(node_id, tag_id)` · `IX ix_node_tags_tag (tag_id)`

### `nodes`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | id | 否 | |
| name | str(64) | 否 | |
| node_group_id | id | 是 | |
| display_order | i32 | 否 | 默认 0 |
| is_hidden | bool | 否 | 默认 0 |
| agent_token_hash | str(64) | 是 | 吊销即置 NULL |
| agent_version | str(32) | 是 | |
| conn_state | str(16) | 否 | `never` / `online` / `offline`，默认 `never` |
| last_seen_at_ms | ts | 是 | |
| clock_skew_ms | i64 | 是 | 最近一次时钟偏差；超阈值时界面标记 |
| note | text | 是 | |
| created_at_ms | ts | 否 | |
| updated_at_ms | ts | 否 | |

`PK(id)` · `UX ux_nodes_token (agent_token_hash)` · `IX ix_nodes_group (node_group_id)`

> `provider_code` / `cloud_resource_id` 是第四期云集成的列，**第一期不建**。

### `node_facts`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| node_id | id | 否 | |
| arch | str(16) | 是 | |
| os_name | str(64) | 是 | |
| os_version | str(48) | 是 | |
| kernel | str(64) | 是 | |
| virt | str(32) | 是 | |
| cpu_model | str(128) | 是 | |
| cpu_cores | i32 | 是 | 物理核 |
| cpu_threads | i32 | 是 | 逻辑核 |
| mem_total | i64 | 是 | |
| swap_total | i64 | 是 | |
| disk_total | i64 | 是 | |
| ipv4 | str(64) | 是 | |
| ipv6 | str(128) | 是 | |
| boot_at_ms | ts | 是 | |
| facts_hash | str(32) | 是 | 不含 `boot_at_ms` |
| updated_at_ms | ts | 否 | |

`PK(node_id)`

### `node_billing`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| node_id | id | 否 | |
| currency | str(8) | 是 | |
| price | f64 | 是 | |
| cycle_days | i32 | 是 | |
| is_auto_renew | bool | 否 | 默认 0 |
| expires_at_ms | ts | 是 | |
| traffic_limit | i64 | 是 | 字节 |
| traffic_limit_kind | str(8) | 是 | `sum`/`max`/`min`/`up`/`down` |
| traffic_reset_day | i32 | 是 | 每月几号，1–28 |
| updated_at_ms | ts | 否 | |

`PK(node_id)`

### `enroll_tokens`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | id | 否 | |
| token_hash | str(64) | 否 | |
| preset_name | str(64) | 是 | 预绑定的节点名 |
| preset_group_id | id | 是 | |
| expires_at_ms | ts | 否 | 签发 + 15 分钟 |
| used_at_ms | ts | 是 | 非空即已作废 |
| used_node_id | id | 是 | |
| created_by | id | 是 | |
| created_at_ms | ts | 否 | |

`PK(id)` · `UX ux_enroll_token (token_hash)`

---

## 3. 时序表

### `metric_defs`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| metric_code | str(40) | 否 | 如 `disk.used` |
| display_name | str(64) | 否 | |
| unit | str(16) | 是 | `bytes` / `%` / `ms` … |
| value_kind | str(16) | 否 | `gauge` / `counter` / `delta` |
| created_at_ms | ts | 否 | |
| updated_at_ms | ts | 否 | |

`PK(metric_code)`

### `metric_series`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | id | 否 | |
| node_id | id | 否 | |
| metric_code | str(40) | 否 | |
| dim_key | str(128) | 否 | 挂载点 / 网卡名 |
| dim_json | text | 是 | |
| first_at_ms | ts | 否 | |
| last_at_ms | ts | 否 | |
| created_at_ms | ts | 否 | |
| updated_at_ms | ts | 否 | |

`PK(id)` · `UX ux_series (node_id, metric_code, dim_key)`

### `sample_host`

主机级原始样本，一行一次采样。字段语义见 `08-field-map.md` §1。

| 列 | 类型 | 空 |
|---|---|---|
| node_id | id | 否 |
| ts_ms | ts | 否 |
| cpu_pct | f64 | 是 |
| mem_used | i64 | 是 |
| swap_used | i64 | 是 |
| load1 | f64 | 是 |
| load5 | f64 | 是 |
| load15 | f64 | 是 |
| disk_used | i64 | 是 |
| net_up_bps | i64 | 是 |
| net_down_bps | i64 | 是 |
| net_total_up | i64 | 是 |
| net_total_down | i64 | 是 |
| traffic_up | i64 | 是 |
| traffic_down | i64 | 是 |
| proc_count | i32 | 是 |
| tcp_count | i32 | 是 |
| udp_count | i32 | 是 |
| uptime_s | i64 | 是 |

`PK(node_id, ts_ms)` · **不建其他索引**

> 所有指标列可空。**采集失败写 NULL，绝不写 0。**
> Oracle 侧建议 `ORGANIZATION INDEX`（IOT），写在 `.oracle.sql` 里；
> MySQL 的 InnoDB 聚簇主键天然等价，`.mysql.sql` 不需要额外语句。

### `sample_host_1m` / `sample_host_1h` / `sample_host_1d`

**三张表列完全相同。** 按下面的规则从 `sample_host` 的列机械展开：

| 源列 | 聚合类别 | 展开成 |
|---|---|---|
| cpu_pct · mem_used · swap_used · load1 · load5 · load15 · disk_used · net_up_bps · net_down_bps · proc_count · tcp_count · udp_count | **gauge** | `<列>_avg` `<列>_max` `<列>_min`（各 f64） |
| net_total_up · net_total_down · uptime_s | **counter** | `<列>_last`（i64） |
| traffic_up · traffic_down | **delta** | `<列>_sum`（i64） |

固定列：

| 列 | 类型 | 空 |
|---|---|---|
| node_id | id | 否 |
| bucket_ms | ts | 否 |
| sample_cnt | i32 | 否 |

共 3 + 12×3 + 3 + 2 = **44 列**。

`PK(node_id, bucket_ms)` · **不建其他索引**

> `<列>_avg` 一律 `f64`，即使源列是 `i32`/`i64`——平均值不是整数。
> `_max` / `_min` 也用 `f64`，保持三个聚合列类型一致，避免查询层做类型分支。

### `sample_dim`
| 列 | 类型 | 空 |
|---|---|---|
| series_id | id | 否 |
| ts_ms | ts | 否 |
| val | f64 | 是 |

`PK(series_id, ts_ms)`

### `sample_dim_1m` / `_1h` / `_1d`
| 列 | 类型 | 空 |
|---|---|---|
| series_id | id | 否 |
| bucket_ms | ts | 否 |
| sample_cnt | i32 | 否 |
| val_avg | f64 | 是 |
| val_max | f64 | 是 |
| val_min | f64 | 是 |
| val_last | f64 | 是 |

`PK(series_id, bucket_ms)`

---

## 4. 事件（迁移 0002，任务 20）

### `event_types`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| event_type | str(64) | 否 | 如 `node.offline` |
| display_name | str(64) | 否 | |
| default_severity | str(16) | 否 | 代码内置，不可改 |
| severity | str(16) | 否 | 用户覆盖值，初始 = default |
| default_disposition | str(16) | 否 | 代码内置，不可改 |
| disposition | str(16) | 否 | 用户覆盖值，初始 = default |
| description | text | 是 | |
| is_builtin | bool | 否 | 默认 1 |
| updated_at_ms | ts | 否 | |

`PK(event_type)`

> 存 `default_*` 和用户覆盖值两份，**才能提供「恢复默认」**，
> 也能在升级时识别出哪些是用户改过的。

### `events`
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | id | 否 | |
| event_type | str(64) | 否 | |
| severity | str(16) | 否 | 产生时的快照，不随类型配置变化 |
| source_module | str(32) | 否 | |
| target_kind | str(32) | 是 | |
| target_id | str(26) | 是 | |
| title | str(255) | 否 | |
| payload_json | text | 是 | 模板变量 |
| dedup_key | str(128) | 是 | 如 `node.offline:<node_id>` |
| is_read | bool | 否 | 默认 0 |
| occurred_at_ms | ts | 否 | |
| created_at_ms | ts | 否 | |

`PK(id)` · `IX ix_events_occurred (occurred_at_ms)` ·
`IX ix_events_type (event_type, occurred_at_ms)` ·
`IX ix_events_target (target_kind, target_id, occurred_at_ms)`

---

## 5. 保留期

存 `settings`，键名与默认值：

| 键 | 默认 | 作用表 |
|---|---|---|
| `retention.raw_days` | 3 | `sample_host` `sample_dim` |
| `retention.1m_days` | 30 | `sample_host_1m` `sample_dim_1m` |
| `retention.1h_days` | 400 | `sample_host_1h` `sample_dim_1h` |
| `retention.1d_days` | 0 | 0 = 永久 |
| `retention.events_days` | 90 | `events` |
| `retention.audit_days` | 365 | `audit_log` |

30 台 / 5 秒的稳态体量约 **900 MB**，ADB Always Free 的 20 GB 留有扩到 100 台的空间。
