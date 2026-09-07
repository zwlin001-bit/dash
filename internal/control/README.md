# internal/control

Agent 接入、长连接管理、状态同步与指令回带中心。

## 职责边界

- **注册接入（Enrollment）**：提供 `/api/agent/v1/enroll` 无认证注册端点，支持基于一次性 15 分钟有效的 `enrollment token` 换取长期 Token，生成节点记录与静态 facts，并保证用过即作废与防重入。内置 IP 级别滑动窗口限流（10 次/分钟）。
- **WebSocket 长连接管理（WS RPC）**：提供 `/api/agent/v1/rpc` WebSocket 端点，基于 Bearer Token 鉴权（只比对 SHA-256 哈希值）。升级前严格校验，无效返回 HTTP 401 及 `-32000` 协议错误码。
- **在线连接注册表（Registry）**：维护内存中实时在线节点与连接会话。**同一节点重复连接自动踢掉旧连接**，防止重连风暴积累僵尸连接。
- **参数权威协商（Hello）**：处理连接后的第一条请求 `agent.hello`。校验协议版本（不兼容返回 `-32001`）；比对 `facts_hash` 决定是否要求上报 facts；下发服务端权威采集间隔与参数。
- **心跳与在线状态判定**：90 秒未收到任何消息（含 WebSocket Ping）判定离线，同步更新数据库中 `nodes.conn_state` 与 `last_seen_at_ms`。
- **时钟偏差纠正**：计算 `server_ts_ms - ts_ms`，偏差绝对值超过 60 秒时强制以服务端当前时间作为入库基准，并在节点上记录 `clock_skew_ms` 时钟异常标记。
- **Token 吊销与主动断连**：提供 `RevokeToken` 接口，置空 `nodes.agent_token_hash` 并写入审计日志；若节点当前在线，主动下发 `-32000` 错误并强制断开连接，促使 Agent 永久终止重连。
- **HTTP 回退通道（Fallback Report）**：提供 `/api/agent/v1/report` 批量上报与下行指令回带端点，在 WebSocket 不可用时保证指标上报并回带 pending 任务指令。

## 对外接口

- `NewModule() *ControlModule`：实现 `app.Module` 规范。
- `RegisterRoutes(mux *http.ServeMux, database *db.DB, registry *Registry, cfg *config.Config)`：注册端点。
- `NewRegistry(database *db.DB, logger *logx.Logger) *Registry`：连接注册表。
  - `Register(sess *Session) (kickedOld *Session)`：注册会话（自动踢旧连接）。
  - `Unregister(sess *Session)`：注销会话。
  - `Get(nodeID string) (*Session, bool)`：获取在线会话。
  - `IsOnline(nodeID string) bool`：查询节点是否在线。
  - `Kick(nodeID string) bool`：主动断开节点连接。
  - `RevokeToken(ctx context.Context, nodeID string, actorKind, actorID, clientIP string) error`：吊销长期 Token 并踢除连接。
  - `EnqueueFallbackCommand(nodeID string, cmd *protocol.Request)`：入队回退指令。
  - `PopFallbackCommands(nodeID string) []*protocol.Request`：提取回退指令。
- `CreateEnrollmentToken(ctx, db, presetName, presetGroupID, createdBy, ttl) (token, id, err)`：签发 enrollment token。

## 依赖谁

- `internal/protocol`：JSON-RPC 契约、错误码与数据结构定义。
- `internal/db`：可移植 SQL 数据库访问抽象。
- `internal/config`：服务端配置参数。
- `internal/logx`：脱敏日志。
- `github.com/gorilla/websocket`：WebSocket 协议实现。
