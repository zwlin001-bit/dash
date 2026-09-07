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
