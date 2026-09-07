package migrate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"dash/internal/db"
	"dash/internal/logx"
	"dash/migrations"
)

var (
	// File naming: NNNN_<name>.<dialect>.sql, e.g. 0001_init.oracle.sql
	migrationFilePattern = regexp.MustCompile(`^(\d{4})_([a-zA-Z0-9_]+)\.(oracle|mysql)\.sql$`)
	sepPattern           = regexp.MustCompile(`(?m)^--@@\s*$`)
)

// Migration represents a single parsed migration file.
type Migration struct {
	Version  int
	Name     string
	Dialect  string
	FileName string
	Checksum string
	Content  string
}

// Status represents migration state.
type Status struct {
	Version     int
	Name        string
	Applied     bool
	AppliedAtMs int64
	Checksum    string
}

// Migrator runs schema migrations against the database.
type Migrator struct {
	db  *db.DB
	dir string
	fs  fs.FS
}

// New creates a Migrator with a filesystem directory (defaults to "migrations").
func New(d *db.DB, dir string) *Migrator {
	if dir == "" {
		dir = "migrations"
	}
	return &Migrator{
		db:  d,
		dir: dir,
	}
}

// NewWithFS creates a Migrator backed by an fs.FS (e.g. embed.FS).
func NewWithFS(d *db.DB, fsys fs.FS, dir string) *Migrator {
	return &Migrator{
		db:  d,
		fs:  fsys,
		dir: dir,
	}
}

// DefaultMigrator creates a Migrator with filesystem directory if it exists, or embedded migrations.FS.
func DefaultMigrator(d *db.DB, dir string) *Migrator {
	if dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return New(d, dir)
		}
	}
	if fi, err := os.Stat("migrations"); err == nil && fi.IsDir() {
		return New(d, "migrations")
	}
	return NewWithFS(d, migrations.FS, ".")
}

func (m *Migrator) tableExists(ctx context.Context, tableName string) (bool, error) {
	driver := strings.ToLower(m.db.DriverName())
	var q string
	var args []any
	if strings.Contains(driver, "oracle") || strings.Contains(driver, "ora") {
		q = "SELECT COUNT(*) FROM user_tables WHERE table_name = ?"
		args = []any{strings.ToUpper(tableName)}
	} else {
		q = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?"
		args = []any{strings.ToLower(tableName)}
	}
	var count int
	err := m.db.QueryRow(ctx, q, args...).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// loadApplied returns map of version_no -> checksum from schema_migrations.
func (m *Migrator) loadApplied(ctx context.Context) (map[int]Status, error) {
	exists, err := m.tableExists(ctx, "schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("migrate: check schema_migrations exists failed: %w", err)
	}
	if !exists {
		return make(map[int]Status), nil
	}

	rows, err := m.db.Query(ctx, "SELECT version_no, name, checksum, applied_at_ms FROM schema_migrations ORDER BY version_no ASC")
	if err != nil {
		return nil, fmt.Errorf("migrate: failed to query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]Status)
	for rows.Next() {
		var s Status
		s.Applied = true
		if err := rows.Scan(&s.Version, &s.Name, &s.Checksum, &s.AppliedAtMs); err != nil {
			return nil, fmt.Errorf("migrate: failed to scan migration row: %w", err)
		}
		applied[s.Version] = s
	}
	return applied, rows.Err()
}

// discoverMigrations loads and filters migrations matching the current dialect.
func (m *Migrator) discoverMigrations() ([]Migration, error) {
	targetDialect := "oracle"
	driver := strings.ToLower(m.db.DriverName())
	if strings.Contains(driver, "mysql") {
		targetDialect = "mysql"
	}

	var migFiles []Migration

	handleEntry := func(path string, data []byte) error {
		fileName := filepath.Base(path)
		m := migrationFilePattern.FindStringSubmatch(fileName)
		if m == nil {
			return nil
		}
		ver, err := strconv.Atoi(m[1])
		if err != nil {
			return nil
		}
		name := m[2]
		dia := m[3]

		if dia != targetDialect {
			return nil
		}

		sum := fmt.Sprintf("%x", sha256.Sum256(data))
		migFiles = append(migFiles, Migration{
			Version:  ver,
			Name:     fmt.Sprintf("%s_%s", m[1], name),
			Dialect:  dia,
			FileName: fileName,
			Checksum: sum,
			Content:  string(data),
		})
		return nil
	}

	if m.fs != nil {
		err := fs.WalkDir(m.fs, m.dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := fs.ReadFile(m.fs, path)
			if err != nil {
				return err
			}
			return handleEntry(path, data)
		})
		if err != nil {
			return nil, fmt.Errorf("migrate: read fs error: %w", err)
		}
	} else {
		entries, err := os.ReadDir(m.dir)
		if err != nil {
			return nil, fmt.Errorf("migrate: failed to read migrations directory %q: %w", m.dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			path := filepath.Join(m.dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("migrate: failed to read file %q: %w", path, err)
			}
			if err := handleEntry(path, data); err != nil {
				return nil, err
			}
		}
	}

	sort.Slice(migFiles, func(i, j int) bool {
		return migFiles[i].Version < migFiles[j].Version
	})

	return migFiles, nil
}

// SplitStatements splits a SQL migration script into executable single statements.
func SplitStatements(body string) []string {
	// First check if explicit delimiter --@@ is used
	if sepPattern.MatchString(body) {
		rawChunks := sepPattern.Split(body, -1)
		var out []string
		for _, chunk := range rawChunks {
			trimmed := cleanStatement(chunk)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	}

	// Split by semicolons, accounting for quotes and comments
	var statements []string
	var cur strings.Builder
	cur.Grow(len(body))

	inString := false
	inLineComment := false
	inBlockComment := false
	n := len(body)

	for i := 0; i < n; i++ {
		ch := body[i]

		// Handle string literals '...'
		if ch == '\'' {
			cur.WriteByte(ch)
			if inString && i+1 < n && body[i+1] == '\'' {
				// Escaped quote ''
				i++
				cur.WriteByte(body[i])
				continue
			}
			inString = !inString
			continue
		}

		if inString {
			cur.WriteByte(ch)
			continue
		}

		// Handle line comments --
		if !inBlockComment && !inLineComment && ch == '-' && i+1 < n && body[i+1] == '-' {
			inLineComment = true
			cur.WriteString("--")
			i++
			continue
		}
		if inLineComment {
			cur.WriteByte(ch)
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}

		// Handle block comments /* ... */
		if !inLineComment && !inBlockComment && ch == '/' && i+1 < n && body[i+1] == '*' {
			inBlockComment = true
			cur.WriteString("/*")
			i++
			continue
		}
		if inBlockComment {
			cur.WriteByte(ch)
			if ch == '*' && i+1 < n && body[i+1] == '/' {
				inBlockComment = false
				i++
				cur.WriteByte(body[i])
			}
			continue
		}

		// Statement terminator
		if ch == ';' {
			stmt := cleanStatement(cur.String())
			if stmt != "" {
				statements = append(statements, stmt)
			}
			cur.Reset()
			continue
		}

		cur.WriteByte(ch)
	}

	if remaining := cleanStatement(cur.String()); remaining != "" {
		statements = append(statements, remaining)
	}

	return statements
}

func cleanStatement(raw string) string {
	lines := strings.Split(raw, "\n")
	var validLines []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		// Omit pure line comments from statement payload
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		validLines = append(validLines, l)
	}
	res := strings.TrimSpace(strings.Join(validLines, "\n"))
	// Strip trailing semicolon if any
	res = strings.TrimRight(res, "; \t\r\n")
	return res
}

// Up runs all pending migrations in ascending version order.
func (m *Migrator) Up(ctx context.Context) error {
	applied, err := m.loadApplied(ctx)
	if err != nil {
		return err
	}

	migrations, err := m.discoverMigrations()
	if err != nil {
		return err
	}

	// 1. Verify checksums of already applied migrations
	for _, mig := range migrations {
		if appStatus, ok := applied[mig.Version]; ok {
			if appStatus.Checksum != mig.Checksum {
				return fmt.Errorf("migration %s (v%04d) checksum mismatch: recorded=%s, file=%s",
					mig.Name, mig.Version, appStatus.Checksum, mig.Checksum)
			}
		}
	}

	// 2. Apply unapplied migrations
	appliedCount := 0
	for _, mig := range migrations {
		if _, ok := applied[mig.Version]; ok {
			continue
		}

		logx.Info(fmt.Sprintf("Applying migration %s (dialect: %s)...", mig.Name, mig.Dialect))

		statements := SplitStatements(mig.Content)
		for idx, stmt := range statements {
			if _, err := m.db.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("migration %s statement %d failed: %w\nStatement: %s",
					mig.Name, idx+1, err, stmt)
			}
		}

		// Record migration entry
		nowMs := time.Now().UnixMilli()
		insertSQL := "INSERT INTO schema_migrations (version_no, name, checksum, applied_at_ms) VALUES (?, ?, ?, ?)"
		if _, err := m.db.Exec(ctx, insertSQL, mig.Version, mig.Name, mig.Checksum, nowMs); err != nil {
			return fmt.Errorf("failed to record applied migration %s: %w", mig.Name, err)
		}

		logx.Info(fmt.Sprintf("Successfully applied migration %s (v%04d)", mig.Name, mig.Version))
		appliedCount++
	}

	if appliedCount == 0 {
		logx.Info("Database schema is already up to date. No migrations to apply.")
	}

	return nil
}

// Status returns current migration status for all discovered migrations.
func (m *Migrator) Status(ctx context.Context) ([]Status, error) {
	applied, err := m.loadApplied(ctx)
	if err != nil {
		return nil, err
	}
	migrations, err := m.discoverMigrations()
	if err != nil {
		return nil, err
	}

	var list []Status
	for _, mig := range migrations {
		st, ok := applied[mig.Version]
		if ok {
			list = append(list, st)
		} else {
			list = append(list, Status{
				Version:  mig.Version,
				Name:     mig.Name,
				Applied:  false,
				Checksum: mig.Checksum,
			})
		}
	}
	return list, nil
}
