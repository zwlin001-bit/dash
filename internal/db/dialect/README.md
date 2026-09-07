# internal/db/dialect

数据库方言差异收敛层。

## 职责边界

- **全工程唯一允许包含方言差异的包**（PRINCIPLES P2）。
- 仅处理三处受控方言差异：
  1. 占位符改写（`Rebind`）：`?` -> `:1, :2` (Oracle) / 原样 (MySQL)
  2. 分页（`Paginate`）：`OFFSET ? ROWS FETCH NEXT ? ROWS ONLY` (Oracle) / `LIMIT ? OFFSET ?` (MySQL)
  3. Upsert 与批量插入（`UpsertSQL` / `BatchInsert`）：`MERGE INTO` + 数组绑定 (Oracle) / `ON DUPLICATE KEY UPDATE` + 多行 VALUES (MySQL)
- 禁止任何业务代码直接 import 此包。

## 对外接口

```go
type Dialect interface {
    Name() string
    Rebind(q string) string
    Paginate(q string, limit, offset int) (string, []any)
    UpsertSQL(table string, keyCols, updCols []string) string
    BatchInsert(ctx context.Context, ex Execer, table string, cols []string, rows [][]any) error
}

func Get(driver string) (Dialect, error)
```

## 依赖谁

- 标准库 `context`, `database/sql`, `fmt`, `strings`
- 零其他内部模块依赖
