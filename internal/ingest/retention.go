package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dash/internal/db"
)

// TableRetentionTarget defines a database table and its timestamp column subject to retention policies.
type TableRetentionTarget struct {
	TableName   string
	TimeCol     string
	SettingKey  string
	DefaultDays int
}

// DefaultRetentionTargets lists the retention targets defined in docs/10-schema-spec.md §5.
var DefaultRetentionTargets = []TableRetentionTarget{
	{TableName: "sample_host", TimeCol: "ts_ms", SettingKey: "retention.raw_days", DefaultDays: 3},
	{TableName: "sample_dim", TimeCol: "ts_ms", SettingKey: "retention.raw_days", DefaultDays: 3},
	{TableName: "sample_host_1m", TimeCol: "bucket_ms", SettingKey: "retention.1m_days", DefaultDays: 30},
	{TableName: "sample_dim_1m", TimeCol: "bucket_ms", SettingKey: "retention.1m_days", DefaultDays: 30},
	{TableName: "sample_host_1h", TimeCol: "bucket_ms", SettingKey: "retention.1h_days", DefaultDays: 400},
	{TableName: "sample_dim_1h", TimeCol: "bucket_ms", SettingKey: "retention.1h_days", DefaultDays: 400},
	{TableName: "sample_host_1d", TimeCol: "bucket_ms", SettingKey: "retention.1d_days", DefaultDays: 0},
	{TableName: "sample_dim_1d", TimeCol: "bucket_ms", SettingKey: "retention.1d_days", DefaultDays: 0},
	{TableName: "events", TimeCol: "occurred_at_ms", SettingKey: "retention.events_days", DefaultDays: 90},
	{TableName: "audit_log", TimeCol: "created_at_ms", SettingKey: "retention.audit_days", DefaultDays: 365},
}

// RetentionService manages data retention cleanup.
type RetentionService struct {
	db            *db.DB
	sleepInterval time.Duration
	targets       []TableRetentionTarget
}

// NewRetentionService creates a new RetentionService.
// Defaults to 200ms sleep between chunk deletions as specified in 02-database.md §5.6.
func NewRetentionService(d *db.DB) *RetentionService {
	return &RetentionService{
		db:            d,
		sleepInterval: 200 * time.Millisecond,
		targets:       DefaultRetentionTargets,
	}
}

// SetSleepInterval overrides the sleep interval between chunk deletions (useful for testing).
func (s *RetentionService) SetSleepInterval(d time.Duration) {
	s.sleepInterval = d
}

// SetTargets overrides retention target definitions (useful for testing).
func (s *RetentionService) SetTargets(targets []TableRetentionTarget) {
	s.targets = targets
}

// LoadSettings reads retention policy settings from the database settings table.
func (s *RetentionService) LoadSettings(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Query(ctx, "SELECT setting_key, setting_val FROM settings WHERE setting_key LIKE 'retention.%'")
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return make(map[string]int), nil
		}
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]int)
	for rows.Next() {
		var key string
		var val sql.NullString
		if err := rows.Scan(&key, &val); err != nil {
			return nil, err
		}
		if val.Valid {
			if days, err := strconv.Atoi(strings.TrimSpace(val.String)); err == nil {
				settings[key] = days
			}
		}
	}
	return settings, rows.Err()
}

// Clean executes the retention cleanup across all configured tables for current time nowMs.
// Tables with retention days <= 0 (such as retention.1d_days = 0) represent permanent retention and are skipped.
func (s *RetentionService) Clean(ctx context.Context, nowMs int64) error {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return fmt.Errorf("load retention settings: %w", err)
	}

	for _, target := range s.targets {
		days, ok := settings[target.SettingKey]
		if !ok {
			days = target.DefaultDays
		}

		// 0 or negative indicates permanent retention
		if days <= 0 {
			continue
		}

		cutoffMs := nowMs - int64(days)*86400*1000
		if err := s.CleanTable(ctx, target.TableName, target.TimeCol, cutoffMs); err != nil {
			// If table doesn't exist (e.g. events before migration 0002), skip gracefully
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(errMsg, "doesn't exist") || strings.Contains(errMsg, "table or view does not exist") || strings.Contains(errMsg, "no such table") {
				continue
			}
			return fmt.Errorf("clean table %s: %w", target.TableName, err)
		}
	}

	return nil
}

// CleanTable deletes rows in table older than cutoffMs.
// Adheres to P2 / 02-database.md §5.6:
// - Bounded 1-hour chunk deletion: DELETE FROM <table> WHERE <timeCol> >= ? AND <timeCol> < ?
// - No limit clause used.
// - Advances sequentially from oldest boundary to cutoffMs.
// - Sleeps between rounds to prevent long transactions and undo log bloat.
func (s *RetentionService) CleanTable(ctx context.Context, table, timeCol string, cutoffMs int64) error {
	var oldest sql.NullInt64
	q := fmt.Sprintf("SELECT MIN(%s) FROM %s", timeCol, table)
	err := s.db.QueryRow(ctx, q).Scan(&oldest)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil
		}
		return err
	}

	if !oldest.Valid || oldest.Int64 >= cutoffMs {
		return nil // No rows older than cutoff
	}

	const chunkSize = int64(3600000) // 1 hour
	windowStart := (oldest.Int64 / chunkSize) * chunkSize
	deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE %s >= ? AND %s < ?", table, timeCol, timeCol)

	for windowStart < cutoffMs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		windowEnd := windowStart + chunkSize
		if windowEnd > cutoffMs {
			windowEnd = cutoffMs
		}

		if _, err := s.db.Exec(ctx, deleteSQL, windowStart, windowEnd); err != nil {
			return err
		}

		windowStart = windowEnd

		if s.sleepInterval > 0 {
			time.Sleep(s.sleepInterval)
		}
	}

	return nil
}
