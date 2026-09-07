# internal/control

Agent 长连接管理与指令下发中心。

## 职责边界

- 管理 agent 的 WebSocket 长连接生命周期与会话状态。
- 维护节点的实时在线 / 离线状态。
- 下行指令调度与异步任务指令下发（如测速、升级、配置更新）。
- 心跳保活与连接超时断开检测。

## 对外接口

- `Hub`：长连接会话集线器。
- `SendCommand(nodeID string, cmd *protocol.Command) error`：下发指令。
- `GetOnlineStatus(nodeID string) bool`：查询节点是否在线。

## 依赖谁

- `internal/protocol`
- `internal/db`
- `internal/logx`
