# scripts

工程辅助工具、CI/CD 检查与验收基准测试脚本。

## 职责边界

- 包含代码纪律检查脚本（`lint-imports.sh`、`lint-sql.sh`）。
- 包含资源预算自动化验收测试脚本（`agent-bench.sh`、`mock-server.go`）。
- 包含数据库探测与迁移辅助脚本。
- 提供部署与打包工具。

## 对外接口

- `lint-imports.sh`：执行 agent 依赖隔离纪律检查（可通过 `make lint` 调用）。
- `agent-bench.sh [duration]`：一条命令执行 dash-agent 资源预算四项硬指标自动化评测（常驻 RSS、稳态 CPU、稳态磁盘写入、二进制体积），并输出达标报告。

## 依赖谁

- 标准 Bash 运行环境与 Go 工具链。
