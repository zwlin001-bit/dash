# agent/runtime

Agent 运行时主循环与生命周期管理。

## 职责边界

- 驱动三档采集与上报调度：
  - **fast 档**（默认 5s）：周期性采样 CPU、内存、swap、load、网络速率与累计字节、uptime，立即上报。
  - **slow 档**（默认 60s）：昂贵指标（连接数、磁盘用量、进程数）到期时直接搭在下一个 fast 报文里一同发送，不单独建包。
  - **facts 档**：静态硬件与系统信息仅在 `facts_hash` 变更或首次连接时上报，带最长 30 分钟兜底心跳。
- 复用 JSON 编码缓冲（`MetricsEncoder`），零堆内存分配，热路径绝不使用 `fmt.Sprintf`。
- 本地状态持久化（`StateStore`）：维护 `/var/lib/dash-agent/state.json`，写临时文件 → `fsync` → `rename` 原子替换，限频最多每分钟写入一次，文件丢失或目录只读不影响运行。
- 服务端策略动态应用：优先使用 `agent.hello` 握手应答及 `server.config` 下发的采集参数；参数变更立即生效，**不需要重连**。
- 日志一律写 stderr，不开日志文件、不做轮转，交给宿主机 init 系统。
- 处理退出信号（SIGINT, SIGTERM），优雅刷新脏状态并断开传输连接。
- 遵守非特权、非 root 运行约束。

## 对外接口

- `Config` / `DefaultConfig()` / `LoadConfig()`：多源配置加载（优先级：命令行参数 > 环境变量 `DASH_AGENT_*` > 配置文件 > 默认值）。
- `Runtime`：主运行器。
  - `New(cfg *Config, version string, opts ...Option) (*Runtime, error)`：构建运行时实例。
  - `Run(ctx context.Context) error`：启动主循环直至 context 取消。
  - `MetricsReportCount() int64` / `FactsReportCount() int64`：运行统计接口。
- `MetricsEncoder`：复用缓冲区指标序列化器。
- `StateStore`：状态文件原子读写与限频管理。

## 依赖谁

- `dash/agent/collect`（指标采集抽象与注册表）
- `dash/agent/collect/linux`（Linux 平台采集器实现）
- `dash/agent/transport`（WebSocket 主通道与 HTTP 回退传输层）
- `dash/internal/protocol`（协议契约结构体与常量）
- **严禁依赖任何服务端 internal 包。**
