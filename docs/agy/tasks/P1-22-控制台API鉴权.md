# P1-22 控制台 API 鉴权【🔴 安全 · 最高优先级】

> 第一期 · 补充任务 22 ｜ 前置：14 ✅（`RequireAuth` 已存在）｜ 分支：`agy/p1-22-auth`

> ★ **完成后必须自己 `git push -u origin <分支名>`**，并确认
> `git log --oneline -1 origin/<分支名>` 能看到你的提交。只提交到本地等于没交付。

## 问题

**控制台的全部 API 都没有鉴权。** 在线上环境实测（完全不带 cookie、不带任何凭据）：

```
GET    /api/v1/nodes           → 200   返回全部节点
GET    /api/v1/settings        → 200
GET    /api/v1/events          → 200
GET    /api/v1/node-groups     → 200
GET    /api/v1/tags            → 200
POST   /api/v1/node-groups     → 400   ← 是参数校验失败，不是 401
DELETE /api/v1/nodes/{id}      → 404   ← 是节点不存在，不是 401（真 ID 会被删掉）
POST   /api/v1/enroll-tokens   → 200   ← ★ 真的创建出了一个装机令牌
GET    /api/v1/me              → 401   ← 全站只有这一个检查了
```

★ **最严重的是 `POST /api/v1/enroll-tokens`** —— 任何能访问控制台的人都能匿名铸造装机令牌，
把任意机器注册进集群。

**根因**：`RequireAuth` 中间件早就写好了（`internal/auth/login.go:588`），
但除了 `/api/v1/me` **没有任何路由使用它**。
`internal/inventory/module.go` 等模块直接 `mux.HandleFunc(...)` 裸挂路由。

控制台绑在 WireGuard 内网确实缩小了暴露面，但那是纵深防御，**不能当作唯一防线**。

## 目标

**所有控制台 API 默认需要鉴权，免鉴权的必须是显式白名单。**

## 交付物

```
internal/auth/           导出可复用的中间件
internal/app/            App 上提供统一的路由注册辅助（带鉴权 / 免鉴权两个入口）
internal/{inventory,api/metrics,api/events,settings}/module.go   改为走鉴权注册
```

## 约束

### 免鉴权白名单（只有这些，其余一律要登录）

| 路径 | 原因 |
|---|---|
| `POST /api/v1/login` | 登录本身 |
| `/api/agent/v1/**` | agent 用 `Authorization: Bearer <agent_token>` 走自己的校验 |
| `/healthz` | `setup.sh` 与监控探活 |
| `/install.sh`、`/dl/**` | 装机脚本与二进制下载 |
| `/`、`/assets/**` 及 SPA 路由 | 前端静态资源 |

### 实现要求

- ★ **默认拒绝，白名单放行** —— 不要反过来做成「给这几个加鉴权」，
  那样每加一个新端点都可能忘记，而忘记的后果是静默的安全洞
- 在 `App` 上提供两个注册入口，模块必须二选一，**不允许再直接摸 `a.Mux`**：
  ```go
  func (a *App) HandleAuthed(pattern string, h http.HandlerFunc)  // 默认用这个
  func (a *App) HandlePublic(pattern string, h http.HandlerFunc)  // 免鉴权，用时要写注释说明理由
  ```
- 未登录一律返回 **401** 和统一错误体（`12-api-spec.md` §1），**不要重定向**
- 会话过期同样 401，前端据此跳登录页
- 保留 `Authorization: Bearer <api_token>` 这条路（`api_tokens` 表已有），
  为将来的自动化留口子

### 顺带修

`POST /api/v1/login` 的**失败限流**（`12-api-spec.md` §3）：
同一用户名或 IP 连续失败 5 次锁定 15 分钟，返回 429。
现在没有，控制台暴露后可被暴力破解。

## 验收

★ **必须实跑，不能只看代码。** 起服务后逐条 curl：

```sh
# 1. 完全不带凭据 —— 全部必须 401
for ep in /api/v1/nodes /api/v1/settings /api/v1/events /api/v1/node-groups /api/v1/tags /api/v1/enroll-tokens; do
  code=$(curl -sk -o /dev/null -w "%{http_code}" "$BASE$ep")
  [ "$code" = "401" ] || echo "❌ $ep 返回 $code，应为 401"
done

# 2. 写接口同样必须 401（不是 400 / 404 / 200）
curl -sk -o /dev/null -w "%{http_code}\n" -X POST   "$BASE/api/v1/enroll-tokens" -d '{}'      # 必须 401
curl -sk -o /dev/null -w "%{http_code}\n" -X DELETE "$BASE/api/v1/nodes/anyid"                # 必须 401
curl -sk -o /dev/null -w "%{http_code}\n" -X POST   "$BASE/api/v1/node-groups" -d '{}'        # 必须 401

# 3. 伪造 cookie 必须 401
curl -sk -o /dev/null -w "%{http_code}\n" -H "Cookie: dash_session=bogus" "$BASE/api/v1/nodes"

# 4. 正常登录后必须 200
curl -sk -c /tmp/ck -X POST "$BASE/api/v1/login" -H 'Content-Type: application/json' \
     -d '{"username":"admin","password":"..."}'
curl -sk -b /tmp/ck -o /dev/null -w "%{http_code}\n" "$BASE/api/v1/nodes"                     # 必须 200

# 5. 免鉴权白名单仍然可用
for ep in /healthz /install.sh /dl/sha256sums.txt /; do
  curl -sk -o /dev/null -w "$ep %{http_code}\n" "$BASE$ep"    # 不能是 401
done

# 6. agent 链路不受影响
#    起一个 agent，确认仍能连上并上报
```

**外加一条防回归的单元测试**：遍历注册表，断言除白名单外的每个 `/api/v1/*` 路由都被鉴权包过。
这条很重要——它能挡住以后新增端点时忘记加鉴权。

## 边界

不做权限分级（多用户 / 只读角色），本期只做「登录与否」。

---

# 验收记录

## 第 1 轮 · 2026-09-07 · ✅ 通过

分支 `agy/p1-22-auth`，提交 `1f9ef0f`。

### ★ 全部实跑验证（不是看代码）

**① 不带任何凭据的读接口 —— 全部 401**

| 端点 | 修复前 | 修复后 |
|---|---|---|
| `/api/v1/nodes` | 200 🔴 | **401** ✅ |
| `/api/v1/settings` | 200 🔴 | **401** ✅ |
| `/api/v1/events` | 200 🔴 | **401** ✅ |
| `/api/v1/node-groups` | 200 🔴 | **401** ✅ |
| `/api/v1/tags` | 200 🔴 | **401** ✅ |
| `/api/v1/enroll-tokens` | 200 🔴 | **401** ✅ |
| `/api/v1/me` | 401 | 401 ✅ |

**② 写接口 —— 全部 401**（此前分别是 200 / 404 / 400，都不是鉴权拒绝）

```
POST   /api/v1/enroll-tokens  → 401 ✅   （此前 200，能匿名铸造装机令牌）
DELETE /api/v1/nodes/{id}     → 401 ✅   （此前 404，真 ID 会被删掉）
POST   /api/v1/node-groups    → 401 ✅   （此前 400）
```

**③ 伪造 cookie** `dash_session=bogus` → **401** ✅

**④ 免鉴权白名单仍可用**：`/healthz` `/install.sh` `/dl/sha256sums.txt` `/` `/assets/*.css` 全部 200 ✅

**⑤ 登录后恢复正常**：登录 200 → `/api/v1/nodes`、`/api/v1/me`、`/api/v1/settings` 全部 200 ✅

**⑥ 登录失败限流**：连续错密码 5 次均 401，**第 6 次 429** ✅

**⑦ agent 链路未被误伤**：生成 token → `POST /api/agent/v1/enroll` 200 →
agent `WSConnected` → `/healthz` 显示 `agents_online: 2` ✅

**⑧ 回归**：22 个包测试全过，两个 lint 通过 ✅

**任务 22 关闭。**
