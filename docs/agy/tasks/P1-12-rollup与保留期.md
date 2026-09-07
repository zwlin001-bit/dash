# P1-12 Rollup 与保留期

> 第一期 · 任务 12/19 ｜ 前置：04 ｜ 分支：`agy/p1-12-rollup`
> 设计依据：[`../../02-database.md`](../../02-database.md) §5.5、§5.6

## 目标

raw → 1m → 1h → 1d 三级降采样，加保留期清理。
**不依赖任务 11**：用种子数据自测即可。

## 交付物

```
internal/ingest/rollup.go      三级 rollup
internal/ingest/retention.go   保留期清理
内部调度器（1m 每分钟 / 1h 每小时 / 1d 每天 / 清理每小时）
```

★ **开工前先读 [`../../08-field-map.md`](../../08-field-map.md)**，字段名、单位、聚合方式全部以它为准。

## 生成与调度

### 44 列的 SQL 别手敲

`sample_host_1m/1h/1d` 各 44 列，`INSERT ... SELECT` 语句很长。
★ **用 Go 代码按 `10-schema-spec.md` §3 的展开规则生成 SQL**（gauge→`_avg/_max/_min`、
counter→`_last`、delta→`_sum`），列表只写一份，三级 rollup 共用。
手敲三遍必然出现列顺序错位，而且那种 bug 只在数值上体现，不会报错。

### 调度窗口

| 级别 | 频率 | 处理窗口 |
|---|---|---|
| 1m | 每分钟第 10 秒 | `[上一分钟起点, 上一分钟终点)` |
| 1h | 每小时第 2 分钟 | `[上一小时起点, 上一小时终点)` |
| 1d | 每天 00:05 | `[昨天 00:00, 今天 00:00)` |

延后一点执行是为了让迟到的上报先落库。**只处理已关闭的桶。**

★ **启动时要补跑**：查 `sample_host_1m` 的 `MAX(bucket_ms)`，
从那里补到当前，避免 dashd 停机期间的数据永远没有 rollup。
补跑按小时分块，不要一条 SQL 扫几天。

### 逐级聚合的加权平均

```sql
-- _1h 从 _1m 聚合，avg 必须加权
SUM(cpu_pct_avg * sample_cnt) / SUM(sample_cnt)   AS cpu_pct_avg,
MAX(cpu_pct_max)                                  AS cpu_pct_max,
MIN(cpu_pct_min)                                  AS cpu_pct_min,
SUM(sample_cnt)                                   AS sample_cnt
```
★ 直接 `AVG(cpu_pct_avg)` 是错的。各分钟样本数不等时结果会偏，
而且**不会报错、肉眼看不出**——这是最阴的一类 bug。

`SUM(sample_cnt) = 0` 时结果写 NULL，不要除零。

### 保留期清理

```sql
DELETE FROM sample_host WHERE ts_ms >= ? AND ts_ms < ?
```
每次删一小时区间，从最老边界推进到保留边界，**每轮之间 sleep 200ms**。
保留期从 `settings` 读（键名见 `10-schema-spec.md` §5）。
`retention.1d_days = 0` 表示永久，跳过该表。

## 约束

- **用可移植 SQL 在数据库内完成，不要把数据拉到应用层再算。**
  `FLOOR(ts_ms/60000)*60000` 在 Oracle 和 MySQL 上语义一致，这是能这么做的关键
  ```sql
  INSERT INTO sample_host_1m (node_id, bucket_ms, sample_cnt, cpu_pct_avg, ...)
  SELECT node_id, FLOOR(ts_ms/60000)*60000, COUNT(*), AVG(cpu_pct), ...
  FROM sample_host WHERE ts_ms >= ? AND ts_ms < ?
  GROUP BY node_id, FLOOR(ts_ms/60000)
  ```
- **只处理已关闭的时间桶**，否则半个桶会被写两次
- **幂等靠「先按 bucket 区间 DELETE 再 INSERT，一个事务」**，不用 upsert（更简单也更可移植）
- `_1h` 从 `_1m` 聚合、`_1d` 从 `_1h` 聚合，**不回读 raw**
- ★ **逐级聚合的 avg 必须加权**：`SUM(x_avg * sample_cnt) / SUM(sample_cnt)`。
  直接 `AVG(x_avg)` 是错的，而且这个 bug 很难被肉眼发现
- 计数器类（`net_total_*`）桶内单调，用 `MAX` 即等于 last；增量类（`traffic_*`）用 `SUM`
- **保留期清理按时间区间分块 DELETE（每次一小时），不用 `LIMIT`**，每轮之间 sleep，
  避免长事务和 undo 膨胀
- 保留期参数存 `settings` 表，可改。默认：raw 3 天 / 1m 30 天 / 1h 400 天 / 1d 永久

## 验收

1. 灌 3 天模拟数据，跑 rollup，**抽查 `_1m` 的 avg/max/min 与 raw 直接算的结果一致**
2. **重复跑 rollup 结果不变**（幂等）
3. `_1h` 的加权 avg 正确：构造一个各分钟样本数不等的用例，验证结果不等于简单平均
4. 清理任务跑完，`sample_host` 里没有超出保留期的行
5. 清理过程中**没有长事务**（在 ADB 上观察，或至少证明每次 DELETE 的区间是有界的）

## 边界

不碰上报链路，不碰查询 API。
