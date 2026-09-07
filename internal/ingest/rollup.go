package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"dash/internal/db"
)

// MetricKind represents the rollup aggregation style of a metric column.
type MetricKind int

const (
	// KindGauge expands to _avg, _max, _min (f64)
	KindGauge MetricKind = iota
	// KindCounter expands to _last (i64, using MAX within closed bucket)
	KindCounter
	// KindDelta expands to _sum (i64, using SUM within closed bucket)
	KindDelta
)

// MetricDef defines a source metric in sample_host and its aggregation behavior.
type MetricDef struct {
	Name string
	Kind MetricKind
}

// HostMetricDefs specifies all 17 host metric columns in sample_host,
// strictly conforming to docs/08-field-map.md §1 and docs/10-schema-spec.md §3.
var HostMetricDefs = []MetricDef{
	{Name: "cpu_pct", Kind: KindGauge},
	{Name: "mem_used", Kind: KindGauge},
	{Name: "swap_used", Kind: KindGauge},
	{Name: "load1", Kind: KindGauge},
	{Name: "load5", Kind: KindGauge},
	{Name: "load15", Kind: KindGauge},
	{Name: "disk_used", Kind: KindGauge},
	{Name: "net_up_bps", Kind: KindGauge},
	{Name: "net_down_bps", Kind: KindGauge},
	{Name: "proc_count", Kind: KindGauge},
	{Name: "tcp_count", Kind: KindGauge},
	{Name: "udp_count", Kind: KindGauge},
	{Name: "net_total_up", Kind: KindCounter},
	{Name: "net_total_down", Kind: KindCounter},
	{Name: "uptime_s", Kind: KindCounter},
	{Name: "traffic_up", Kind: KindDelta},
	{Name: "traffic_down", Kind: KindDelta},
}

// ExpandedHostColumns returns the exact 44 columns of sample_host_1m/1h/1d in order:
// 3 fixed + 12*3 gauge + 3 counter + 2 delta = 44 columns.
func ExpandedHostColumns() []string {
	cols := []string{"node_id", "bucket_ms", "sample_cnt"}
	for _, m := range HostMetricDefs {
		switch m.Kind {
		case KindGauge:
			cols = append(cols, m.Name+"_avg", m.Name+"_max", m.Name+"_min")
		case KindCounter:
			cols = append(cols, m.Name+"_last")
		case KindDelta:
			cols = append(cols, m.Name+"_sum")
		}
	}
	return cols
}

// BuildHostRollupSQLRaw generates the SQL for raw sample_host -> sample_host_1m.
func BuildHostRollupSQLRaw(targetTable string, bucketMs int64) string {
	targetCols := strings.Join(ExpandedHostColumns(), ", ")

	var selectItems []string
	selectItems = append(selectItems,
		"node_id",
		fmt.Sprintf("FLOOR(ts_ms / %d) * %d AS bucket_ms", bucketMs, bucketMs),
		"COUNT(*) AS sample_cnt",
	)

	for _, m := range HostMetricDefs {
		switch m.Kind {
		case KindGauge:
			selectItems = append(selectItems,
				fmt.Sprintf("AVG(%s) AS %s_avg", m.Name, m.Name),
				fmt.Sprintf("MAX(%s) AS %s_max", m.Name, m.Name),
				fmt.Sprintf("MIN(%s) AS %s_min", m.Name, m.Name),
			)
		case KindCounter:
			selectItems = append(selectItems, fmt.Sprintf("MAX(%s) AS %s_last", m.Name, m.Name))
		case KindDelta:
			selectItems = append(selectItems, fmt.Sprintf("SUM(%s) AS %s_sum", m.Name, m.Name))
		}
	}

	return fmt.Sprintf(`INSERT INTO %s (%s)
SELECT %s
FROM sample_host
WHERE ts_ms >= ? AND ts_ms < ?
GROUP BY node_id, FLOOR(ts_ms / %d) * %d`,
		targetTable,
		targetCols,
		strings.Join(selectItems, ", "),
		bucketMs, bucketMs,
	)
}

// BuildHostRollupSQLTier generates the SQL for tiered aggregation (1m -> 1h or 1h -> 1d).
// Uses weighted average for gauge metrics: SUM(x_avg * sample_cnt) / SUM(sample_cnt),
// guarded against division by zero (producing NULL when sample_cnt sum is 0 or all values NULL).
func BuildHostRollupSQLTier(sourceTable, targetTable string, bucketMs int64) string {
	targetCols := strings.Join(ExpandedHostColumns(), ", ")

	var selectItems []string
	selectItems = append(selectItems,
		"node_id",
		fmt.Sprintf("FLOOR(bucket_ms / %d) * %d AS bucket_ms", bucketMs, bucketMs),
		"SUM(sample_cnt) AS sample_cnt",
	)

	for _, m := range HostMetricDefs {
		switch m.Kind {
		case KindGauge:
			avgCol := m.Name + "_avg"
			maxCol := m.Name + "_max"
			minCol := m.Name + "_min"
			weightedAvg := fmt.Sprintf(
				"CASE WHEN SUM(CASE WHEN %s IS NOT NULL THEN sample_cnt ELSE 0 END) = 0 THEN NULL ELSE SUM(%s * sample_cnt) / SUM(CASE WHEN %s IS NOT NULL THEN sample_cnt ELSE 0 END) END AS %s",
				avgCol, avgCol, avgCol, avgCol,
			)
			selectItems = append(selectItems,
				weightedAvg,
				fmt.Sprintf("MAX(%s) AS %s", maxCol, maxCol),
				fmt.Sprintf("MIN(%s) AS %s", minCol, minCol),
			)
		case KindCounter:
			lastCol := m.Name + "_last"
			selectItems = append(selectItems, fmt.Sprintf("MAX(%s) AS %s", lastCol, lastCol))
		case KindDelta:
			sumCol := m.Name + "_sum"
			selectItems = append(selectItems, fmt.Sprintf("SUM(%s) AS %s", sumCol, sumCol))
		}
	}

	return fmt.Sprintf(`INSERT INTO %s (%s)
SELECT %s
FROM %s
WHERE bucket_ms >= ? AND bucket_ms < ?
GROUP BY node_id, FLOOR(bucket_ms / %d) * %d`,
		targetTable,
		targetCols,
		strings.Join(selectItems, ", "),
		sourceTable,
		bucketMs, bucketMs,
	)
}

// BuildDimRollupSQLRaw generates SQL for raw sample_dim -> sample_dim_1m.
func BuildDimRollupSQLRaw(targetTable string, bucketMs int64) string {
	return fmt.Sprintf(`INSERT INTO %s (series_id, bucket_ms, sample_cnt, val_avg, val_max, val_min, val_last)
SELECT series_id, FLOOR(ts_ms / %d) * %d, COUNT(*), AVG(val), MAX(val), MIN(val), MAX(val)
FROM sample_dim
WHERE ts_ms >= ? AND ts_ms < ?
GROUP BY series_id, FLOOR(ts_ms / %d) * %d`,
		targetTable, bucketMs, bucketMs, bucketMs, bucketMs,
	)
}

// BuildDimRollupSQLTier generates SQL for tiered sample_dim aggregation (1m -> 1h or 1h -> 1d).
func BuildDimRollupSQLTier(sourceTable, targetTable string, bucketMs int64) string {
	return fmt.Sprintf(`INSERT INTO %s (series_id, bucket_ms, sample_cnt, val_avg, val_max, val_min, val_last)
SELECT series_id, FLOOR(bucket_ms / %d) * %d, SUM(sample_cnt),
       CASE WHEN SUM(CASE WHEN val_avg IS NOT NULL THEN sample_cnt ELSE 0 END) = 0 THEN NULL ELSE SUM(val_avg * sample_cnt) / SUM(CASE WHEN val_avg IS NOT NULL THEN sample_cnt ELSE 0 END) END,
       MAX(val_max), MIN(val_min), MAX(val_last)
FROM %s
WHERE bucket_ms >= ? AND bucket_ms < ?
GROUP BY series_id, FLOOR(bucket_ms / %d) * %d`,
		targetTable, bucketMs, bucketMs, sourceTable, bucketMs, bucketMs,
	)
}

// Pre-compiled SQL statements
var (
	sqlRollup1mHost = BuildHostRollupSQLRaw("sample_host_1m", 60000)
	sqlRollup1hHost = BuildHostRollupSQLTier("sample_host_1m", "sample_host_1h", 3600000)
	sqlRollup1dHost = BuildHostRollupSQLTier("sample_host_1h", "sample_host_1d", 86400000)

	sqlRollup1mDim = BuildDimRollupSQLRaw("sample_dim_1m", 60000)
	sqlRollup1hDim = BuildDimRollupSQLTier("sample_dim_1m", "sample_dim_1h", 3600000)
	sqlRollup1dDim = BuildDimRollupSQLTier("sample_dim_1h", "sample_dim_1d", 86400000)
)

// RollupService manages rollup aggregation across all three tiers.
type RollupService struct {
	db *db.DB
}

// NewRollupService creates a new RollupService.
func NewRollupService(d *db.DB) *RollupService {
	return &RollupService{db: d}
}

// Rollup1m executes 1-minute rollup for [fromMs, toMs).
// Idempotent: deletes existing buckets in [fromMs, toMs) then inserts in a single transaction.
func (s *RollupService) Rollup1m(ctx context.Context, fromMs, toMs int64) error {
	if fromMs >= toMs {
		return nil
	}
	return s.db.WithTx(ctx, func(tx *db.Tx) error {
		// Host rollup
		if _, err := tx.Exec(ctx, "DELETE FROM sample_host_1m WHERE bucket_ms >= ? AND bucket_ms < ?", fromMs, toMs); err != nil {
			return fmt.Errorf("delete sample_host_1m [%d, %d): %w", fromMs, toMs, err)
		}
		if _, err := tx.Exec(ctx, sqlRollup1mHost, fromMs, toMs); err != nil {
			return fmt.Errorf("insert sample_host_1m [%d, %d): %w", fromMs, toMs, err)
		}

		// Dim rollup (best-effort if table exists)
		_, _ = tx.Exec(ctx, "DELETE FROM sample_dim_1m WHERE bucket_ms >= ? AND bucket_ms < ?", fromMs, toMs)
		_, _ = tx.Exec(ctx, sqlRollup1mDim, fromMs, toMs)

		return nil
	})
}

// Rollup1h executes 1-hour rollup for [fromMs, toMs).
// Idempotent: deletes existing buckets in [fromMs, toMs) then inserts in a single transaction.
func (s *RollupService) Rollup1h(ctx context.Context, fromMs, toMs int64) error {
	if fromMs >= toMs {
		return nil
	}
	return s.db.WithTx(ctx, func(tx *db.Tx) error {
		// Host rollup
		if _, err := tx.Exec(ctx, "DELETE FROM sample_host_1h WHERE bucket_ms >= ? AND bucket_ms < ?", fromMs, toMs); err != nil {
			return fmt.Errorf("delete sample_host_1h [%d, %d): %w", fromMs, toMs, err)
		}
		if _, err := tx.Exec(ctx, sqlRollup1hHost, fromMs, toMs); err != nil {
			return fmt.Errorf("insert sample_host_1h [%d, %d): %w", fromMs, toMs, err)
		}

		// Dim rollup
		_, _ = tx.Exec(ctx, "DELETE FROM sample_dim_1h WHERE bucket_ms >= ? AND bucket_ms < ?", fromMs, toMs)
		_, _ = tx.Exec(ctx, sqlRollup1hDim, fromMs, toMs)

		return nil
	})
}

// Rollup1d executes 1-day rollup for [fromMs, toMs).
// Idempotent: deletes existing buckets in [fromMs, toMs) then inserts in a single transaction.
func (s *RollupService) Rollup1d(ctx context.Context, fromMs, toMs int64) error {
	if fromMs >= toMs {
		return nil
	}
	return s.db.WithTx(ctx, func(tx *db.Tx) error {
		// Host rollup
		if _, err := tx.Exec(ctx, "DELETE FROM sample_host_1d WHERE bucket_ms >= ? AND bucket_ms < ?", fromMs, toMs); err != nil {
			return fmt.Errorf("delete sample_host_1d [%d, %d): %w", fromMs, toMs, err)
		}
		if _, err := tx.Exec(ctx, sqlRollup1dHost, fromMs, toMs); err != nil {
			return fmt.Errorf("insert sample_host_1d [%d, %d): %w", fromMs, toMs, err)
		}

		// Dim rollup
		_, _ = tx.Exec(ctx, "DELETE FROM sample_dim_1d WHERE bucket_ms >= ? AND bucket_ms < ?", fromMs, toMs)
		_, _ = tx.Exec(ctx, sqlRollup1dDim, fromMs, toMs)

		return nil
	})
}

// CatchUp scans for missing rollup intervals on startup and processes them sequentially:
// 1. 1m: from MAX(bucket_ms) in sample_host_1m (or MIN(ts_ms) in sample_host) up to latest closed minute, chunked by 1 hour.
// 2. 1h: from MAX(bucket_ms) in sample_host_1h (or MIN(bucket_ms) in sample_host_1m) up to latest closed hour, chunked by 24 hours.
// 3. 1d: from MAX(bucket_ms) in sample_host_1d (or MIN(bucket_ms) in sample_host_1h) up to latest closed day.
func (s *RollupService) CatchUp(ctx context.Context, nowMs int64) error {
	// 1. Catch up 1m
	if err := s.catchUp1m(ctx, nowMs); err != nil {
		return fmt.Errorf("catchUp1m: %w", err)
	}

	// 2. Catch up 1h
	if err := s.catchUp1h(ctx, nowMs); err != nil {
		return fmt.Errorf("catchUp1h: %w", err)
	}

	// 3. Catch up 1d
	if err := s.catchUp1d(ctx, nowMs); err != nil {
		return fmt.Errorf("catchUp1d: %w", err)
	}

	return nil
}

func (s *RollupService) catchUp1m(ctx context.Context, nowMs int64) error {
	var maxBucket sql.NullInt64
	err := s.db.QueryRow(ctx, "SELECT MAX(bucket_ms) FROM sample_host_1m").Scan(&maxBucket)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return err
	}

	var startMs int64
	if maxBucket.Valid {
		startMs = maxBucket.Int64 + 60000
	} else {
		var minRaw sql.NullInt64
		err = s.db.QueryRow(ctx, "SELECT MIN(ts_ms) FROM sample_host").Scan(&minRaw)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return err
		}
		if !minRaw.Valid {
			return nil // No raw data
		}
		startMs = (minRaw.Int64 / 60000) * 60000
	}

	closedMinute := (nowMs / 60000) * 60000
	if startMs >= closedMinute {
		return nil
	}

	// Process in 1-hour chunks (3600000 ms)
	const chunkSize = int64(3600000)
	for cur := startMs; cur < closedMinute; {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		next := cur + chunkSize
		if next > closedMinute {
			next = closedMinute
		}

		if err := s.Rollup1m(ctx, cur, next); err != nil {
			return err
		}
		cur = next
	}

	return nil
}

func (s *RollupService) catchUp1h(ctx context.Context, nowMs int64) error {
	var maxBucket sql.NullInt64
	err := s.db.QueryRow(ctx, "SELECT MAX(bucket_ms) FROM sample_host_1h").Scan(&maxBucket)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return err
	}

	var startMs int64
	if maxBucket.Valid {
		startMs = maxBucket.Int64 + 3600000
	} else {
		var min1m sql.NullInt64
		err = s.db.QueryRow(ctx, "SELECT MIN(bucket_ms) FROM sample_host_1m").Scan(&min1m)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return err
		}
		if !min1m.Valid {
			return nil
		}
		startMs = (min1m.Int64 / 3600000) * 3600000
	}

	closedHour := (nowMs / 3600000) * 3600000
	if startMs >= closedHour {
		return nil
	}

	// Process in 24-hour chunks (86400000 ms)
	const chunkSize = int64(86400000)
	for cur := startMs; cur < closedHour; {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		next := cur + chunkSize
		if next > closedHour {
			next = closedHour
		}

		if err := s.Rollup1h(ctx, cur, next); err != nil {
			return err
		}
		cur = next
	}

	return nil
}

func (s *RollupService) catchUp1d(ctx context.Context, nowMs int64) error {
	var maxBucket sql.NullInt64
	err := s.db.QueryRow(ctx, "SELECT MAX(bucket_ms) FROM sample_host_1d").Scan(&maxBucket)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return err
	}

	var startMs int64
	if maxBucket.Valid {
		startMs = maxBucket.Int64 + 86400000
	} else {
		var min1h sql.NullInt64
		err = s.db.QueryRow(ctx, "SELECT MIN(bucket_ms) FROM sample_host_1h").Scan(&min1h)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return err
		}
		if !min1h.Valid {
			return nil
		}
		startMs = (min1h.Int64 / 86400000) * 86400000
	}

	closedDay := (nowMs / 86400000) * 86400000
	if startMs >= closedDay {
		return nil
	}

	return s.Rollup1d(ctx, startMs, closedDay)
}
