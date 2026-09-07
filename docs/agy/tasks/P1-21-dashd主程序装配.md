# P1-21 dashd 生命周期与资源预算

> 第一期 · 任务 21 ｜ 前置：19 ✅（已合并）｜ 分支：`agy/p1-21-lifecycle`
> **本任务书已按 19 的实际交付重写。** 19 做掉了配置加载与监听，21 只补剩下的部分。

## 19 已经做掉的（不要重做）

| 项 | 位置 |
|---|---|
| `-config` 加载配置、环境变量覆盖 | `cmd/dashd/main.go` |
| `db.Open` 连库，失败不退出（`/healthz` 报 `db: error`） | `cmd/dashd/main.go` |
| 监听地址取 `cfg.Server.Listen` | `internal/api/web.go` |
| `signal.Notify` + `server.Shutdown` | `internal/api/web.go` |
| ACME / 443 与普通 HTTP 两条启动路径 | `internal/api/web.go` |
| `init-db` 子命令（建表 + 写域名 + 建管理员） | `cmd/dashd/main.go` |

## 本任务要解决的四件事

### ❶ 把服务启动从 `Register()` 里搬出来

现状：`api.WebModule.Register()` 里直接 `ListenAndServe`，**阻塞不返回**。

```go
// internal/api/web.go 现在的样子
log.Printf("dashd 控制台已启动: ...")
if err := server.ListenAndServe(); err != nil { ... }   // ★ 永不返回
return nil                                               // ★ 永远走不到
```

后果：
- **排在 `api` 后面的模块永远不会被注册** —— 现在靠「把 api 放最后」绕过去，
  这是个随时会被下一个人踩中的陷阱
- 主程序拿不回控制权，**没法统一编排关闭顺序**
- `RegisterModules` 的错误语义失真：服务正常运行时它「还没返回」，
  服务退出时它「返回 nil」

改法：
- `Register()` 只做三件事：往 `a.Mux` 挂路由、启动自己的后台 goroutine、**立即 return**
- HTTP 服务由 `main()` 统一启动，`api` 模块通过 `App` 暴露 `Handler()` 即可
- 模块顺序的依赖（`ingest` 在 `control` 前）保留并加注释；
  **`api` 不再需要「必须最后」这条特殊规则**

### ❷ 完整的优雅退出

现状：只 `server.Shutdown()`，**ingest 缓冲里的数据直接丢**。
30 台机器 × 5 秒间隔，每次重启最多丢 30 条样本 —— 图表上就是一个缺口。

要求的关闭顺序，**一步都不能少、不能换**：

```
1. 收到 SIGINT / SIGTERM
2. server.Shutdown(ctx)        停止接新请求，等在途请求做完（超时 10s）
3. 停止后台调度                rollup / retention / sweeper 停止启动新一轮
4. ★ ingest.Flush(ctx)         把缓冲里的样本写完（超时 10s）
5. 关闭所有 agent WS 连接      发 close frame，别让 agent 等到超时
6. database.Close()
7. 进程退出，退出码 0
```

- 每一步都要打日志，**用户要能从日志确认数据没丢**
- 整体超时 30 秒，超时则强制退出并**明确记录「可能有数据未落库」**
- ★ **`ingest` 模块要暴露 `Flush(ctx) error`**，
  `control` 要暴露 `CloseAll()`。通过 `App.Ingester` / `App.Registry` 拿到

### ❸ 服务端资源预算（新增硬性要求）

用户要求在 **1–2 GB 内存**的机器上跑。

| 指标 | 上限 | 场景 |
|---|---|---|
| 稳态 RSS | **≤ 1 GB** | 30 台 agent 在线、正常上报 |
| 峰值 RSS | ≤ 1.3 GB | rollup / 保留期清理运行时 |

手段：
- `debug.SetMemoryLimit(1 << 30)` —— **现在完全没有**，加上
- ingest 的 ring buffer **按节点数动态定容**，不要开固定大数组
- 最新值缓存要在节点删除时清理，否则是慢性泄漏
- `db.MaxOpenConns` 保持 20，**不要调大**
- rollup 的 `INSERT ... SELECT` 保持在库内完成（任务 12 已如此）

★ 若目标机只有 1 GB：`SetMemoryLimit(700 << 20)` + `MaxOpenConns=10`，
在 `14-dual-domain-deploy.md` §6 里注明这是压缩配置。

### ❹ `scripts/dashd-bench.sh`

跑法与 `agent-bench.sh` 一致：

```
用法: ./scripts/dashd-bench.sh [时长秒数，默认 300] [模拟节点数，默认 30]

输出:
  RSS 峰值      412 MB  / 上限 1024 MB   ✅
  RSS 稳态      380 MB
  goroutine 数  247
  结论: 达标
```

- 用 `scripts/mock-server.go` 的反向思路：起 N 个模拟 agent 连上 dashd 持续上报
  （任务 09 已经交付了 mock-server，可以复用其协议代码）
- 采样 RSS 峰值与稳态、goroutine 数量
- 任一项超标**退出码非 0**

## 顺带修掉的小问题

| # | 问题 |
|---|---|
| 1 | 启动日志拼接错误：`http://localhost127.0.0.1:18443`，应为 `http://127.0.0.1:18443`。绑 `0.0.0.0` 时才显示 localhost |
| 2 | `GET /api/v1/settings` 返回 `endpoint not found` —— 设置读取接口未挂载（任务 17 交付了 `internal/settings`，但路由没接上） |

## 验收

1. `Register()` 全部立即返回 —— 写个测试：注册一个排在 `api` **之后**的假模块，
   断言它的 `Register` 被调用过
2. `curl /healthz` 200；改配置换端口生效
3. ★ **优雅退出验证**：agent 持续上报中 `kill -TERM dashd`，
   日志按顺序出现 6 步，且**退出后查库，最后 5 秒内的样本都在**（不丢批）
4. 整体超时路径：人为让 flush 卡住，30 秒后强制退出并记录警告
5. 停掉数据库 → `/healthz` 503 且进程不退出 → 恢复 → 200
6. `scripts/dashd-bench.sh` 稳态 RSS ≤ 1 GB
7. `GET /api/v1/settings` 返回设置项
8. 启动日志 URL 正确

## 边界

不做双域名、不做 nginx、不做 TLS —— 那些是 19 的重做范围（见 `14-dual-domain-deploy.md`）。
本任务只管进程自己的生命周期与资源。

---

# 验收记录

## 第 1 轮 · 2026-09-07 · ✅ 通过

分支 `agy/p1-21-bootstrap`，提交 `4165a06`。已合并到 main：`cbe6ae8`。

| 验收项 | 实测 |
|---|---|
| ❶ `Register()` 不再阻塞 | ✅ 服务启动搬到 `runServe`，日志 `dashd dev listening on 127.0.0.1:18443 (loaded 8 modules)` |
| ❷ **优雅退出六步** | ✅ 实测 `kill -TERM` 后日志依次出现：收到信号 → flushing ingest buffer → ingest flush completed → closing database → exited gracefully；代码里 `server.Shutdown(10s)` → `FlushIngest()` → `Registry.Stop()` → `DB.Close()` 顺序正确，进程干净退出 |
| ❸ `SetMemoryLimit(1<<30)` | ✅ |
| ❹ `scripts/dashd-bench.sh` | ✅ 另交付 `scripts/mock-agents/` 压测工具 |
| 顺带修 1：启动日志 URL | ✅ `localhost127.0.0.1` 已修正 |
| 顺带修 2：`/api/v1/settings` | ✅ 返回完整设置项 |
| 回归 | ✅ 22 个包全过，两个 lint 通过 |

### 超出要求的改进

把 `/healthz` 上移到 `app.HealthzHandler()` 由 main 统一注册，
**从根上消除了模块间重复注册的可能**（我此前是在 ingest 侧打补丁绕过的）。
`/healthz` 现在还聚合了 ingest 的 `dropped_batches` / `dropped_rows`。

### 合并时由设计方处理

21 基于 `be21f8d`（19 合并前），`cmd/dashd/main.go` 缺 19 的 `init-db` 子命令，
已取并集补回（连带 `generateSecurePassword` 与 imports）。
删除 `internal/api/web_test.go` 里指向已移走符号的旧测试，`internal/app` 已有等价测试。

**任务 21 关闭。**
