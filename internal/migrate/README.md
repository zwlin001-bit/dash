# internal/migrate

数据库 Schema 迁移引擎。

## 职责边界

- 按照版本编号升序执行 `migrations/` 目录下的 SQL 迁移脚本。
- 根据当前连接的数据库引擎自动匹配对应方言文件（`NNNN_<name>.oracle.sql` 或 `NNNN_<name>.mysql.sql`）。
- 将已执行的迁移版本、脚本名称、SHA256 校验和、执行时间（epoch 毫秒）持久化在 `schema_migrations` 表中。
- 幂等性保证：重复执行自动跳过已应用版本；已应用脚本内容若被篡改则报错拒绝执行。
- 切分语句并安全移除单行注释与分号结尾，确保在 Oracle 与 MySQL 8 上均能可靠执行 DDL。

## 对外接口

```go
func New(d *db.DB, dir string) *Migrator
func (m *Migrator) Up(ctx context.Context) error
func (m *Migrator) Status(ctx context.Context) ([]Status, error)
func SplitStatements(body string) []string
```

## 依赖谁

- `dash/internal/db`
- `dash/internal/logx`
