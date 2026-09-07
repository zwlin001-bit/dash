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
