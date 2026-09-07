package dialect

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// MySQLDialect implements Dialect interface for MySQL 8+.
type MySQLDialect struct{}

func (d *MySQLDialect) Name() string {
	return "mysql"
}

// Rebind leaves standard '?' placeholders untouched as MySQL natively supports '?'.
func (d *MySQLDialect) Rebind(q string) string {
	return q
}

// Paginate appends MySQL pagination syntax: LIMIT ? OFFSET ?.
// Returned arguments are in the order [limit, offset].
func (d *MySQLDialect) Paginate(q string, limit, offset int) (string, []any) {
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 0
	}
	base := strings.TrimRight(strings.TrimSpace(q), ";")
	pagedSQL := fmt.Sprintf("%s LIMIT ? OFFSET ?", base)
	return pagedSQL, []any{limit, offset}
}

// UpsertSQL constructs a MySQL INSERT ... ON DUPLICATE KEY UPDATE statement.
func (d *MySQLDialect) UpsertSQL(table string, keyCols, updCols []string) string {
	allCols := make([]string, 0, len(keyCols)+len(updCols))
	seen := make(map[string]bool)
	for _, k := range keyCols {
		if !seen[k] {
			seen[k] = true
			allCols = append(allCols, k)
		}
	}
	for _, u := range updCols {
		if !seen[u] {
			seen[u] = true
			allCols = append(allCols, u)
		}
	}

	placeholders := make([]string, len(allCols))
	for i := range allCols {
		placeholders[i] = "?"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table, strings.Join(allCols, ", "), strings.Join(placeholders, ", ")))

	if len(updCols) > 0 {
		updClauses := make([]string, len(updCols))
		for i, u := range updCols {
			updClauses[i] = fmt.Sprintf("%s = VALUES(%s)", u, u)
		}
		sb.WriteString(fmt.Sprintf(" ON DUPLICATE KEY UPDATE %s", strings.Join(updClauses, ", ")))
	} else if len(keyCols) > 0 {
		sb.WriteString(fmt.Sprintf(" ON DUPLICATE KEY UPDATE %s = %s", keyCols[0], keyCols[0]))
	}

	return sb.String()
}

// BatchInsert executes mass insertion into table using multi-row VALUES syntax.
// Batches exceeding maxBatchSize (1000) are automatically split.
func (d *MySQLDialect) BatchInsert(ctx context.Context, ex Execer, table string, cols []string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	if len(cols) == 0 {
		return errors.New("db: no columns specified for BatchInsert")
	}
	for i, r := range rows {
		if len(r) != len(cols) {
			return fmt.Errorf("db: row %d length %d does not match columns length %d", i, len(r), len(cols))
		}
	}

	// Template for a single row: (?, ?, ...)
	rowPlaceholders := "(" + strings.Repeat("?, ", len(cols)-1) + "?)"

	for start := 0; start < len(rows); start += maxBatchSize {
		end := start + maxBatchSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[start:end]

		valPlaceholders := make([]string, len(chunk))
		flatArgs := make([]any, 0, len(chunk)*len(cols))
		for r, row := range chunk {
			valPlaceholders[r] = rowPlaceholders
			flatArgs = append(flatArgs, row...)
		}

		sqlText := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s",
			table, strings.Join(cols, ", "), strings.Join(valPlaceholders, ", "))

		if _, err := ex.ExecContext(ctx, sqlText, flatArgs...); err != nil {
			return fmt.Errorf("mysql batch insert failed (rows %d-%d): %w", start, end-1, err)
		}
	}

	return nil
}
