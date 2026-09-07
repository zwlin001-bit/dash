package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"dash/internal/config"
	"dash/internal/db"
)

func getMySQLDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("Skipping MySQL test (cannot connect to local MySQL on 33306): %v", err)
	}
	return d
}

func getOracleDB(t *testing.T) *db.DB {
	appCfg, err := config.Load("/etc/dash/config.toml")
	if err != nil || appCfg.DB.DSN == "" {
		t.Skipf("Skipping Oracle test (missing DSN): %v", err)
	}
	appCfg.DB.Driver = "oracle"
	d, err := db.Open(&appCfg.DB)
	if err != nil {
		t.Skipf("Skipping Oracle test (cannot connect to ADB): %v", err)
	}
	return d
}

func testDatabaseOperations(t *testing.T, d *db.DB, tableName string, createDDL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Clean up table beforehand
	dropTable(d, tableName)

	// Create table
	if _, err := d.Exec(ctx, createDDL); err != nil {
		t.Fatalf("[%s] failed to create test table: %v", d.DriverName(), err)
	}
	defer dropTable(d, tableName)

	// 1. BatchInsert with > 1000 rows to test auto-chunking (1000 limit)
	totalRows := 1200
	rows := make([][]any, totalRows)
	for i := 0; i < totalRows; i++ {
		rows[i] = []any{fmt.Sprintf("ID_%05d", i), i * 2}
	}

	cols := []string{"id", "val"}
	if err := d.BatchInsert(ctx, tableName, cols, rows); err != nil {
		t.Fatalf("[%s] BatchInsert 1200 rows failed: %v", d.DriverName(), err)
	}

	// Verify count
	var count int
	err := d.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)).Scan(&count)
	if err != nil {
		t.Fatalf("[%s] count failed: %v", d.DriverName(), err)
	}
	if count != totalRows {
		t.Fatalf("[%s] expected %d rows, got %d", d.DriverName(), totalRows, count)
	}

	// 2. Query single row with '?' placeholder (verifies Rebind on Oracle & passthrough on MySQL)
	var val int
	err = d.QueryRow(ctx, fmt.Sprintf("SELECT val FROM %s WHERE id = ?", tableName), "ID_00010").Scan(&val)
	if err != nil {
		t.Fatalf("[%s] QueryRow with ? failed: %v", d.DriverName(), err)
	}
	if val != 20 {
		t.Fatalf("[%s] expected val=20, got %d", d.DriverName(), val)
	}

	// 3. Upsert: insert new row
	upsertRow1 := map[string]any{
		"id":  "ID_99999",
		"val": 999,
	}
	if err := d.Upsert(ctx, tableName, []string{"id"}, []string{"val"}, upsertRow1); err != nil {
		t.Fatalf("[%s] Upsert new row failed: %v", d.DriverName(), err)
	}
	var upsertVal int
	if err := d.QueryRow(ctx, fmt.Sprintf("SELECT val FROM %s WHERE id = ?", tableName), "ID_99999").Scan(&upsertVal); err != nil {
		t.Fatalf("[%s] query upsert row failed: %v", d.DriverName(), err)
	}
	if upsertVal != 999 {
		t.Fatalf("[%s] expected 999, got %d", d.DriverName(), upsertVal)
	}

	// 4. Upsert: update existing row
	upsertRow2 := map[string]any{
		"id":  "ID_99999",
		"val": 1888,
	}
	if err := d.Upsert(ctx, tableName, []string{"id"}, []string{"val"}, upsertRow2); err != nil {
		t.Fatalf("[%s] Upsert update existing row failed: %v", d.DriverName(), err)
	}
	if err := d.QueryRow(ctx, fmt.Sprintf("SELECT val FROM %s WHERE id = ?", tableName), "ID_99999").Scan(&upsertVal); err != nil {
		t.Fatalf("[%s] query upsert row after update failed: %v", d.DriverName(), err)
	}
	if upsertVal != 1888 {
		t.Fatalf("[%s] expected 1888 after upsert update, got %d", d.DriverName(), upsertVal)
	}

	// 5. WithTx transaction test: commit
	err = d.WithTx(ctx, func(tx *db.Tx) error {
		txRow := map[string]any{"id": "ID_TX_01", "val": 777}
		return tx.Upsert(ctx, tableName, []string{"id"}, []string{"val"}, txRow)
	})
	if err != nil {
		t.Fatalf("[%s] WithTx commit failed: %v", d.DriverName(), err)
	}
	var txVal int
	if err := d.QueryRow(ctx, fmt.Sprintf("SELECT val FROM %s WHERE id = ?", tableName), "ID_TX_01").Scan(&txVal); err != nil {
		t.Fatalf("[%s] query tx committed row failed: %v", d.DriverName(), err)
	}
	if txVal != 777 {
		t.Fatalf("[%s] expected 777, got %d", d.DriverName(), txVal)
	}

	// 6. WithTx transaction test: rollback on error
	_ = d.WithTx(ctx, func(tx *db.Tx) error {
		txRow := map[string]any{"id": "ID_TX_FAIL", "val": 888}
		_ = tx.Upsert(ctx, tableName, []string{"id"}, []string{"val"}, txRow)
		return fmt.Errorf("intentional rollback")
	})
	var failVal int
	err = d.QueryRow(ctx, fmt.Sprintf("SELECT val FROM %s WHERE id = ?", tableName), "ID_TX_FAIL").Scan(&failVal)
	if err == nil {
		t.Fatalf("[%s] expected row to be rolled back, but found val=%d", d.DriverName(), failVal)
	}
}

func dropTable(d *db.DB, tableName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if d.DriverName() == "oracle" {
		_, _ = d.Exec(ctx, fmt.Sprintf("DROP TABLE %s PURGE", tableName))
	} else {
		_, _ = d.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName))
	}
}

func TestMySQLOperations(t *testing.T) {
	d := getMySQLDB(t)
	defer d.Close()

	ddl := "CREATE TABLE test_mysql_ops (id VARCHAR(26) PRIMARY KEY, val INT)"
	testDatabaseOperations(t, d, "test_mysql_ops", ddl)
}

func TestOracleOperations(t *testing.T) {
	d := getOracleDB(t)
	defer d.Close()

	ddl := "CREATE TABLE test_ora_ops (id VARCHAR2(26) PRIMARY KEY, val NUMBER(10))"
	testDatabaseOperations(t, d, "test_ora_ops", ddl)
}

// TestPaginateEquivalence satisfies Acceptance Criterion 5:
// "Paginate 在两个数据库上返回同样的分页结果（写测试）"
func TestPaginateEquivalence(t *testing.T) {
	dMy := getMySQLDB(t)
	defer dMy.Close()

	dOra := getOracleDB(t)
	defer dOra.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tableMy := "test_page_my"
	tableOra := "test_page_ora"

	dropTable(dMy, tableMy)
	dropTable(dOra, tableOra)

	if _, err := dMy.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (id VARCHAR(26) PRIMARY KEY, val INT)", tableMy)); err != nil {
		t.Fatalf("failed to create mysql table: %v", err)
	}
	defer dropTable(dMy, tableMy)

	if _, err := dOra.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (id VARCHAR2(26) PRIMARY KEY, val NUMBER(10))", tableOra)); err != nil {
		t.Fatalf("failed to create oracle table: %v", err)
	}
	defer dropTable(dOra, tableOra)

	// Populate both tables with identical records: 10 items
	rows := make([][]any, 10)
	for i := 0; i < 10; i++ {
		rows[i] = []any{fmt.Sprintf("P_%02d", i), i * 100}
	}

	cols := []string{"id", "val"}
	if err := dMy.BatchInsert(ctx, tableMy, cols, rows); err != nil {
		t.Fatalf("mysql batch insert failed: %v", err)
	}
	if err := dOra.BatchInsert(ctx, tableOra, cols, rows); err != nil {
		t.Fatalf("oracle batch insert failed: %v", err)
	}

	// Query page: pageSize=4, offset=2
	// Expect items: P_02, P_03, P_04, P_05
	pageSize := 4
	offset := 2

	fetchPage := func(d *db.DB, table string) ([]string, []int) {
		q := fmt.Sprintf("SELECT id, val FROM %s ORDER BY id", table)
		rowsRes, err := d.QueryPage(ctx, q, pageSize, offset)
		if err != nil {
			t.Fatalf("[%s] QueryPage failed: %v", d.DriverName(), err)
		}
		defer rowsRes.Close()

		var ids []string
		var vals []int
		for rowsRes.Next() {
			var id string
			var val int
			if err := rowsRes.Scan(&id, &val); err != nil {
				t.Fatalf("[%s] scan row failed: %v", d.DriverName(), err)
			}
			ids = append(ids, id)
			vals = append(vals, val)
		}
		return ids, vals
	}

	idsMy, valsMy := fetchPage(dMy, tableMy)
	idsOra, valsOra := fetchPage(dOra, tableOra)

	if len(idsMy) != pageSize || len(idsOra) != pageSize {
		t.Fatalf("expected page length %d, got MySQL=%d, Oracle=%d", pageSize, len(idsMy), len(idsOra))
	}

	for i := 0; i < pageSize; i++ {
		if idsMy[i] != idsOra[i] || valsMy[i] != valsOra[i] {
			t.Fatalf("row %d mismatch: MySQL=(%s, %d), Oracle=(%s, %d)",
				i, idsMy[i], valsMy[i], idsOra[i], valsOra[i])
		}
	}

	t.Logf("Pagination equivalence verified! Page rows: %v", idsMy)
}
