# internal/db

可移植 SQL 数据库访问层。所有业务 SQL 必须经过的统一底层。

## 职责边界

- 统一封装 Oracle ADB（自治数据库，基于纯 Go 驱动 `go-ora/v2` 与 TLS / wallet mTLS）与 MySQL 8 访问。
- 提供连接池管理与 `WithTx` 事务生命周期封装（错误或 panic 均安全回滚）。
- 提供 `BatchInsert` 批量高效写入（超过 1000 行自动切片分批）。
- 提供 `Upsert` 统一主键/唯一索引冲突更新操作。
- 集中隔离全工程仅有的三处方言差异（占位符改写、分页、批量插入/upsert），业务代码禁止接触 `dialect` 包。
- 业务代码一律写 `?` 占位符，严禁在业务代码中使用方言（如 ROWNUM、SYSDATE、MERGE、FROM dual 等，由 `scripts/lint-sql.sh` 自动化门禁防护）。

## 对外接口

```go
// 打开数据库连接并初始化连接池与连通性自检
func Open(cfg *Config) (*DB, error)

// 执行通用查询，内部自动根据方言执行 Rebind 改写
func (d *DB) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error)
func (d *DB) QueryRow(ctx context.Context, q string, args ...any) *sql.Row
func (d *DB) Exec(ctx context.Context, q string, args ...any) (sql.Result, error)

// 分页查询：传基础 SQL（不带 LIMIT/FETCH），由方言自动补全并传参
func (d *DB) QueryPage(ctx context.Context, q string, limit, offset int, args ...any) (*sql.Rows, error)

// 事务封装：fn 返回错误或发生 panic 时自动 Rollback，成功自动 Commit
func (d *DB) WithTx(ctx context.Context, fn func(*Tx) error) error

// 批量插入：rows 每行长度必须等于 len(cols)，超 1000 行自动分片
func (d *DB) BatchInsert(ctx context.Context, table string, cols []string, rows [][]any) error

// 冲突更新：keyCols 为冲突判定列，updCols 为匹配时更新列
func (d *DB) Upsert(ctx context.Context, table string, keyCols, updCols []string, row map[string]any) error
```

`*Tx` 同样暴露 `Query`, `QueryRow`, `Exec`, `QueryPage`, `BatchInsert`, `Upsert` 接口。

## 方言差异隔离（dialect 子包）

全工程仅有 `internal/db/dialect` 包含方言差异，绝不扩散：

1. **占位符改写**：业务代码统一写 `?`，Oracle 下改写为 `:1, :2`，MySQL 下原样透传。
2. **分页**：Oracle 为 `OFFSET ? ROWS FETCH NEXT ? ROWS ONLY`，MySQL 为 `LIMIT ? OFFSET ?`。
3. **批量写入与 Upsert**：
   - Oracle 批量插入采用 `go-ora` 原生数组绑定（切片参数传递），Upsert 采用 `MERGE INTO ... USING (SELECT ... FROM dual)`；
   - MySQL 批量插入采用多行 `VALUES (...), (...)`，Upsert 采用 `INSERT ... ON DUPLICATE KEY UPDATE`。

## 语义约定

- `db.ErrNotFound`：包装 `sql.ErrNoRows`，业务层统一使用 `errors.Is(err, db.ErrNotFound)`。
- 连接失败时敏感密码自动进行脱敏打码，不泄露明文。
- 0 行记录传给 `BatchInsert` 时直接返回 `nil`，不向数据库发送语句。

## 依赖谁

- `github.com/sijms/go-ora/v2`（纯 Go Oracle 驱动，无 CGO 依赖）
- `github.com/go-sql-driver/mysql`（纯 Go MySQL 驱动）
- `dash/internal/db/dialect`
