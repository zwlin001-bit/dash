# ADR 0001 用可移植 SQL 子集，而不是 Oracle 原生能力

**状态**：已采纳 · 2026-09-07

## 背景
当前数据库是 Oracle 自治数据库（ADB），但明确要求后续能迁到 MySQL 8。
Oracle 与 MySQL 的差异远不止语法糖：没有 `BIGINT`、空串等于 NULL、
分页语法不同、没有 `AUTO_INCREMENT`、`MERGE` vs `ON DUPLICATE KEY`、占位符风格不同。
「完全通用的 SQL」在工程上不存在。

## 选项
1. **用 ORM 抹平**（GORM 等）。省事，但复杂查询会退化成方言 SQL，
   时序聚合尤其如此；而且 ORM 生成的 DDL 在 Oracle 上经常不是想要的类型。
2. **写两套 SQL**。迁移时不痛，但日常维护双倍成本，且两套必然逐渐不一致。
3. **约束到可移植子集 + 三个方言模板**。

## 决定
选 3。具体见 `PRINCIPLES.md` P2 与 `docs/02-database.md` §1。

关键手法是把「不可移植」的部分从 SQL 里消掉，而不是去兼容它：
- 主键用应用生成的 ULID → 不需要序列 / IDENTITY / AUTO_INCREMENT
- 时间用 epoch 毫秒整数 → 不需要日期类型、时区、`SYSDATE`/`NOW()`
- 布尔用 0/1 整数 → 绕开 Oracle 无 BOOLEAN
- JSON 存文本、应用层解析 → 绕开两边完全不同的 JSON 函数
- 聚合用 `FLOOR(ts_ms/60000)` → 两边语义一致

剩下真正无法消除的只有三处：占位符、分页、upsert/批量插入。
全部收敛到 `internal/db/dialect` 一个包。

## 代价
- 放弃 Oracle 的分区、物化视图、并行查询等能力。对 30 台机器的量级无实际损失。
- DDL 要维护两份。用同一份抽象 schema 生成，且**从第一天就同时写两份**——
  只写一份、迁移时再补，必然腐化。
