package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"dash/internal/config"
	"dash/internal/db"
	"dash/internal/logx"
)

func main() {
	var (
		configPath = flag.String("config", "/etc/dash/config.toml", "path to config.toml")
		driverFlag = flag.String("driver", "", "override db driver (oracle | mysql)")
		dsnFlag    = flag.String("dsn", "", "override db dsn")
		userFlag   = flag.String("user", "", "override db user")
		passFlag   = flag.String("password", "", "override db password")
	)
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	if *driverFlag != "" {
		cfg.DB.Driver = *driverFlag
	}
	if *dsnFlag != "" {
		cfg.DB.DSN = *dsnFlag
	}
	if *userFlag != "" {
		cfg.DB.User = *userFlag
	}
	if *passFlag != "" {
		cfg.DB.Password = *passFlag
	}

	fmt.Printf("Probing database connectivity (%s)...\n", cfg.DB.Driver)

	database, err := db.Open(&cfg.DB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %s\n", logx.Redact(err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var versionStr, schemaStr string
	driverName := strings.ToLower(cfg.DB.Driver)

	if strings.Contains(driverName, "oracle") || strings.Contains(driverName, "ora") {
		// Oracle query
		err = database.SQLDB().QueryRowContext(ctx, "SELECT banner FROM v$version WHERE ROWNUM = 1").Scan(&versionStr)
		if err != nil {
			// Fallback if banner fails
			versionStr = "Oracle (version query failed: " + err.Error() + ")"
		}
		err = database.SQLDB().QueryRowContext(ctx, "SELECT sys_context('USERENV', 'CURRENT_SCHEMA') FROM dual").Scan(&schemaStr)
		if err != nil {
			schemaStr = "unknown"
		}
	} else {
		// MySQL query
		err = database.SQLDB().QueryRowContext(ctx, "SELECT VERSION()").Scan(&versionStr)
		if err != nil {
			versionStr = "MySQL (version query failed: " + err.Error() + ")"
		}
		err = database.SQLDB().QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schemaStr)
		if err != nil {
			schemaStr = "unknown"
		}
	}

	fmt.Println("Database connection probe SUCCESSFUL:")
	fmt.Printf("  Driver:  %s\n", cfg.DB.Driver)
	fmt.Printf("  Version: %s\n", strings.TrimSpace(versionStr))
	fmt.Printf("  Schema:  %s\n", strings.TrimSpace(schemaStr))
}
