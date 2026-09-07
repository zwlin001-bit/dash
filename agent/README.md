# agent

dash-agent 客户端核心实现。

## 职责边界

- 单个静态二进制产物，编译参数 `CGO_ENABLED=0`。
- 资源预算硬性指标：常驻 RSS ≤ 20 MB，单核 CPU ≤ 0.5%，磁盘写入 ≤ 1 次/分钟。
- 直接读取 Linux `/proc`，不依赖 gopsutil，不自做日志轮转（输出到 stderr）。
- 架构硬性约束：**仅允许 import `internal/protocol`**，严禁引入任何服务端内部包。

## 目录结构

- `collect/`：系统指标采集（`/proc` 解析、分级采集）
- `transport/`：WebSocket 通信与 HTTP 回退上报
- `runtime/`：Agent 主循环、定时驱动与优雅退出
