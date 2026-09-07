package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"dash/internal/db"
)

func parseEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			if (strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`)) ||
				(strings.HasPrefix(v, `'`) && strings.HasSuffix(v, `'`)) {
				v = v[1 : len(v)-1]
			}
			env[k] = v
		}
	}
	return env, nil
}

func findEnvFile(specified string) string {
	if specified != "" {
		return specified
	}
	if v := os.Getenv("ADB_ENV_PATH"); v != "" {
		return v
	}
	candidates := []string{
		".secrets/adb.env",
		"../.secrets/adb.env",
		"../../.secrets/adb.env",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ".secrets/adb.env"
}

func main() {
	var (
		envPathFlag = flag.String("env", "", "path to .secrets/adb.env")
		driverFlag  = flag.String("driver", "", "override db driver (oracle | mysql)")
		dsnFlag     = flag.String("dsn", "", "override db dsn")
		userFlag    = flag.String("user", "", "override db user")
		passFlag    = flag.String("password", "", "override db password")
	)
	flag.Parse()

	envFile := findEnvFile(*envPathFlag)
	envVars, _ := parseEnvFile(envFile)
	if envVars == nil {
		envVars = make(map[string]string)
	}

	getVal := func(cliVal string, envKeys ...string) string {
		if cliVal != "" {
			return cliVal
		}
		for _, k := range envKeys {
			if v, ok := envVars[k]; ok && v != "" {
				return v
			}
			if v := os.Getenv(k); v != "" {
				return v
			}
		}
		return ""
	}

	dsn := getVal(*dsnFlag, "ADB_DSN", "DSN")
	user := getVal(*userFlag, "ADB_USER", "USER", "DB_USER")
	password := getVal(*passFlag, "ADB_PASSWORD", "PASSWORD", "DB_PASSWORD")
	driver := getVal(*driverFlag, "ADB_DRIVER", "DRIVER", "DB_DRIVER")

	if driver == "" {
		if strings.HasPrefix(dsn, "mysql") || strings.Contains(dsn, "@tcp(") {
			driver = "mysql"
		} else {
			driver = "oracle"
		}
	}

	if dsn == "" {
		fmt.Fprintf(os.Stderr, "Error: no DSN found in %s, env vars, or CLI flags\n", envFile)
		os.Exit(1)
	}

	fmt.Printf("Probing database connectivity (%s)...\n", driver)

	cfg := &db.Config{
		Driver:   driver,
		DSN:      dsn,
		User:     user,
		Password: password,
	}

	database, err := db.Open(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var versionStr, schemaStr string
	driverName := strings.ToLower(driver)

	if strings.Contains(driverName, "oracle") || strings.Contains(driverName, "ora") {
		// Oracle query
		err = database.SQLDB().QueryRowContext(ctx, "SELECT banner FROM v$version WHERE ROWNUM = 1").Scan(&versionStr)
		if err != nil {
			versionStr = "Oracle (version query fallback: " + err.Error() + ")"
		}
		err = database.SQLDB().QueryRowContext(ctx, "SELECT sys_context('USERENV', 'CURRENT_SCHEMA') FROM dual").Scan(&schemaStr)
		if err != nil {
			schemaStr = "unknown"
		}
	} else {
		// MySQL query
		err = database.SQLDB().QueryRowContext(ctx, "SELECT VERSION()").Scan(&versionStr)
		if err != nil {
			versionStr = "MySQL (version query fallback: " + err.Error() + ")"
		}
		err = database.SQLDB().QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schemaStr)
		if err != nil {
			schemaStr = "unknown"
		}
	}

	fmt.Println("Database connection probe SUCCESSFUL:")
	fmt.Printf("  Driver:  %s\n", cfg.Driver)
	fmt.Printf("  Version: %s\n", strings.TrimSpace(versionStr))
	fmt.Printf("  Schema:  %s\n", strings.TrimSpace(schemaStr))
}
