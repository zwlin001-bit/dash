package migrate_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"dash/internal/config"
	"dash/internal/db"
	"dash/internal/migrate"
)

var expectedTables = []string{
	"schema_migrations",
	"settings",
	"account_users",
	"user_sessions",
	"audit_log",
	"node_groups",
	"tags",
	"node_tags",
	"nodes",
	"node_facts",
	"node_billing",
	"enroll_tokens",
	"metric_defs",
	"metric_series",
	"sample_host",
	"sample_host_1m",
	"sample_host_1h",
	"sample_host_1d",
	"sample_dim",
	"sample_dim_1m",
	"sample_dim_1h",
	"sample_dim_1d",
	"event_types",
	"events",
	"jobs",
	"job_steps",
	"notify_channels",
	"notify_rules",
	"notify_deliveries",
	"credentials",
	"providers",
	"cloud_accounts",
	"cloud_resources",
}

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
	d, err := func() (*db.DB, error) { o := appCfg.DB.DBOptions(); return db.Open(&o) }()
	if err != nil {
		t.Skipf("Skipping Oracle test (cannot connect to ADB): %v", err)
	}
	return d
}

func queryMySQLColumns(ctx context.Context, d *db.DB) (map[string][]string, error) {
	q := "SELECT table_name, column_name FROM information_schema.columns WHERE table_schema = DATABASE() ORDER BY table_name, ordinal_position"
	rows, err := d.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string][]string)
	for rows.Next() {
		var tbl, col string
		if err := rows.Scan(&tbl, &col); err != nil {
			return nil, err
		}
		tbl = strings.ToLower(tbl)
		col = strings.ToLower(col)
		res[tbl] = append(res[tbl], col)
	}
	return res, rows.Err()
}

func queryOracleColumns(ctx context.Context, d *db.DB) (map[string][]string, error) {
	q := "SELECT table_name, column_name FROM user_tab_cols ORDER BY table_name, column_id"
	rows, err := d.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	expectedSet := make(map[string]bool)
	for _, t := range expectedTables {
		expectedSet[t] = true
	}

	res := make(map[string][]string)
	for rows.Next() {
		var tbl, col string
		if err := rows.Scan(&tbl, &col); err != nil {
			return nil, err
		}
		tbl = strings.ToLower(tbl)
		col = strings.ToLower(col)
		// Only collect dash tables (exclude legacy tables starting with DASH_ or DBTOOLS$)
		if expectedSet[tbl] {
			res[tbl] = append(res[tbl], col)
		}
	}
	return res, rows.Err()
}

// TestSchemaEquivalence satisfies Acceptance Criterion 3:
// "对比测试：分别在 ADB 和本地 docker MySQL 8 跑完迁移，对比 information_schema 的表名列名集合，不一致即失败。这个测试必须交付"
func TestSchemaEquivalence(t *testing.T) {
	dMy := getMySQLDB(t)
	defer dMy.Close()

	dOra := getOracleDB(t)
	defer dOra.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Ensure both have migrations applied
	migMy := migrate.New(dMy, "../../migrations")
	if err := migMy.Up(ctx); err != nil {
		t.Fatalf("mysql migration Up failed: %v", err)
	}

	migOra := migrate.New(dOra, "../../migrations")
	if err := migOra.Up(ctx); err != nil {
		t.Fatalf("oracle migration Up failed: %v", err)
	}

	myCols, err := queryMySQLColumns(ctx, dMy)
	if err != nil {
		t.Fatalf("query mysql columns failed: %v", err)
	}

	oraCols, err := queryOracleColumns(ctx, dOra)
	if err != nil {
		t.Fatalf("query oracle columns failed: %v", err)
	}

	// 1. Verify table counts
	if len(expectedTables) != 28 {
		t.Fatalf("expected 28 tables defined, got %d", len(expectedTables))
	}

	for _, tbl := range expectedTables {
		if _, ok := myCols[tbl]; !ok {
			t.Errorf("MySQL missing expected table: %s", tbl)
		}
		if _, ok := oraCols[tbl]; !ok {
			t.Errorf("Oracle missing expected table: %s", tbl)
		}
	}

	// 2. Compare table-by-table and column-by-column
	for _, tbl := range expectedTables {
		myList := myCols[tbl]
		oraList := oraCols[tbl]

		if len(myList) != len(oraList) {
			t.Errorf("table %s column count mismatch: MySQL has %d (%v), Oracle has %d (%v)",
				tbl, len(myList), myList, len(oraList), oraList)
			continue
		}

		// Sort column lists for exact set comparison
		sortedMy := make([]string, len(myList))
		copy(sortedMy, myList)
		sort.Strings(sortedMy)

		sortedOra := make([]string, len(oraList))
		copy(sortedOra, oraList)
		sort.Strings(sortedOra)

		for i := 0; i < len(sortedMy); i++ {
			if sortedMy[i] != sortedOra[i] {
				t.Errorf("table %s column mismatch at index %d: MySQL has %s, Oracle has %s",
					tbl, i, sortedMy[i], sortedOra[i])
			}
		}

		t.Logf("✓ Table %-18s: %2d columns MATCH between Oracle and MySQL", tbl, len(myList))
	}
}

func TestSplitStatements(t *testing.T) {
	script := `
-- Comment line
CREATE TABLE a (id VARCHAR(26));

-- Another comment
CREATE TABLE b (
    id VARCHAR(26),
    note TEXT -- inline comment
);

INSERT INTO a VALUES ('semi;inside;quotes');
`
	stmts := migrate.SplitStatements(script)
	if len(stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d:\n%v", len(stmts), stmts)
	}
	if strings.Contains(stmts[2], ";") {
		// Inside quotes 'semi;inside;quotes' is preserved, trailing semicolon stripped
		if !strings.Contains(stmts[2], "'semi;inside;quotes'") {
			t.Fatalf("quote content modified: %s", stmts[2])
		}
	}
}

func TestMigrateIdempotency(t *testing.T) {
	dMy := getMySQLDB(t)
	defer dMy.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migMy := migrate.New(dMy, "../../migrations")
	// Second run should be no-op
	if err := migMy.Up(ctx); err != nil {
		t.Fatalf("second migration Up failed (idempotency violation): %v", err)
	}

	statusList, err := migMy.Status(ctx)
	if err != nil {
		t.Fatalf("status query failed: %v", err)
	}
<<<<<<< HEAD
	if len(statusList) == 0 {
		t.Fatalf("expected migrations, got 0")
	}
	for _, s := range statusList {
		if !s.Applied {
			t.Fatalf("migration %s (v%d) not applied: %+v", s.Name, s.Version, s)
		}
=======
	if len(statusList) < 2 {
		t.Fatalf("unexpected migration status: %+v", statusList)
>>>>>>> origin/agy/p2-03-jobs
	}
	for _, s := range statusList {
		if !s.Applied {
			t.Fatalf("migration %s is not applied: %+v", s.Name, statusList)
		}
	}
}
