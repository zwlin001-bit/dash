# scripts

工程辅助工具与 CI/CD 检查脚本。

## 职责边界

- 包含代码纪律检查脚本（`lint-imports.sh`、`lint-sql.sh`）。
- 包含数据库探测与迁移辅助脚本。
- 提供部署与打包工具。

## 对外接口

- `lint-imports.sh`：执行 agent 依赖隔离纪律检查。
- `lint-sql.sh`：执行可移植 SQL 方言检查（阻断业务代码中的 Oracle 与 MySQL 方言）。
- 可通过 `make lint` 触发调用。

## 依赖谁

- 标准 Bash 运行环境与 Go 工具链。
