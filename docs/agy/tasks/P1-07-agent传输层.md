# P1-07 Agent 传输层

> 第一期 · 任务 07/19 ｜ 前置：05 ｜ 分支：`agy/p1-07-transport`
> 设计依据：[`../../04-protocol.md`](../../04-protocol.md) §1、§5

> ★ **完成后必须自己 `git push -u origin <分支名>`**，并确认
> `git log --oneline -1 origin/<分支名>` 能看到你的提交。只提交到本地等于没交付。

## 目标

一条 WebSocket 长连接承载上报与下行指令，外加 HTTP 上报回退。

## 交付物

```
agent/transport/
  ws.go          WebSocket 主通道
  fallback.go    HTTP POST 回退
  backoff.go     退避 + 抖动
  README.md
```

- 主通道：`wss://<domain>/api/agent/v1/rpc`
- 回退：`POST https://<domain>/api/agent/v1/report`

## 接口与状态机

```go
package transport

type Handler func(method string, params json.RawMessage) (result any, err error)

type Transport interface {
    // Start 启动连接与重连循环，非阻塞
    Start(ctx context.Context) error
    // Send 发送一条 notification。连接不可用时直接丢弃并返回错误，★ 绝不阻塞调用方
    Send(method string, params any) error
    // Call 发送 request 并等待应答（仅 agent.hello 用）
    Call(ctx context.Context, method string, params any) (json.RawMessage, error)
    // OnServerMessage 注册下行消息处理器
    OnServerMessage(h Handler)
    // State 返回当前连接状态，供主循环判断
    State() ConnState
    Close() error
}

type ConnState int
const (
    StateDisconnected ConnState = iota
    StateConnecting
    StateWSConnected
    StateFallbackHTTP
    StateStopped          // 收到 -32000，不再重连
)
```

### 重连状态机

```
Disconnected ──拨号──▶ Connecting ──成功──▶ WSConnected
      ▲                    │                    │
      │                    │ 失败                │ 断开
      │              ┌─────┴─────┐              │
      └──退避等待────┤ 失败计数+1 │◀─────────────┘
                     └─────┬─────┘
                           │ 连续失败 ≥3
                           ▼
                    FallbackHTTP ──每 60s 尝试恢复──▶ Connecting

任意状态收到 -32000 ──▶ Stopped（终态，不再重连）
```

- 退避序列：1s → 2s → 4s → 8s → 16s → 32s → 60s（封顶），**每次乘 0.8~1.2 的随机因子**
- 连接成功后**重置失败计数和退避**
- `FallbackHTTP` 状态下按 fast 间隔攒批 POST，响应体里的 `commands` 逐条交给 `Handler`

### HTTP 回退的请求/响应格式

★ 见 [`../../12-api-spec.md`](../../12-api-spec.md) §2 的 `POST /api/agent/v1/report`。
**下行指令通过响应体的 `commands` 数组回带**——这是回退模式下唯一的下行通道。

## 约束

- 认证用 `Authorization: Bearer <token>`，**不放 query string**——会进反代访问日志
- 消息结构体全部来自 `internal/protocol`，**不在这里另定义**
- **重连退避必须带 ±20% 抖动**：1s → 2s → 4s → … → 60s 封顶。
  没有抖动的话，服务端重启会导致所有 agent 同时重连把它再打挂一次
- WS 连续失败 3 次转 HTTP 回退，之后每 60s 尝试恢复 WS
- **断线期间不缓存上报、不补报旧点。** 监控数据的价值随时间衰减，
  补报要付出的内存和复杂度不值这个价
- **小于 1 KB 的报文不压缩**（压缩收益为负，纯浪费 CPU 和分配）
- 心跳：30s 一次 WS ping；读超时 60s，超时主动重连
- **收到不认识的 method 返回 `-32601` 并继续运行，不许崩溃。**
  这是将来滚动升级能工作的前提
- 收到 `-32000`（token 无效/已吊销）**停止重连**，不要无限重试打服务端

## 验收

1. 断网 2 分钟再恢复，自动重连成功，且 **agent 内存无增长**
2. 服务端返回 `-32000` 后停止重连（看日志确认不再发起连接）
3. 服务端下发一个未定义的 method，agent 返回 `-32601` 且继续正常上报
4. 打断 WS 三次后自动转 HTTP 回退，恢复后能切回 WS
5. 退避间隔的抖动可以在日志里观察到（不是固定的 1/2/4/8）

## 边界

**不做采集、不做定时调度。** 对上暴露「发一条消息」「收到消息回调」两个接口即可。

---

# 验收记录

## 第 1 轮 · 2026-09-07 · ✅ 通过

分支 `agy/p1-07-transport`，提交 `22c153d`。

| 验收项 | 实测 |
|---|---|
| 依赖纪律 | ✅ agent 侧只新增 `gorilla/websocket v1.5.3`——这是白名单里唯一允许的那一个 |
| `go test ./agent/...` | ✅ 通过 |
| 退避 ±20% 抖动 | ✅ `factor := 0.8 + rng.Float64()*0.4`，且有测试断言 20 次取值不重复 |
| token 放 Header | ✅ `Authorization: Bearer`，未进 query string |
| `-32000` 停止重连 | ✅ |
| 未知方法回 `-32601` 并继续 | ✅ |
| 小报文不压缩 | ✅ 1 KB 阈值 |
| 连续失败转 HTTP 回退 | ✅ 有失败计数与 `StateFallbackHTTP` 状态 |
| 协议结构体复用 | ✅ 来自 `internal/protocol`，未另定义 |

### 待联调补验（依赖任务 10，不算缺陷）

- 真实断网 2 分钟再恢复、内存无增长
- 打断 WS 三次后转 HTTP 回退、恢复后切回 WS
- 服务端下发未知 method 后 agent 仍正常上报

这三条依赖服务端存在，属于「待联调补验」，不影响本轮判定。

**任务 07 关闭。等 02/03 合并后可随时合并。**

**已合并到 main：`541011e`。**
