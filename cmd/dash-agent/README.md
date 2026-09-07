# cmd/dash-agent

Agent 客户端主二进制入口。

## 职责边界

- 运行于受控主机上的监控客户端静态二进制程序。
- 启动强制启用硬性资源保护约束：
  ```go
  runtime.GOMAXPROCS(1)
  debug.SetGCPercent(50)
  debug.SetMemoryLimit(24 << 20)
  ```
- 负责命令行参数解析与多级配置合成（命令行参数 > 环境变量 `DASH_AGENT_*` > 配置文件 > 默认值）。
- 监听操作系统信号（`SIGINT`, `SIGTERM`），协调 Agent 运行时（`agent/runtime`）优雅停机。
- 单个静态二进制，`CGO_ENABLED=0` 纯静态编译，不依赖宿主机 glibc 或外部运行时，在 Alpine(musl) / Debian / Ubuntu 上直接运行。

## 命令行选项

- `-v`, `--version`：输出版本、git commit 与构建时间。
- `-c`, `--config`：配置文件路径（默认 `/etc/dash-agent/config.json`）。
- `--endpoint`：服务端端点地址（如 `https://example.com` 或 `http://127.0.0.1:8080`）。
- `--token`：长期认证 Token。
- `--state-file`：状态持久化文件路径（默认 `/var/lib/dash-agent/state.json`）。
- `--interval-fast`, `--interval-fast-s`：fast 档采集周期（秒，默认 5）。
- `--interval-slow`, `--interval-slow-s`：slow 档采集周期（秒，默认 60）。
- `--facts-max-interval`, `--facts-max-interval-s`：facts 兜底上报周期（秒，默认 1800）。
- `--collect-conns`：是否采集 TCP/UDP 连接数（默认 true）。
- `--include-mounts` / `--exclude-mounts`：磁盘挂载点过滤列表（逗号分隔）。
- `--include-nics` / `--exclude-nics`：网卡过滤列表（逗号分隔）。
- `--mem-include-cache`：是否将内存计算口径切换为 `MemTotal - MemFree`（默认 false）。
- `--exec-mode`：远程执行模式（`off` / `actions` / `shell`，默认 `off`）。
- `--enable-terminal`：是否开启交互式终端能力（默认 false）。
- `--insecure-skip-verify`：是否跳过 TLS 证书校验（仅调试用，默认 false）。
- `--prefer-ip-version`：优先 IP 版本（`4` 或 `6`）。

## 依赖谁

- Go 1.22+ 标准库。
- `dash/agent/runtime`（运行时主循环与状态管理）。
- `dash/agent/collect/linux`（自动注册 Linux 指标采集器）。
- **严禁依赖服务端 internal 包（`internal/db`, `internal/api` 等）。**
