# 04 Agent ↔ Server 协议

**版本：v1**

> 改动本协议必须升 `protocol_version` 并同步更新本文件（P1.3）。
> 服务端必须同时接受 N 和 N-1 两个版本，给 agent 留出滚动升级窗口。

---

## 1. 通道

| 通道 | 端点 | 用途 |
|---|---|---|
| 主通道 | `wss://<domain>/api/agent/v1/rpc` | 上报 + 指令下发，一条长连接 |
| 回退通道 | `POST https://<domain>/api/agent/v1/report` | WS 不可用时只上报，指令通过响应体回带 |
| 终端 | `wss://<domain>/api/agent/v1/terminal` | 交互式终端，独立连接，默认关闭 |
| 产物下载 | `GET https://<domain>/dl/dash-agent-linux-<arch>` | 安装与升级 |

认证：`Authorization: Bearer <token>`（不放 query string——会进反代访问日志）。

消息格式：**JSON-RPC 2.0**。上报用 notification（无 `id`），需要应答的用 request（带 `id`）。
WebSocket 使用 text frame。启用 `permessage-deflate`，但小于 1 KB 的报文不压缩。

---

## 2. Agent → Server

### 2.1 `agent.hello`（连接后第一条，request）

```json
{"jsonrpc":"2.0","id":1,"method":"agent.hello","params":{
  "protocol_version": 1,
  "agent_version": "0.1.0",
  "boot_at_ms": 1757222400000,
  "capabilities": ["metrics","facts","exec_actions","terminal"],
  "facts_hash": "3f2a…"
}}
```

服务端应答，同时把服务端侧的策略下发回来：

```json
{"jsonrpc":"2.0","id":1,"result":{
  "node_id": "01JBX…",
  "server_time_ms": 1757222400123,
  "interval_fast_s": 5,
  "interval_slow_s": 60,
  "collect_conns": true,
  "exec_mode": "actions",
  "enable_terminal": false,
  "need_facts": true,
  "action_catalog_rev": 7
}}
```

- `need_facts`：服务端比对 `facts_hash`，不一致才要求上报 facts
- **采集参数由服务端下发**，本地配置只作为服务端不可达时的兜底。这样调整采集频率不用逐台改配置

### 2.2 `agent.metrics`（notification，fast 档每轮一条）

```json
{"jsonrpc":"2.0","method":"agent.metrics","params":{
  "ts_ms": 1757222405000,
  "cpu_pct": 3.5,
  "mem_used": 412000000,
  "swap_used": 0,
  "load": [0.12, 0.09, 0.05],
  "net": {"up_bps": 12000, "down_bps": 84000, "total_up": 90123456789, "total_down": 0},
  "uptime_s": 864000,
  "slow": {
    "disk_used": 12000000000,
    "proc_count": 92,
    "tcp_count": 41,
    "udp_count": 6,
    "disks": [{"k":"/","used":12000000000,"total":42000000000}],
    "nics": [{"k":"eth0","total_up":90123456789,"total_down":512345678901}]
  }
}}
```

- `slow` 字段只在 slow 档到期的那一轮出现，其余轮次省略
- `ts_ms` 由 agent 生成。服务端记录 `server_ts_ms - ts_ms` 作为时钟偏差；
  偏差超过 60 秒时**以服务端时间为准**入库，并在节点上标记时钟异常
- 缺失/采集失败的指标**省略字段**（不填 0），服务端写 NULL

### 2.3 `agent.facts`（notification）

只在启动、`facts_hash` 变化、或服务端 `need_facts` 时上报。字段对应 `node_facts` 表。

### 2.4 `agent.result`（notification）

指令执行结果回传：

```json
{"jsonrpc":"2.0","method":"agent.result","params":{
  "request_id": "01JBY…",
  "ok": true,
  "exit_code": 0,
  "stdout": "…", "stderr": "…", "truncated": false,
  "started_at_ms": 0, "ended_at_ms": 0
}}
```

### 2.5 `agent.ping_result`（notification）

网络探测任务结果，写入 `sample_dim`。

---

## 3. Server → Agent

| 方法 | 类型 | 说明 |
|---|---|---|
| `server.config` | notification | 下发采集参数变更，agent 立即生效，不需重连 |
| `server.exec` | request | 执行动作。`{request_id, mode:"action"\|"shell", action, args:[...], timeout_s}` |
| `server.upgrade` | request | `{version, url, sha256}` |
| `server.ping_task` | notification | 下发探测任务清单（全量替换） |
| `server.terminal_open` | request | 要求 agent 发起终端连接，携带一次性 ticket |
| `server.reload_actions` | notification | 动作清单版本变更 |

**agent 对不认识的方法必须返回 `-32601 Method not found` 并继续运行**，
不允许因为服务端下发了新方法就崩溃——这是滚动升级能工作的前提。

---

## 4. 错误码

| 码 | 含义 |
|---|---|
| `-32700 / -32600 / -32601 / -32602` | JSON-RPC 标准错误 |
| `-32000` | token 无效或已吊销（agent 收到后停止重连，等待人工处理） |
| `-32001` | 协议版本不兼容 |
| `-32002` | 能力未开启（如 exec_mode=off 时收到 exec） |
| `-32003` | 执行超时 |
| `-32004` | 限流 |

---

## 5. 心跳与在线判定

- agent 每 30s 发 WS ping
- 服务端 90s 内没收到任何消息（含 ping）→ 标记 `conn_state=offline`，关闭连接
- agent 侧读超时 60s，超时即主动重连
- 重连退避：1s → 2s → 4s → … → 60s 封顶，每次乘 ±20% 抖动

---

## 6. 注册流程

```
1. 用户在控制台生成 enrollment token（一次性、15 分钟有效、可绑定预期名称/分组）
2. agent 启动时若无长期 token，POST /api/agent/v1/enroll {enroll_token, facts}
3. 服务端校验 → 创建 nodes 行 → 返回 {node_id, agent_token}
4. agent 写入配置文件（0600），后续用长期 token 连接
5. enrollment token 立即作废
```

长期 token 只存哈希（`agent_token_hash`）。吊销 = 置空该列，agent 下次连接收到 `-32000`。
