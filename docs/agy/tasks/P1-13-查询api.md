# P1-13 时序查询 API

> 第一期 · 任务 13/19 ｜ 前置：04 ｜ 分支：`agy/p1-13-query-api`
> 设计依据：[`../../02-database.md`](../../02-database.md) §6

## 目标

前端画图要的那几个接口。**不依赖上报链路**，用种子数据自测。

## 交付物

```
internal/api/metrics/
  GET /api/v1/nodes/{id}/metrics?from=&to=&fields=   时序查询
  GET /api/v1/nodes/{id}/latest                      最新值（读内存缓存）
  GET /api/v1/metrics/stream                         SSE 实时推送
  README.md
```

★ **开工前先读 [`../../08-field-map.md`](../../08-field-map.md)**，字段名、单位、聚合方式全部以它为准。

## 端点规格

★ 端点、参数、返回结构在 [`../../12-api-spec.md`](../../12-api-spec.md) §6，
返回的列式结构在 [`../../08-field-map.md`](../../08-field-map.md) §4。两处都要对照。

### 抽稀算法

raw 表点数超 `max_points` 时：

```
step = ceil(n / max_points)
每 step 个点取一个窗口 → ★ 取该窗口的最大值，不是第一个点
```

★ **不要用 LTTB 之类的视觉抽稀，也不要取平均或首个点。**
监控图表里**丢掉尖峰比丢掉平均值严重得多**——用户看图就是为了找那个尖峰。
时间戳取窗口内被选中那个点的真实时间戳。

`net_total_*` 这类计数器抽稀时取窗口**最后一个**值（单调递增，取最大等价）。

### SSE

- 每 25 秒发 `ping` 事件保活，防止反代掐断
- ★ 数据源是**内存最新值缓存，不查库**
- 客户端断开必须正确关闭 goroutine 和 channel，**验收会跑一次连接泄漏检查**

## 约束

- **按时间跨度自动选表**：
  | 跨度 | 用表 |
  |---|---|
  | ≤ 6 小时 | `sample_host` |
  | ≤ 3 天 | `sample_host_1m` |
  | ≤ 60 天 | `sample_host_1h` |
  | 更长 | `sample_host_1d` |
- **返回点数上限 400**，超出在应用层抽稀
- **禁止在时序表上做 join。** 要节点名等信息让前端自己拼
- 查询固定形状，走主键范围扫描：
  ```sql
  SELECT ts_ms, cpu_pct, mem_used, ... FROM sample_host
  WHERE node_id = ? AND ts_ms >= ? AND ts_ms < ? ORDER BY ts_ms
  ```
- 列表类接口用 `dialect.Paginate`，**不许裸写 `LIMIT` / `FETCH`**
- SSE 推的是**内存最新值缓存**，不轮询数据库
- 返回值的形状要和任务 15 的图表组件入参对齐，**两边定好一次，别各改各的**

## 验收

1. 查 6 小时 / 3 天 / 60 天 / 1 年四个跨度，**分别命中 raw / 1m / 1h / 1d**
   （在响应里带上实际用了哪张表，方便验证）
2. 四个跨度返回点数都 ≤ 400
3. 单次查询 **< 500ms**（PR 里贴实际耗时）
4. `scripts/lint-sql.sh` 通过
5. SSE 连接在数据库不可用时仍能推送最新值

## 边界

只做时序相关接口，节点/分组/标签的 CRUD 是任务 14。

---

# 验收记录

## 第 1 轮 · 2026-09-07 · ✅ 通过

分支 `agy/p1-13-query-api`，提交 `2374b8b`。已合并到 main。

| 验收项 | 实测 |
|---|---|
| 测试 | ✅ `internal/api/metrics` 通过（含 226 行 stream_test） |
| 选表规则 | ✅ `sample_host` / `_1m`(60s) / `_1h`(3600s) / `_1d`(86400s) 四档齐全，返回带 `source` |
| 点数上限 400 | ✅ |
| ★ 抽稀取窗口最大值 | ✅ 非计数器取窗口内 primary 字段的最大值，**计数器取窗口最后一个点**（单调递增，语义正确） |
| 时间戳取被选中点的真实值 | ✅ |
| SSE 25 秒保活 | ✅ |
| 可移植 SQL | ✅ lint 通过 |

**任务 13 关闭。**
