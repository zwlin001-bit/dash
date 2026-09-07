package control_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dash/internal/db"
	"dash/internal/migrate"
)

func getTestDB(t *testing.T) *db.DB {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		dsn = "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true"
	}
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    dsn,
	}
	database, err := db.Open(cfg)
	if err != nil {
		t.Skipf("skipping test: cannot connect to MySQL (%v)", err)
	}

	// 运行 migrations 确保表结构就绪
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 查找 migrations 目录
	migDir := "migrations"
	for _, cand := range []string{"../../migrations", "../../../migrations", "migrations"} {
		if stat, err := os.Stat(cand); err == nil && stat.IsDir() {
			migDir = cand
			break
		}
	}
	migDir, _ = filepath.Abs(migDir)

	mig := migrate.New(database, migDir)
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("run migrations failed: %v", err)
	}

	return database
}

func cleanTables(t *testing.T, database *db.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tables := []string{
		"audit_log",
		"node_facts",
		"node_tags",
		"node_billing",
		"nodes",
		"enroll_tokens",
	}

	_, _ = database.Exec(ctx, "SET FOREIGN_KEY_CHECKS = 0")
	for _, tbl := range tables {
		_, _ = database.Exec(ctx, "DELETE FROM "+tbl)
	}
	_, _ = database.Exec(ctx, "SET FOREIGN_KEY_CHECKS = 1")
}
