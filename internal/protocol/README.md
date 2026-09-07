# internal/protocol

agent 与 dashd 之间的统一通信协议契约。

## 职责边界

- 定义 JSON-RPC 2.0 报文信封（Request / Response / RPCError）。
- 定义 agent 上报结构体（`agent.hello`、`agent.metrics`、`agent.facts` 等）。
- 定义服务端下行配置结构体（`server.config`）。
- 定义二期预留方法名常量（`agent.result`、`agent.ping_result`、`server.exec` 等），不定义未使用的结构体。
- 定义 JSON-RPC 标准错误码与 dash 自定义错误码（-32000 ~ -32004）。
- 仅定义协议契约数据结构与常量，**不包含网络收发、WebSocket 或任何业务逻辑**。
- 指标可选字段用指针 + `omitempty`，区分「采集失败省略」与「值为 0」。

## 文件结构

- `version.go`: 协议版本号 `ProtocolVersion = 1`。
- `rpc.go`: JSON-RPC 2.0 信封（`Request`, `Response`, `RPCError`）。
- `agent2srv.go`: Agent 到 Server 的上行消息契约（`HelloParams`, `HelloResult`, `MetricsParams`, `FactsParams` 等）。
- `srv2agent.go`: Server 到 Agent 的下行消息契约（`ServerConfigParams` 等）。
- `errors.go`: 错误码常量定义。

## 对外接口

- `ProtocolVersion`：协议版本常量（当前为 1）。
- JSON-RPC 2.0 信封结构体：`Request`、`Response`、`RPCError`。
- 上行结构体：`HelloParams`、`HelloResult`、`MetricsParams`、`FactsParams` 及子结构体。
- 下行结构体：`ServerConfigParams`。
- 方法名常量：`MethodAgentHello`、`MethodAgentMetrics`、`MethodAgentFacts`、`MethodServerConfig` 等。
- 协议错误码常量：`ErrCodeTokenInvalid`、`ErrCodeVersionMismatch` 等。

## 依赖谁

- **零依赖**。绝对禁止 import 任何其他 internal 包，仅依赖 Go 标准库（`encoding/json`, `fmt`）。

## 变更纪律

本任务合并后**本包锁定**。后续任务要改协议：
1. 停下上报；
2. 由设计方改 `04-protocol.md` 并升 `ProtocolVersion`；
3. 指定一人改代码。
