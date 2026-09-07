# M3 注册、鉴权与指令下发

设计文档：[`../03-agent.md`](../03-agent.md) §6-7、[`../04-protocol.md`](../04-protocol.md)

---

## T3.1 注册与 token

**交付物**：enrollment token 生成/核销、`POST /api/agent/v1/enroll`、长期 token 签发与吊销。

**约束**
- enrollment token 一次性、15 分钟有效，可预绑定节点名与分组
- 长期 token **只存哈希**（`nodes.agent_token_hash`）
- 吊销 = 置空该列，agent 下次连接收到 `-32000` 后停止重连
- 全部写审计日志

**验收**：生成 token → agent 注册成功 → 同一 token 再用一次被拒 → 吊销后 agent 停止重连。

---

## T3.2 动作清单执行（exec_mode=actions）

**本期只做 `actions` 模式。`shell` 模式不实现**（`00-scope.md`）。

**交付物**
```
agent/action/
  action.go     type Action interface { Name() string; Run(ctx, args []string) (Result, error) }
  registry.go   注册表
  builtin/      restart_service.go  reload_config.go  write_file.go  fetch_file.go ...
```

**约束（安全红线）**
- **参数以 `argv` 数组传递，绝不拼接 shell 字符串**，不经过 `sh -c`
- agent 侧按名查注册表；**服务端下发的动作名不在注册表里就返回 `-32601`，不做任何回退**
- 每次执行：超时默认 300s、输出截断 256 KB、并发上限 2
- `exec_mode` 由服务端能力位 + agent 本地配置**双向确认**才生效，任一为 off 即拒绝
- 所有执行写审计日志：谁触发、哪个节点、什么动作、什么参数、退出码

**验收**
- 下发一个注册的动作，结果正确回传并入库
- 下发未注册的动作名，agent 返回 `-32601` 且**不执行任何东西**
- 参数里带 `; rm -rf /` 之类内容，确认它被当作普通字符串参数传给目标程序，没有被 shell 解释
- `exec_mode=off` 时下发动作被拒绝

---

## T3.3 Job 引擎

**交付物**：`internal/jobs/`——持久化任务队列、step 执行、租约、SSE 进度推送。

**约束**
- 所有慢操作走 job，**HTTP 请求里禁止同步执行**
- `lease_owner` / `lease_until_ms` 抢占租约；进程崩溃后过期租约的 job 可被重新拾起
- step 幂等，失败可从断点重试，`max_attempts` 可配

**验收**：跑一个多 step 的 job，中途 kill dashd，重启后 job 从断点继续而不是从头再来。

---

## T3.4 Agent 升级下发

**交付物**：`server.upgrade` 指令 + agent 侧的下载校验替换。

**约束**
- **不自更新、不访问 GitHub**。产物由 dashd 自己托管在 `/dl/`
- agent 下载 → 校验 sha256 → 原子替换 → 请求 init 系统重启自己
- 校验失败或替换失败时**保留旧版本继续运行**，回报错误

**验收**：下发一个新版本，agent 升级后自动回连并上报新版本号；下发一个 sha256 错误的包，
agent 拒绝升级且继续正常运行。
