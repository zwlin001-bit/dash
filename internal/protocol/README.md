# internal/protocol

agent 与 dashd 之间的统一通信协议契约。

## 职责边界

- 定义 JSON-RPC 2.0 报文信封（Request / Response / RPCError）。
- 定义 agent 上报结构体（`agent.hello`、`agent.metrics`、`agent.facts` 等）。
- 定义服务端下行指令与配置结构体（`server.config` 等）。
- 定义标准错误码常量（-32000 ~ -32004 等）。
- 仅定义协议契约数据结构与常量，**不包含网络收发、WebSocket 或任何业务逻辑**。
- 可选字段用指针或 `omitempty`，区分「采集失败省略」与「值为 0」。

## 对外接口

- `ProtocolVersion`：协议版本常量（初始为 1）。
- JSON-RPC 2.0 信封结构体。
- 各上行与下行消息类型结构体。
- 协议标准错误码与常量定义。

## 依赖谁

- **零依赖**。绝对禁止 import 任何其他 internal 包。
