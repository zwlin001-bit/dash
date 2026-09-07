# cmd/dash-agent

Agent 客户端主入口。

## 职责边界

- 运行于受控主机上的监控客户端主二进制入口。
- 负责命令行参数解析（支持 `-v`, `--version`）。
- 协调 Agent 运行时（`agent/runtime`）启动。
- 单个静态二进制，`CGO_ENABLED=0` 编译，不依赖宿主机 glibc 或外部运行时。

## 对外接口

- CLI 选项支持 `-v` 与 `--version` 输出版本与构建信息。

## 依赖谁

- 标准库。
- `agent/runtime`（主循环）。
- `internal/protocol`（协议定义）。
- **严禁依赖服务端 internal 包（`internal/db`, `internal/api` 等）。**
