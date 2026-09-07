# P1-14 机器清单 API

> 第一期 · 任务 14/19 ｜ 前置：04、02 ｜ 分支：`agy/p1-14-inventory`
> 设计依据：[`../../02-database.md`](../../02-database.md) §3、§4

## 目标

**「机器清单维护」这一半的核心。** 节点、分组、标签、计费信息的增删改查，
加上登录与注册 token 的管理接口。

## 交付物

```
internal/inventory/
  nodes.go     节点 CRUD、排序、隐藏、备注
  groups.go    分组 CRUD
  tags.go      标签 CRUD + 节点打标签
  facts.go     node_facts 写入与查询
  billing.go   node_billing 增删改
  enroll.go    enrollment token 的生成 / 列表 / 作废
  README.md
internal/auth/
  login.go     账号密码登录、会话
```

接口大致：
```
GET/POST/PATCH/DELETE  /api/v1/nodes            /api/v1/nodes/{id}
GET/POST/PATCH/DELETE  /api/v1/node-groups
GET/POST/PATCH/DELETE  /api/v1/tags
POST/DELETE            /api/v1/nodes/{id}/tags/{tagId}
GET/PATCH              /api/v1/nodes/{id}/billing
POST/GET/DELETE        /api/v1/enroll-tokens
POST                   /api/v1/login    /api/v1/logout
```

## 端点规格

★ 端点、请求体、返回、状态码在 [`../../12-api-spec.md`](../../12-api-spec.md) §3–§5，照着实现。

几个要点：
- 节点列表项要**一次返回齐**（含 group、tags、latest、billing），
  前端不做 N+1。`latest` 读内存缓存不查库
- `POST /api/v1/nodes/tags:batch` 是**批量打标签**接口，30 台机器逐个点太蠢
- 创建 enrollment token 的响应里要带**完整的一键安装命令**（域名从 `settings.site.domain` 取）

## 校验规则

| 字段 | 规则 |
|---|---|
| `name`（节点/分组/标签） | 去首尾空白后 1–64 字符（标签 1–48），不允许纯空白 |
| 唯一性 | 分组名、标签名全局唯一，冲突返回 `409 duplicate_name` |
| `traffic_reset_day` | 1–28（避开 29/30/31 在部分月份不存在的问题） |
| `cycle_days` | > 0 |
| `price` | ≥ 0 |
| `password` | ≥ 8 字符 |
| 排序字段 | 只接受接口声明的白名单，**不接受任意字段**（防注入） |

密码用 **bcrypt cost 12** 或 **argon2id**（`time=1, memory=64MB, threads=4`）。
**不许自己发明哈希方案。**

## 删除顺序

★ **不依赖外键级联**（`10-schema-spec.md` §0 明确不建外键）。按顺序显式删：

- 删节点：`node_tags` → `node_facts` → `node_billing` → `nodes`。
  **时序数据不同步删**，交给保留期过期
- 删分组：其下节点的 `node_group_id` 置 NULL，**不级联删节点**
- 删标签：先删 `node_tags` 关联行，再删 `tags`

整个删除放在**一个事务**里。

## 约束

- **标签走 `tags` + `node_tags` 关联表**，不许退回成分号拼接的字符串。
  komari 就是那么干的，结果标签无法索引、无法反查、改一个要读改写
- 删除逻辑**在应用层显式写全**，不依赖外键级联（P2 的禁令）：
  删节点要一并清理 `node_tags` / `node_facts` / `node_billing`；
  **时序数据不同步删**，交给保留期自然过期（同步删会造成长事务）
- 密码用 bcrypt 或 argon2id 哈希，**不许自己发明**
- 会话 token 存哈希，`user_sessions`
- **所有变更类操作写审计日志**：谁、何时、对哪个资源、做了什么、结果
- 列表接口用 `dialect.Paginate`
- 节点列表要能按分组、标签、在线状态过滤，按名称/排序值排序

## 验收

1. 节点、分组、标签的完整 CRUD 都能跑通
2. **一个节点打多个标签、按标签反查节点**，两个方向都对
3. 删除节点后，`node_tags` / `node_facts` / `node_billing` 里的关联行都被清掉
4. 删除一个仍被节点使用的标签，关联行被正确清理，不留孤儿
5. 登录、登出、会话过期都正确
6. 变更操作在 `audit_log` 里有对应记录
7. `scripts/lint-sql.sh` 通过

## 边界

不做前端页面（任务 17）。不做云资产、代理、告警相关的任何东西。

---

# 验收记录

## 第 1 轮 · 2026-09-07 · ✅ 通过

分支 `agy/p1-14-inventory`，提交 `7d01c8e`。已合并到 main。

| 验收项 | 实测 |
|---|---|
| 测试 | ✅ `internal/inventory`、`internal/auth`、`internal/ulid` 全过 |
| 密码哈希 | ✅ bcrypt / argon2，未自造 |
| 标签走关联表 | ✅ `node_tags`，未退回分号拼接 |
| ★ 删除顺序应用层显式 | ✅ `node_tags` → `node_facts` → `node_billing` → `nodes`，在一个事务里，**未依赖外键级联** |
| 批量打标签 | ✅ |
| 一键安装命令 | ✅ `install_cmd` |
| 审计日志 | ✅ |
| 分页走 dialect | ✅ 未裸写 `LIMIT` |
| 两个 lint | ✅ 均通过 |

**任务 14 关闭。**
