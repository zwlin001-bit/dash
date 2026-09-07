package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	go_ora "github.com/sijms/go-ora/v2"

	"dash/internal/db/dialect"
)

// ErrNotFound aliases sql.ErrNoRows for business code to inspect via errors.Is(err, db.ErrNotFound).
var ErrNotFound = sql.ErrNoRows

// Config defines the database connectivity and connection pool settings.
type Config struct {
	Driver           string        `toml:"driver"`
	User             string        `toml:"user"`
	Password         string        `toml:"password"`
	DSN              string        `toml:"dsn"`
	WalletPath       string        `toml:"wallet_path"`
	MaxOpenConns     int           `toml:"max_open_conns"`
	MaxIdleConns     int           `toml:"max_idle_conns"`
	ConnMaxLifetimeS int           `toml:"conn_max_lifetime_s"`
	ConnMaxLifetime  time.Duration `toml:"-"`
}

// DB wraps standard database/sql DB with dialect-aware rewriting and portability abstractions.
type DB struct {
	db      *sql.DB
	dialect dialect.Dialect
	driver  string
}

// Tx wraps standard database/sql Tx with the same dialect-aware query methods.
type Tx struct {
	tx      *sql.Tx
	dialect dialect.Dialect
}

// Open opens a database connection pool according to cfg, applies defaults, and verifies connectivity.
func Open(cfg *Config) (*DB, error) {
	if cfg == nil {
		return nil, fmt.Errorf("db: config cannot be nil")
	}

	driverName := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driverName == "" {
		driverName = "oracle"
	}

	dia, err := dialect.Get(driverName)
	if err != nil {
		return nil, err
	}

	// Apply connection pool defaults per P1-03 / 02-database.md
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 20
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 10
	}
	maxLifetime := cfg.ConnMaxLifetime
	if maxLifetime <= 0 {
		if cfg.ConnMaxLifetimeS > 0 {
			maxLifetime = time.Duration(cfg.ConnMaxLifetimeS) * time.Second
		} else {
			maxLifetime = 30 * time.Minute
		}
	}

	var connStr string
	var sqlDriver string

	switch driverName {
	case "oracle", "go-ora", "godror", "oci8":
		sqlDriver = "oracle"
		dsn := strings.TrimSpace(cfg.DSN)
		options := make(map[string]string)
		if cfg.WalletPath != "" {
			options["wallet"] = cfg.WalletPath
		}

		if strings.HasPrefix(dsn, "(") {
			// TNS description (e.g. (description=...))
			connStr = go_ora.BuildJDBC(cfg.User, cfg.Password, dsn, options)
		} else if strings.HasPrefix(dsn, "oracle://") {
			connStr = dsn
		} else if dsn != "" {
			// Host or custom string: parse or wrap
			connStr = go_ora.BuildJDBC(cfg.User, cfg.Password, dsn, options)
		} else {
			return nil, fmt.Errorf("db: missing dsn for oracle")
		}

	case "mysql":
		sqlDriver = "mysql"
		dsn := strings.TrimSpace(cfg.DSN)
		if dsn == "" {
			connStr = fmt.Sprintf("%s:%s@tcp(127.0.0.1:3306)/dash?parseTime=true", cfg.User, cfg.Password)
		} else if !strings.Contains(dsn, "@") && cfg.User != "" {
			connStr = fmt.Sprintf("%s:%s@%s", cfg.User, cfg.Password, dsn)
		} else {
			connStr = dsn
		}

	default:
		return nil, fmt.Errorf("db: unsupported driver %q", driverName)
	}

	sqlDB, err := sql.Open(sqlDriver, connStr)
	if err != nil {
		return nil, fmt.Errorf("db open failed: %w", redactError(err, cfg.Password))
	}

	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(maxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db ping failed: %w", redactError(err, cfg.Password))
	}

	return &DB{
		db:      sqlDB,
		dialect: dia,
		driver:  driverName,
	}, nil
}

// New creates a DB wrapper from an existing *sql.DB and dialect driver name.
func New(sqlDB *sql.DB, driverName string) (*DB, error) {
	if sqlDB == nil {
		return nil, fmt.Errorf("db: sqlDB cannot be nil")
	}
	dia, err := dialect.Get(driverName)
	if err != nil {
		return nil, err
	}
	return &DB{
		db:      sqlDB,
		dialect: dia,
		driver:  driverName,
	}, nil
}

// Query executes a query returning rows, rewriting '?' placeholders according to the current dialect.
func (d *DB) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	rebound := d.dialect.Rebind(q)
	return d.db.QueryContext(ctx, rebound, args...)
}

// QueryRow executes a query returning a single row, rewriting '?' placeholders.
func (d *DB) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	rebound := d.dialect.Rebind(q)
	return d.db.QueryRowContext(ctx, rebound, args...)
}

// Exec executes a query without returning rows, rewriting '?' placeholders.
func (d *DB) Exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	rebound := d.dialect.Rebind(q)
	return d.db.ExecContext(ctx, rebound, args...)
}

// QueryPage paginates the query q using the dialect-specific pagination clause.
func (d *DB) QueryPage(ctx context.Context, q string, limit, offset int, args ...any) (*sql.Rows, error) {
	pagedSQL, pageArgs := d.dialect.Paginate(q, limit, offset)
	allArgs := append(args, pageArgs...)
	return d.Query(ctx, pagedSQL, allArgs...)
}

// WithTx runs fn within a transaction. If fn returns an error or panics, the transaction is rolled back.
func (d *DB) WithTx(ctx context.Context, fn func(*Tx) error) (err error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	wrappedTx := &Tx{
		tx:      tx,
		dialect: d.dialect,
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = fn(wrappedTx); err != nil {
		return err
	}
	return tx.Commit()
}

// Close closes the underlying connection pool.
func (d *DB) Close() error {
	return d.db.Close()
}

// Ping verifies connectivity to the database.
func (d *DB) Ping(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

// DriverName returns the configured database driver name.
func (d *DB) DriverName() string {
	return d.driver
}

// Dialect returns the active dialect implementation.
func (d *DB) Dialect() dialect.Dialect {
	return d.dialect
}

// SQLDB returns the underlying *sql.DB instance.
func (d *DB) SQLDB() *sql.DB {
	return d.db
}

// Query executes a query on transaction, rewriting '?' placeholders.
func (tx *Tx) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	rebound := tx.dialect.Rebind(q)
	return tx.tx.QueryContext(ctx, rebound, args...)
}

// QueryRow executes a query on transaction returning a single row, rewriting '?' placeholders.
func (tx *Tx) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	rebound := tx.dialect.Rebind(q)
	return tx.tx.QueryRowContext(ctx, rebound, args...)
}

// Exec executes a query on transaction without returning rows, rewriting '?' placeholders.
func (tx *Tx) Exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	rebound := tx.dialect.Rebind(q)
	return tx.tx.ExecContext(ctx, rebound, args...)
}

// QueryPage paginates the query q on transaction using the dialect-specific pagination clause.
func (tx *Tx) QueryPage(ctx context.Context, q string, limit, offset int, args ...any) (*sql.Rows, error) {
	pagedSQL, pageArgs := tx.dialect.Paginate(q, limit, offset)
	allArgs := append(args, pageArgs...)
	return tx.Query(ctx, pagedSQL, allArgs...)
}

var (
	reURIPassword = regexp.MustCompile(`(?i)(://[^:]+:)([^@]+)(@)`)
	reKVPassword  = regexp.MustCompile(`(?i)(password\s*=\s*)([^;\s&]+)`)
)

func redactError(err error, password string) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	if password != "" {
		s = strings.ReplaceAll(s, password, "***")
	}
	s = reURIPassword.ReplaceAllString(s, "${1}***${3}")
	s = reKVPassword.ReplaceAllString(s, "${1}***")
	return errors.New(s)
}
