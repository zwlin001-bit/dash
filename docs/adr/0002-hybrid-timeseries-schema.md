# ADR 0002 时序数据用「宽表 + 窄表」混合模型

**状态**：已采纳 · 2026-09-07

## 背景
komari 的 `pkg/metric` 是完整的归一化时序模型：
metric 定义表、labels 表、series 表、resolutions 表、rollups 表，
外加 t-digest 分位数摘要存 BLOB，约 1.7 万行代码。

dash 的实际负载是 **30 台机器、5 秒间隔、指标种类基本固定**。

## 选项
1. **照抄归一化星型模型**。极致灵活，任何指标不改表。
   代价：一次图表查询 join 四张表；t-digest BLOB 不可移植到 MySQL；
   行数是宽表的 12 倍（每指标一行 vs 每次采样一行）；实现和调试成本极高。
2. **纯宽表**（komari 早期的 `Record` 表）。简单快，但每块磁盘、每张网卡、
   每个 ping 目标这类不定维度的指标塞不进去。
3. **混合**：主机级固定指标走宽表，带维度的指标走窄表。

## 决定
选 3。

- `sample_host`（宽表）：一行一次采样，约 18 列。30 台 × 5s = 6 行/秒。
  图表查询 = 主键范围扫描，无 join。
- `sample_dim` + `metric_series`（窄表）：磁盘 / 网卡 / GPU / ping 等不定维度。
- 两者各自有 1m / 1h / 1d 三级 rollup，用可移植的 `INSERT ... SELECT ... GROUP BY FLOOR()` 生成。

## 代价
- 新增一个主机级指标需要一次 `ALTER TABLE ADD COLUMN` + 一条迁移。
  Oracle 和 MySQL 8 上加可空列都是元数据操作，几乎零成本，可以接受。
- 放弃精确分位数（p95/p99）。只保留 avg / min / max / last。
  对个人 VPS 监控，分位数不是决策依据，不值得为它引入不可移植的摘要结构。

## 数字
稳态约 750 MB（raw 3 天 / 1m 30 天 / 1h 400 天 / 1d 永久），
ADB Always Free 的 20 GB 留有扩到 100 台的空间。
