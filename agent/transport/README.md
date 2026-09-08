# agent/transport

Agent 网络通信与传输层。

## 职责边界

- **主通道（WebSocket）**：维护与服务端的单条长连接（`wss://<domain>/api/agent/v1/rpc`），双向承载 JSON-RPC 2.0 报文上报（metrics, facts）与指令接收（config, exec 等）。
- **回退通道（HTTP POST）**：端点为 `POST https://<domain>/agent/v1/report`。在 `transport: "auto"` 模式下，WebSocket 连续失败 ≥ 3 次时自动切换至 HTTP 回退，按 fast 间隔攒批上报，并通过响应体的 `commands` 数组接收下行指令，每 60s 自动尝试恢复 WebSocket 主通道；在 `transport: "http"` 模式下，直接进入 HTTP 回退通道，零次 WS 握手尝试、不定时重试 WS。请求头包含 `Authorization: Bearer <token>` 与 `X-Node: <nodeID>`。
- **重连与退避**：指数退避序列（1s → 2s → 4s → 8s → 16s → 32s → 60s 封顶）且每次乘 0.8~1.2 的随机抖动（±20% Jitter），连接成功后重置。
- **状态机与终态保护**：收到 `-32000`（token 无效/已吊销）或 HTTP 401 立即进入 `StateStopped` 终态，永久停止重连打扰服务端。
- **未知方法容错**：收到服务端未定义的 method 时回复 `-32601`（Method not found）并继续正常上报与运行，绝不崩溃。
- **心跳保活与读超时**：每 30s 发送一次 WS ping；读超时 60s，超时主动断开并触发重连。
- **选择性压缩**：启用 `permessage-deflate`，小于 1 KB 的报文不压缩以节省 CPU 与分配。
- **非阻塞与零补报**：`Send` 绝不阻塞调用方，断线或队列满时直接丢弃；断线期间绝不落盘缓存历史数据、绝不补报旧点。
- **边界说明**：不做系统指标采集、不做业务定时调度。

## 对外接口

```go
type Handler func(method string, params json.RawMessage) (result any, err error)

type Transport interface {
    // Start 启动连接与重连循环，非阻塞
    Start(ctx context.Context) error
    // Send 发送一条 notification。连接不可用时直接丢弃并返回错误，绝不阻塞调用方
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

## 依赖谁

- Go 1.22+ 标准库
- `github.com/gorilla/websocket`（v1.5.3，受控轻量 WebSocket 客户端库）
- `dash/internal/protocol`（协议契约结构体与错误码常量）
- **严禁依赖任何服务端 internal 包（如 internal/config, internal/logx, internal/db 等）。**
