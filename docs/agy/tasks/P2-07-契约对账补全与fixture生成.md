# P2-07 契约对账补全 + fixture 从服务端生成

> 第二期 · 任务 07 ｜ 前置：P1-27 ✅（已合并）｜ 分支：`agy/p2-07-contract-gen`

> ★ **完成后必须自己 `git push -u origin <分支名>`**，并确认
> `git log --oneline -1 origin/<分支名>` 能看到你的提交。只提交到本地等于没交付。

## 背景

P1-27 已交付：53 个端点的对账表、`toItems()` 统一收口、API 层按域拆分、
`/healthz` 暴露 `dist_fingerprint`。**这些都做对了，不要推翻重做。**

但它的分支基点在 P2-01~04 合并之前，留下两个缺口。

---

## ❶ 补齐第二期端点

对账表与契约测试目前一个第二期端点都没覆盖：

| 模块 | API 端点数 | 对账表命中 |
|---|---|---|
| `notify` | 12 | **0** |
| `cloud` | 14 | **0** |
| `jobs` | 7 | **0** |
| `guard` | 6 | **0** |

照 P1-27 已经建立的格式往 `docs/12-api-spec.md` 的对账表里加行，
每行三列（服务端实际返回 / 前端声明 / 对齐状态），**逐个核对而不是照抄类型标注**。

### 已知的两处形状不符，一并修掉

验收 P2-01 与 P2-03 时就记录了，至今未改：

| 端点 | 现状 | 应为 |
|---|---|---|
| `GET /api/v1/notify/channels`、`/rules` | `{items}` | `{items, total, page, page_size}` |
| `GET /api/v1/jobs` | `{jobs, total}` | `{items, total, page, page_size}` |

`12-api-spec.md` §3 写得很明确：**所有 GET 列表端点一律返回同一个信封，没有例外。**
改服务端，前端跟着走 `toItems()`。

★ 顺带复查 `cloud` 与 `guard` 的列表端点是不是也各写各的。

## ❷ ★ fixture 必须由服务端生成

P1-27 的 `web/test/fixtures/contracts/*.json` 共 14 份，**全是手写的** ——
没有任何 Go 代码或脚本会产出它们。

这让整套机制少了一半：**能挡住前端改坏，挡不住服务端漂移。**
而最初那次 `y.map` 事故恰恰是服务端返回信封、前端以为是裸数组。
fixture 一旦与真实响应脱节，测试会**绿着骗人**，比没有测试更危险。

### 要做的

1. 写一个 Go 测试（或 `go run` 的生成器），对每个列表/详情端点起 `httptest`，
   用**真实 handler** 打一遍，把响应体写进 `web/test/fixtures/contracts/<name>.json`
2. ★ **每个端点至少两份 fixture：有数据、空数据。**
   空数据那份是防 `items: null` 回归的关键
3. 提供 `make fixtures` 重新生成
4. ★ **加一道校验**：CI/lint 阶段重新生成一次，
   **与仓库里的 fixture 不一致就失败**，提示「服务端响应变了，请跑 make fixtures 并检查前端是否要跟着改」

这条是整个任务的重点。★ **没有第 4 条，前三条等于白做** ——
生成器不跑就没有意义，跟 P1-25 的守卫是同一个道理。

### 数据从哪来

不要连真实数据库。用 handler 层可注入的假 store / 内存实现构造固定数据，
**fixture 必须可重现**：同样的代码跑两次，产出字节一致
（时间戳、ULID 这类要用固定种子或固定值，不许每次都变，否则第 4 条会天天误报）。

## ❸ 顺带

- `web/src/api/index.ts` 同时 `export * from './nodes'` 与 `export * from './groups'`，
  而 `nodes.ts` 内部又 `export * from './groups'`。
  同一个符号从两条路径导出，TS 允许但冗余。**择一保留**，减少后续歧义
- 新增端点时该往哪加对账行、往哪加 fixture，**写进 `docs/agy/03-rules.md`**，
  否则下一个任务照样漏

## 验收

1. 对账表覆盖**全部**端点，`notify`/`cloud`/`jobs`/`guard` 一个不落，每行三列一致
2. 两处已知形状不符已改：`notify/channels`、`notify/rules`、`jobs` 都返回标准信封
3. ★ `make fixtures` 能重新生成全部 fixture，**连跑两次产出字节一致**
4. ★ 故意改一个服务端 handler 的响应字段名 → **lint/CI 失败**并明确提示跑 `make fixtures`
5. 每个列表端点都有「有数据」与「空数据」两份 fixture，
   空数据那份的 `items` 是 `[]` 不是 `null`
6. 故意把某个前端解析函数改错（比如不解包 `items`）→ 契约测试失败
7. `make lint`、`make test`、`npm test` 全绿
8. `docs/agy/03-rules.md` 里写清了新增端点的两处登记义务

## 边界

- 不引入 OpenAPI / 代码生成框架
- 不改 P1-27 已建立的对账表格式与 `toItems()` 收口方式
- 不动 events 的 `limit/offset` 分页（历史差异，`12-api-spec.md` 已标注）

---

# 验收记录

## 第 1 轮 · 2026-09-08 · ✅ 通过（合并时修掉 1 处）

分支 `agy/p2-07-contract-gen`，提交 `5fdd897`。

| 验收项 | 实测 |
|---|---|
| 1 对账表覆盖 | ✅ notify 12 / cloud 11 / jobs 7 / guard 6 行全部补齐（billing 属 P2-09，当时未合并） |
| 2 两处形状不符 | ✅ `notify/channels`、`notify/rules`、`jobs` 都改成返回 `items` |
| 3 `make fixtures` 可重现 | ✅ **连跑两次字节一致** |
| 5 空列表 fixture | ✅ **13 份 `*_empty.json`，`items` 全是 `[]` 不是 `null`** |
| 8 `03-rules.md` 登记义务 | ✅ 写了两条义务 + 一条提交前检查清单项 |
| Go 测试 | ✅ 34 个包全绿 |

★ **`cmd/gen-fixtures` 生成器真做出来了**，这是 P1-27 缺的那一半 ——
fixture 从此由服务端真实 handler 产出，不再是手写的。

### F1 · 交付时提交的 fixture 不是生成器产物（合并时已修）

`lint-fixtures.sh` 在**提交状态下直接失败**，14 份对不上：

```
❌ 服务端响应变了，请跑 make fixtures ... (不匹配: tags_empty.json)
❌ ... (不匹配: enroll_tokens.json / events.json / event_types.json ...)
```

差异是 P1-27 手写残留没被覆盖，例如 `tags_empty.json` 里空列表却写着
`"total": 7, "page": 9999`，生成器产出的是正确的 `"total": 0, "page": 1`。

★ **守卫本身是有效的 —— 它正确抓到了这次漂移**，问题只是交付时忘了把
生成结果提交进去。合并时已跑 `make fixtures` 重新生成 14 份并提交，现在 `lint-fixtures` 通过。

**任务 P2-07 通过。**
