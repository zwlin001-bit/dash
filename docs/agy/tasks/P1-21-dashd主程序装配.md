# P1-21 dashd 主程序装配

> 第一期 · 任务 21 ｜ 前置：10 ✅ 11 ✅ 13 ✅ 14 ✅ 20 ✅ ｜ 分支：`agy/p1-21-bootstrap`
> **这是任务拆分的遗漏，不是任何一个 AGY 的问题。** 说明见下。

## 为什么会有这个任务

各模块都实现好了、单测也全过，但**没有任何一个任务负责把它们装配成一个能跑的服务端**。
实测当前 main 上的 `dashd`：

```
$ ./dashd
2026/09/07 17:56:53 dashd 控制台已启动: http://localhost:8080 (监听地址 :8080)
2026/09/07 17:56:53 failed to register modules: module web: web server failed: ...
$ 进程已退出
```

三个问题：

1. **配置从未加载** —— `config.Load()` 只出现在 `migrate` 分支，serve 模式里没有
2. **数据库从未连接** —— `app := &App{}` 是空的，`App.DB` / `App.Config` 全是 nil，
   `control` / `ingest` / `inventory` / `metrics` / `events` 拿到的都是空壳
3. **监听地址硬编码 `:8080`**，忽略 `config.Server.Listen`；
   而且 `ListenAndServe` 阻塞在 `api.Module.Register()` 里面——**违反模块契约**，
   Register 应当只注册、立即返回

根因是我在拆任务时把「主程序装配」漏了：P1-01 明确写了「不写任何业务逻辑」，
后续任务各管各的模块，没人接这一棒。

## 目标

让 `dashd` 真正跑起来：加载配置 → 连库 → 装配 App → 启动 HTTP 服务 → 优雅退出。

## 交付物

```
cmd/dashd/main.go        serve 模式完整实现
internal/app/app.go      App 的构造与生命周期（如需要）
```

## 约束

- **`dashd [-config <path>] serve`**（或无子命令即 serve），`-config` 默认 `/etc/dash/config.toml`。
  **必须支持 `-config` 覆盖**，否则开发和测试都没法跑
- 启动顺序：
  ```
  加载配置 → logx.Init → 连数据库（失败即退出并给可读错误）
  → 构造 App{Mux, DB, Config} → RegisterModules → 启动 HTTP 服务 → 阻塞等待信号
  ```
- ★ **`Register()` 不允许阻塞。** 模块只往 `App.Mux` 挂路由、启动自己的后台 goroutine 然后返回。
  HTTP 服务由主程序统一启动，**不在模块里 `ListenAndServe`**
- 监听地址取 `config.Server.Listen`，**不许硬编码**
- 模块注册顺序有依赖，保持现状并加注释：
  `auth → inventory → ingest → control → metrics → events → settings → api`
  （`ingest` 必须在 `control` 前；`api` 必须最后，它挂 `/` 兜底 SPA）
- 优雅退出：收到 `SIGINT`/`SIGTERM` 后，先停接新请求，
  **再 flush ingest 的缓冲**（否则最后一批指标会丢），最后关数据库
- `/healthz` 按 `12-api-spec.md` §9 返回：数据库不可用时 503 但**进程不退出**
- ★ **服务端内存预算：稳态 RSS ≤ 1 GB**（详见下方）

## ★ 服务端资源预算（新增硬性要求）

用户要求服务端在 **1–2 GB 内存**的机器上能跑。稳态目标：

| 指标 | 上限 | 说明 |
|---|---|---|
| 稳态 RSS | **≤ 1 GB** | 30 台 agent 在线、正常上报的情况下 |
| 峰值 RSS | ≤ 1.5 GB | rollup / 保留期清理运行时 |

达成手段：
- `debug.SetMemoryLimit(1 << 30)`，让 GC 在接近上限时更积极
- ingest 的 ring buffer 容量按节点数算，**不要开固定的大数组**
- 最新值缓存按节点数量级，注意节点删除后要清理
- 数据库连接池 `MaxOpenConns` 默认 20 已经够，不要调大
- rollup 的 `INSERT ... SELECT` 在数据库内完成，**不要把结果集拉到应用层**（任务 12 已这样做）

交付 `scripts/dashd-bench.sh`，跑法与 `agent-bench.sh` 一致，打印 RSS 峰值与是否达标。

## 验收

1. `./dashd -config <测试配置>` 能启动，`curl /healthz` 返回 200 且 `db: ok`
2. 监听地址取自配置文件，改配置改端口生效
3. `Ctrl-C` 优雅退出，日志里能看到 ingest flush
4. 停掉数据库后 `/healthz` 返回 503，**进程不退出**，恢复后自动变回 200
5. **端到端**：启动 dashd → 界面生成 enrollment token → 真机装 agent →
   节点上线 → `sample_host` 里查到数据
6. `scripts/dashd-bench.sh` 显示稳态 RSS ≤ 1 GB

第 5 条一旦通过，任务 10 / 11 / 12 / 16 / 17 里所有「待联调补验」的项目一并关闭。

## 边界

不做 TLS、不做 ACME、不做双域名——那些是任务 19。本任务只让服务跑起来。
