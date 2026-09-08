# 12 接口规格

**任务 10、13、14、16、17、20 的实现依据。** 端点、参数、返回、状态码全在这里，
后端照着实现，前端照着调，不需要互相打听。

---

## 1. 通用约定

**基址**：控制台 `/api/v1`，Agent `/api/agent/v1`

**认证**
- ★ **控制台 API 默认全部需要登录，免鉴权的是显式白名单**（见下）。
  未登录一律 401，**不要重定向**
- 免鉴权白名单：`POST /api/v1/login`、`/api/agent/v1/**`、`/healthz`、
  `/install.sh`、`/dl/**`、前端静态资源与 SPA 路由
- 控制台：登录后下发 `dash_session` Cookie（`HttpOnly` + `Secure` + `SameSite=Lax`）
- Agent：`Authorization: Bearer <agent_token>`
- 未认证一律 `401`，**不要重定向**（前端自己处理跳转）

**时间**：所有时间字段都是 `*_ms`，epoch 毫秒整数。**接口里不出现日期字符串。**

**错误**：非 2xx 一律返回

```json
{"error":{"code":"node_not_found","message":"节点不存在","detail":null}}
```

`code` 是机器可读的稳定标识，前端按它分支；`message` 给人看，可以改。

| HTTP | 何时用 |
|---|---|
| 400 | 参数校验失败（`code: invalid_param`，`detail` 指出哪个字段） |
| 401 | 未登录 / token 无效 |
| 403 | 已登录但无权限 |
| 404 | 资源不存在 |
| 409 | 唯一性冲突（`code: duplicate_name` 等） |
| 429 | 限流 |
| 500 | 服务端错误。**`message` 不许泄露 SQL 或堆栈** |

**分页**：请求 `?page=1&page_size=50`（`page` 从 1 起，`page_size` 上限 200）

```json
{"items":[…],"total":123,"page":1,"page_size":50}
```

★ **所有 `GET` 列表端点一律返回这个信封，没有例外，也没有「小列表直接返回裸数组」的特例。**
适用范围点名如下，前端不许凭猜测决定要不要拆 `items`：

| 端点 | 响应 |
|---|---|
| `GET /api/v1/nodes` | `{items:[Node…],total,page,page_size}` |
| `GET /api/v1/node-groups` | `{items:[Group…],total,page,page_size}` |
| `GET /api/v1/tags` | `{items:[Tag…],total,page,page_size}` |
| `GET /api/v1/enroll-tokens` | `{items:[EnrollToken…],total,page,page_size}` |
| `GET /api/v1/events` | `{items:[Event…],total,limit,offset}` —— ⚠️ 历史遗留，用 `limit/offset` 而非 `page/page_size`，见 P1-27 |

★ **`items` 在空列表时必须是 `[]`，不许是 `null`。**
Go 的 `var items []*T` 零值会被 `encoding/json` 编码成 `null`，
返回前必须做一次 `if items == nil { items = []*T{} }` 归一。
`null` 传到前端就是 `null.map`，整页崩。

**列表排序**：`?sort=name&order=asc`，允许排序的字段每个接口单独列出，**不接受任意字段**（防注入）。

---

## 2. Agent 端点

### `POST /api/agent/v1/enroll`

无需认证，用一次性 enrollment token 换长期 token。

```jsonc
// 请求
{"enroll_token":"…", "facts":{ /* FactsParams */ }}
// 200
{"node_id":"01J…","agent_token":"…"}   // agent_token 明文只此一次
```

失败：`400 invalid_enroll_token` / `410 enroll_token_used` / `410 enroll_token_expired`

### `GET /api/agent/v1/rpc` （WebSocket）

`Authorization: Bearer <agent_token>` → 升级为 WebSocket，之后走 JSON-RPC（`04-protocol.md`）。

- token 无效：升级前返回 `401`，**不要升级成功后再用 RPC 错误码告知**
- 协议版本不兼容：升级成功后回 `-32001` 再关闭
- 同一 node 已有连接：**踢掉旧连接**，新连接正常服务

### `POST /api/agent/v1/report` （HTTP 回退）

WS 不可用时用它。**请求体是一批 JSON-RPC notification 的数组**，
**响应体是服务端要下发的一批 notification**——下行指令靠这个回带。

```jsonc
// 请求
[ {"jsonrpc":"2.0","method":"agent.metrics","params":{…}} ]
// 200
{"server_time_ms":1757222400123,
 "commands":[ {"jsonrpc":"2.0","method":"server.config","params":{…}} ]}
```

`commands` 为空数组时表示无指令。agent 按收到的顺序处理。

### `GET /dl/dash-agent-linux-<arch>` · `GET /install.sh`

公开，无需认证。`/install.sh` 返回安装脚本，`?token=` 参数会被填进脚本。

---

## 3. 认证

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/login` | `{"username","password"}` → 200 + Set-Cookie；失败 `401 bad_credentials` |
| POST | `/api/v1/logout` | 作废当前会话 |
| GET | `/api/v1/me` | 返回当前用户 |
| POST | `/api/v1/me/password` | `{"old_password","new_password"}` |

**登录失败要限流**：同一用户名或 IP 连续失败 5 次后，锁定 15 分钟，返回 `429`。
每次失败产生 `auth.login_failed` 事件。

---

## 4. 节点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/nodes` | 列表。过滤：`group_id` `tag_id` `conn_state` `q`（名称模糊）。排序字段：`name` `display_order` `last_seen_at_ms` |
| POST | `/api/v1/nodes` | 手工建节点（少用，通常靠 enroll） |
| GET | `/api/v1/nodes/{id}` | 详情：节点 + facts + billing + tags |
| PATCH | `/api/v1/nodes/{id}` | 部分更新。**只更新请求体里出现的字段** |
| DELETE | `/api/v1/nodes/{id}` | 见 §4.1 |
| GET | `/api/v1/nodes/{id}/facts` | |
| GET/PUT | `/api/v1/nodes/{id}/billing` | |
| POST | `/api/v1/nodes/{id}/tags` | `{"tag_ids":[…]}` 全量替换该节点的标签，响应 `{"ok":true}`（**不返回标签列表**） |
| POST | `/api/v1/nodes/tags:batch` | `{"node_ids":[…],"tag_ids":[…],"op":"add"\|"remove"}` **批量打标签** |
| POST | `/api/v1/nodes/{id}/token:revoke` | 吊销 agent token |

★ **可选字段：下列字段在无数据时会被后端 `omitempty` 省略，前端必须按「可能不存在」处理**——
`group`（未分组）、`billing`（未配置计费）、`latest`（从未上报或离线）、
`latest` 内的各指标（采集失败时单项省略）、`clock_skew_ms`（未测得）、`facts`（未上报 facts）。
**不要用 `|| 0` 掩盖缺失**：监控界面上 `0%` 和「没数据」是两回事。

列表项返回（总览页用，**一次请求拿齐，不要前端 N+1**；下例是字段齐全的情况）：

```jsonc
{"id":"01J…","name":"hk-01","conn_state":"online","last_seen_at_ms":…,
 "group":{"id":"…","name":"香港"},
 "tags":[{"id":"…","name":"proxy","color":"#3b82f6"}],
 "latest":{"cpu_pct":3.5,"mem_used":412000000,"mem_total":2147483648,
           "disk_used":…,"disk_total":…,"net_up_bps":…,"net_down_bps":…,
           "traffic_month_up":…,"traffic_month_down":…,"ts_ms":…},
 "billing":{"expires_at_ms":…,"traffic_limit":…},
 "clock_skew_ms":0}
```

★ `latest` **读服务端内存缓存，不查库**。数据库短暂不可用时这个接口仍要能返回。

### 4.1 删除节点

应用层按顺序删（**不依赖外键级联**）：
`node_tags` → `node_facts` → `node_billing` → `nodes`。

★ **时序数据不同步删**，交给保留期自然过期——同步删会造成长事务。
接口要在响应里说明这一点，前端二次确认时展示给用户。

---

## 5. 分组 · 标签 · 注册 token

| 方法 | 路径 |
|---|---|
| GET/POST | `/api/v1/node-groups` |
| PATCH/DELETE | `/api/v1/node-groups/{id}` |
| GET/POST | `/api/v1/tags` |
| PATCH/DELETE | `/api/v1/tags/{id}` |
| GET/POST | `/api/v1/enroll-tokens` |
| DELETE | `/api/v1/enroll-tokens/{id}` |

- 删分组：把其下节点的 `node_group_id` 置 NULL，**不级联删节点**
- 删标签：先删 `node_tags` 里的关联行，再删标签
- `POST /api/v1/enroll-tokens` 返回里带 **完整的一键安装命令**：
  ```json
  {"id":"…","token":"…","expires_at_ms":…,
   "install_cmd":"curl -fsSL https://dash.example.com/install.sh | sh -s -- --enroll <token>"}
  ```
  token 明文只在创建时返回一次。

---

## 6. 时序查询

### `GET /api/v1/nodes/{id}/metrics`

| 参数 | 说明 |
|---|---|
| `from_ms` `to_ms` | 必填 |
| `fields` | 逗号分隔。raw 表写列名（`cpu_pct`）；rollup 表可带聚合后缀（`cpu_pct:max`），默认 `:avg` |
| `max_points` | 默认 400，上限 1000 |

返回结构见 `08-field-map.md` §4（列式）。`source` 字段回带实际用了哪张表。

**选表**：`≤6h`→`sample_host`、`≤3d`→`_1m`、`≤60d`→`_1h`、更长→`_1d`。

**抽稀**（raw 表点数超 `max_points` 时）：
★ **等距取样，不做 LTTB 之类的视觉抽稀**。
按 `ceil(n / max_points)` 的步长取，**每个窗口取该窗口的最大值而不是第一个点**——
监控图表里丢掉尖峰比丢掉平均值严重得多。

### `GET /api/v1/nodes/{id}/latest`

读内存缓存，返回单点。

### `GET /api/v1/stream` （SSE）

推送所有节点的最新值和在线状态变化。

```
event: metrics
data: {"node_id":"01J…","ts_ms":…,"cpu_pct":3.5,…}

event: node_state
data: {"node_id":"01J…","conn_state":"offline","last_seen_at_ms":…}

event: ping
data: {}
```

- 每 25 秒发一次 `ping` 事件保活（防反代掐断）
- ★ 数据源是内存缓存，**不查库**
- 客户端断开要正确清理 goroutine

---

## 7. 事件（任务 20）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/events` | 过滤：`event_type` `severity` `target_id` `is_read` `from_ms` `to_ms` |
| POST | `/api/v1/events/read` | `{"ids":[…]}` 或 `{"before_ms":…}` 批量已读 |
| GET | `/api/v1/events/unread-count` | 界面角标 |
| GET | `/api/v1/event-types` | 类型列表及当前策略 |
| PATCH | `/api/v1/event-types/{event_type}` | 改 `severity` / `disposition`。**不能新增或删除类型** |

---

## 8. 设置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/settings` | 返回全部设置项 |
| PATCH | `/api/v1/settings` | 部分更新 |

第一期可改：`site.domain`（**只读展示**，在线修改是后续任务）、
`retention.*`、`collect.interval_fast_s`、`collect.interval_slow_s`、`collect.enable_conns`。

★ 改 `collect.*` 后要**立即通过已建立的 WS 下发 `server.config`** 给所有在线 agent，
不需要 agent 重连。

---

## 9. 健康检查

`GET /healthz` — 无需认证，`setup.sh` 用它判断启动是否成功。

```json
{"status":"ok","version":"…","db":"ok","agents_online":12}
```

数据库不可用时返回 `503` 且 `db` 字段为 `error`，**但进程不退出**。
