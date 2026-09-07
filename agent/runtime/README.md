# agent/runtime

Agent 运行时主循环与生命周期管理。

## 职责边界

- 协调采集器（`collect`）与传输层（`transport`）。
- 驱动各级别指标的定时采集与周期性上报。
- 处理操作系统退出信号（SIGINT, SIGTERM），实现优雅退出。
- 维护极简的本地状态文件（原子写入，频率 ≤ 1 次/分钟），不落盘时序历史数据。
- 遵守非 root 运行约束。

## 对外接口

- `Runtime`：主运行器。
- `Run(ctx context.Context) error`：启动 Agent 运行时并阻塞直至收到退出信号。

## 依赖谁

- `agent/collect`
- `agent/transport`
- `internal/protocol`
- **严禁依赖任何服务端 internal 包。**
