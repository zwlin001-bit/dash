# agent/transport

Agent 网络通信与传输层。

## 职责边界

- 维护与 dashd 服务端的单条双向 WebSocket 长连接。
- 承载 JSON-RPC 2.0 报文上报（metrics, facts）与下行指令接收（ping, speedtest, upgrade）。
- 实现自动断线重连与指数退避机制。
- 提供 HTTP POST 回退上报机制（用于 WebSocket 不可达的网络环境）。

## 对外接口

- `Transport` 接口：
  - `Connect(ctx context.Context) error`
  - `Send(ctx context.Context, msg any) error`
  - `Receive() (<-chan []byte, <-chan error)`
  - `Close() error`

## 依赖谁

- 标准库及受控 WebSocket 客户端库。
- `internal/protocol`。
- **严禁依赖任何服务端 internal 包。**
