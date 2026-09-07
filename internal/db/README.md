# internal/db

可移植 SQL 数据库访问层。

## 职责边界

- 统一封装 Oracle ADB（自治数据库，基于纯 Go 驱动 `go-ora/v2` 与 wallet mTLS）与 MySQL 8 访问。
- 提供连接池管理与 `WithTx` 事务生命周期封装。
- 提供 `BatchInsert` 批量高效写入。
- 集中隔离全工程仅有的方言差异（占位符改写、分页、批量插入/upsert），业务代码禁止接触方言实现。
- 业务代码严禁使用 Oracle 方言（如 ROWNUM、SYSDATE、MERGE、FROM dual 等）。

## 对外接口

- `Open(cfg *Config) (*DB, error)`
- `WithTx(ctx context.Context, fn func(tx *Tx) error) error`
- `BatchInsert(ctx context.Context, table string, cols []string, rows [][]any) error`
- `dialect` 子包接口：`Rebind`、`Paginate`、`Upsert`、`BatchInsert`

## 依赖谁

- `github.com/sijms/go-ora/v2`（纯 Go Oracle 驱动）
- MySQL 驱动
- `internal/config`
- `internal/logx`
