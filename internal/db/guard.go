package db

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// CheckSafeDB inspects the database driver, configured user, and DSN.
// It returns an error if any of the following unsafe rules are met:
// 1. Database name is "dash_test", "test", or ends with "_test"
// 2. Port is 33306 (the designated test port)
// 3. User is "root" (production should not connect as root)
func CheckSafeDB(driver, user, dsnStr string) error {
	driver = strings.ToLower(strings.TrimSpace(driver))
	if driver != "mysql" {
		// Oracle or other drivers
		if strings.EqualFold(strings.TrimSpace(user), "root") {
			return errors.New("unsafe db: DSN user is 'root' (rule: production must not use root user)")
		}
		return nil
	}

	rawDSN := strings.TrimSpace(dsnStr)
	var cfg *mysql.Config
	var err error
	if rawDSN != "" {
		cfg, err = mysql.ParseDSN(rawDSN)
	}

	dbUser := strings.TrimSpace(user)
	dbName := ""
	dbPort := ""

	if err == nil && cfg != nil {
		if strings.EqualFold(dbUser, "root") || strings.EqualFold(cfg.User, "root") {
			dbUser = "root"
		} else if dbUser == "" {
			dbUser = cfg.User
		}
		dbName = cfg.DBName
		if cfg.Addr != "" {
			if _, p, splitErr := net.SplitHostPort(cfg.Addr); splitErr == nil {
				dbPort = p
			} else {
				dbPort = cfg.Addr
			}
		}
	} else {
		// Fallback manual parsing if DSN cannot be fully parsed by mysql.ParseDSN
		if strings.Contains(rawDSN, "@") {
			parts := strings.SplitN(rawDSN, "@", 2)
			userPass := parts[0]
			dsnUser := userPass
			if strings.Contains(userPass, ":") {
				dsnUser = strings.SplitN(userPass, ":", 2)[0]
			}
			if strings.EqualFold(dbUser, "root") || strings.EqualFold(dsnUser, "root") {
				dbUser = "root"
			} else if dbUser == "" {
				dbUser = dsnUser
			}
			rest := parts[1]
			if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
				afterSlash := rest[slashIdx+1:]
				if qIdx := strings.Index(afterSlash, "?"); qIdx != -1 {
					dbName = afterSlash[:qIdx]
				} else {
					dbName = afterSlash
				}
			}
			if strings.Contains(rest, ":33306") {
				dbPort = "33306"
			}
		}
	}

	// Rule 1: Database name is dash_test, test, or ends with _test
	dbNameLower := strings.ToLower(strings.TrimSpace(dbName))
	if dbNameLower == "dash_test" || dbNameLower == "test" || strings.HasSuffix(dbNameLower, "_test") {
		return fmt.Errorf("unsafe db: database name %q is a test database (rule: db name is dash_test, test, or ends with _test)", dbName)
	}

	// Rule 2: Port is 33306
	if dbPort == "33306" || strings.Contains(rawDSN, ":33306") {
		return errors.New("unsafe db: database port is 33306 (rule: port 33306 is reserved for testing)")
	}

	// Rule 3: DSN user is root
	if strings.EqualFold(dbUser, "root") || strings.EqualFold(strings.TrimSpace(user), "root") {
		return errors.New("unsafe db: DSN user is 'root' (rule: production must not use root user)")
	}

	return nil
}

// EnsureSafeTestDB verifies that target database is NOT a production database.
// If the database contains existing nodes (COUNT > 0) or enroll_tokens / agent tokens,
// it halts the test immediately via t.Fatal to avoid destroying production data.
func EnsureSafeTestDB(t testing.TB, database *DB) {
	t.Helper()
	if database == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check 1: nodes table existence and count > 0
	var nodesCount int
	err := database.QueryRow(ctx, "SELECT COUNT(*) FROM nodes").Scan(&nodesCount)
	if err == nil && nodesCount > 0 {
		t.Fatalf("拒绝在疑似生产库上跑测试。测试库应当是空的。(found %d rows in nodes)", nodesCount)
	}

	// Check 2: enroll_tokens existence and count > 0
	var tokensCount int
	err = database.QueryRow(ctx, "SELECT COUNT(*) FROM enroll_tokens").Scan(&tokensCount)
	if err == nil && tokensCount > 0 {
		t.Fatalf("拒绝在疑似生产库上跑测试。测试库应当是空的。(found %d rows in enroll_tokens)", tokensCount)
	}

	// Check 3: node_facts existence and count > 0 (agent info)
	var factsCount int
	err = database.QueryRow(ctx, "SELECT COUNT(*) FROM node_facts").Scan(&factsCount)
	if err == nil && factsCount > 0 {
		t.Fatalf("拒绝在疑似生产库上跑测试。测试库应当是空的。(found %d rows in node_facts)", factsCount)
	}
}
