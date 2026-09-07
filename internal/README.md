# internal

dashd 服务端私有模块集合。

## 职责边界

- 包含服务端的核心业务实现、数据库交互、接入层与管理逻辑。
- Go 编译器限制：`internal/` 目录下的包仅能被同根模块引用。
- **架构硬性约束**：`agent/` 及其二进制严禁引用除 `internal/protocol` 外的任何 `internal/` 包。

## 目录结构

- `config/`：配置读取与环境覆盖
- `logx/`：结构化日志与敏感信息打码
- `db/`：可移植 SQL 数据库访问层与方言隔离
- `protocol/`：agent 与服务端共享的通信协议契约
- `control/`：agent 连接管理与指令下发
- `ingest/`：指标接收、批量落库与 rollup
- `api/`：控制台 REST API、SSE 与接入端点
- `inventory/`：节点资产清单与标签管理
