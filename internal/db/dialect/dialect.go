package dialect

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Execer is the minimal database execution contract needed by BatchInsert.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Dialect abstracts runtime dialect differences across supported database engines.
// PRINCIPLES P2: 方言差异只允许三处（占位符、分页、upsert/批量），全部集中在此包。
type Dialect interface {
	Name() string
	Rebind(q string) string
	Paginate(q string, limit, offset int) (string, []any)
	UpsertSQL(table string, keyCols, updCols []string) string
	BatchInsert(ctx context.Context, ex Execer, table string, cols []string, rows [][]any) error
}

var (
	oracleInstance = &OracleDialect{}
	mysqlInstance  = &MySQLDialect{}
)

// Get returns the Dialect implementation corresponding to the given driver name.
func Get(driver string) (Dialect, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "oracle", "go-ora", "godror", "oci8":
		return oracleInstance, nil
	case "mysql":
		return mysqlInstance, nil
	default:
		return nil, fmt.Errorf("unsupported database driver dialect: %q", driver)
	}
}
