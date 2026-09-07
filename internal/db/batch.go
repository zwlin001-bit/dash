package db

import (
	"context"

	"dash/internal/db/dialect"
)

// BatchInsert bulk-inserts multiple rows into table.
// Each element in rows must have length equal to len(cols).
// Automatically chunks requests exceeding 1000 rows.
func (d *DB) BatchInsert(ctx context.Context, table string, cols []string, rows [][]any) error {
	return d.dialect.BatchInsert(ctx, d.db, table, cols, rows)
}

// BatchInsert executes BatchInsert within the transaction.
func (tx *Tx) BatchInsert(ctx context.Context, table string, cols []string, rows [][]any) error {
	return tx.dialect.BatchInsert(ctx, tx.tx, table, cols, rows)
}

// Upsert performs an upsert: keyCols determine uniqueness, updCols determine what is updated on collision.
func (d *DB) Upsert(ctx context.Context, table string, keyCols, updCols []string, row map[string]any) error {
	return execUpsert(ctx, d.db, d.dialect, table, keyCols, updCols, row)
}

// Upsert executes Upsert within the transaction.
func (tx *Tx) Upsert(ctx context.Context, table string, keyCols, updCols []string, row map[string]any) error {
	return execUpsert(ctx, tx.tx, tx.dialect, table, keyCols, updCols, row)
}

func execUpsert(ctx context.Context, ex dialect.Execer, dia dialect.Dialect, table string, keyCols, updCols []string, row map[string]any) error {
	sqlText := dia.UpsertSQL(table, keyCols, updCols)

	// Collect ordered columns: keyCols first, then updCols without duplicates
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

	args := make([]any, len(allCols))
	for i, col := range allCols {
		args[i] = row[col]
	}

	rebound := dia.Rebind(sqlText)
	_, err := ex.ExecContext(ctx, rebound, args...)
	return err
}
