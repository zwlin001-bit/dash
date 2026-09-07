package dialect

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const maxBatchSize = 1000

// OracleDialect implements the Dialect interface for Oracle Autonomous Database (ADB).
type OracleDialect struct{}

func (d *OracleDialect) Name() string {
	return "oracle"
}

// Rebind converts standard '?' placeholders to Oracle ':1', ':2' style placeholders.
// It is idempotent: queries without '?' or already using ':n' placeholders are untouched.
// String literals and comments containing '?' are safely preserved.
func (d *OracleDialect) Rebind(q string) string {
	if !strings.Contains(q, "?") {
		return q
	}

	var sb strings.Builder
	sb.Grow(len(q) + 16)

	n := len(q)
	i := 0
	inString := false
	inLineComment := false
	inBlockComment := false

	// First find the highest existing :N parameter index to ensure continuity
	maxExistingParam := 0
	for j := 0; j < n; j++ {
		ch := q[j]
		if ch == '\'' {
			if inString && j+1 < n && q[j+1] == '\'' {
				j++
				continue
			}
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if !inBlockComment && !inLineComment && ch == '-' && j+1 < n && q[j+1] == '-' {
			inLineComment = true
			j++
			continue
		}
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if !inLineComment && !inBlockComment && ch == '/' && j+1 < n && q[j+1] == '*' {
			inBlockComment = true
			j++
			continue
		}
		if inBlockComment {
			if ch == '*' && j+1 < n && q[j+1] == '/' {
				inBlockComment = false
				j++
			}
			continue
		}

		if ch == ':' && j+1 < n && isDigit(q[j+1]) {
			k := j + 1
			for k < n && isDigit(q[k]) {
				k++
			}
			if val, err := strconv.Atoi(q[j+1 : k]); err == nil {
				if val > maxExistingParam {
					maxExistingParam = val
				}
			}
			j = k - 1
		}
	}

	inString = false
	inLineComment = false
	inBlockComment = false
	paramIndex := maxExistingParam + 1

	for i < n {
		ch := q[i]
		// Handle string literals
		if ch == '\'' {
			sb.WriteByte(ch)
			i++
			for i < n {
				c := q[i]
				sb.WriteByte(c)
				if c == '\'' {
					if i+1 < n && q[i+1] == '\'' {
						i++
						sb.WriteByte(q[i])
						i++
						continue
					}
					i++
					break
				}
				i++
			}
			continue
		}

		// Handle line comments
		if ch == '-' && i+1 < n && q[i+1] == '-' {
			sb.WriteString("--")
			i += 2
			for i < n {
				c := q[i]
				sb.WriteByte(c)
				i++
				if c == '\n' {
					break
				}
			}
			continue
		}

		// Handle block comments
		if ch == '/' && i+1 < n && q[i+1] == '*' {
			sb.WriteString("/*")
			i += 2
			for i < n {
				c := q[i]
				sb.WriteByte(c)
				if c == '*' && i+1 < n && q[i+1] == '/' {
					i++
					sb.WriteByte(q[i])
					i++
					break
				}
				i++
			}
			continue
		}

		// Outside strings and comments: replace '?' with ':paramIndex'
		if ch == '?' {
			sb.WriteByte(':')
			sb.WriteString(strconv.Itoa(paramIndex))
			paramIndex++
			i++
			continue
		}

		sb.WriteByte(ch)
		i++
	}

	return sb.String()
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

// Paginate appends Oracle pagination syntax: OFFSET ? ROWS FETCH NEXT ? ROWS ONLY.
// Returned arguments are in the order [offset, limit].
func (d *OracleDialect) Paginate(q string, limit, offset int) (string, []any) {
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 0
	}
	base := strings.TrimRight(strings.TrimSpace(q), ";")
	pagedSQL := fmt.Sprintf("%s OFFSET ? ROWS FETCH NEXT ? ROWS ONLY", base)
	return pagedSQL, []any{offset, limit}
}

// UpsertSQL constructs an Oracle MERGE statement.
// keyCols are conflict target keys, updCols are columns updated upon matching.
func (d *OracleDialect) UpsertSQL(table string, keyCols, updCols []string) string {
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

	// USING (SELECT :1 c1, :2 c2, ... FROM dual) s
	selectCols := make([]string, len(allCols))
	for i, col := range allCols {
		selectCols[i] = fmt.Sprintf(":%d %s", i+1, col)
	}

	// ON (d.k1 = s.k1 AND ...)
	onClauses := make([]string, len(keyCols))
	for i, k := range keyCols {
		onClauses[i] = fmt.Sprintf("d.%s = s.%s", k, k)
	}

	// WHEN NOT MATCHED THEN INSERT (allCols) VALUES (s.c1, s.c2, ...)
	insertVals := make([]string, len(allCols))
	for i, c := range allCols {
		insertVals[i] = fmt.Sprintf("s.%s", c)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("MERGE INTO %s d\n", table))
	sb.WriteString(fmt.Sprintf("USING (SELECT %s FROM dual) s\n", strings.Join(selectCols, ", ")))
	sb.WriteString(fmt.Sprintf("ON (%s)\n", strings.Join(onClauses, " AND ")))

	if len(updCols) > 0 {
		updClauses := make([]string, len(updCols))
		for i, u := range updCols {
			updClauses[i] = fmt.Sprintf("d.%s = s.%s", u, u)
		}
		sb.WriteString(fmt.Sprintf("WHEN MATCHED THEN UPDATE SET %s\n", strings.Join(updClauses, ", ")))
	}

	sb.WriteString(fmt.Sprintf("WHEN NOT MATCHED THEN INSERT (%s) VALUES (%s)",
		strings.Join(allCols, ", "), strings.Join(insertVals, ", ")))

	return sb.String()
}

// BatchInsert executes mass insertion into table using Oracle array binding.
// Batches exceeding maxBatchSize (1000) are automatically split.
func (d *OracleDialect) BatchInsert(ctx context.Context, ex Execer, table string, cols []string, rows [][]any) error {
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

	// Prepare placeholder statement: INSERT INTO t (c1, c2) VALUES (:1, :2)
	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = fmt.Sprintf(":%d", i+1)
	}
	sqlText := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))

	for start := 0; start < len(rows); start += maxBatchSize {
		end := start + maxBatchSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[start:end]

		// Build column slices for array binding
		colArgs := make([]any, len(cols))
		for c := range cols {
			slice := make([]any, len(chunk))
			for r := range chunk {
				slice[r] = chunk[r][c]
			}
			colArgs[c] = slice
		}

		if _, err := ex.ExecContext(ctx, sqlText, colArgs...); err != nil {
			return fmt.Errorf("oracle batch insert failed (rows %d-%d): %w", start, end-1, err)
		}
	}

	return nil
}
