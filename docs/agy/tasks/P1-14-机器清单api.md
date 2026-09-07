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
