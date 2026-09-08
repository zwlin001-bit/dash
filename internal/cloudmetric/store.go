package cloudmetric

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"dash/internal/db"
	"dash/internal/provider"
)

// Store handles persistence and queries for cloud_samples.
type Store struct {
	db *db.DB
}

// NewStore creates a new cloud metrics store.
func NewStore(d *db.DB) *Store {
	return &Store{db: d}
}

// SavePoints batch upserts time-series points into cloud_samples.
func (s *Store) SavePoints(ctx context.Context, accountID, resRef, metricCode string, points []provider.MetricPoint) error {
	if len(points) == 0 {
		return nil
	}

	upsertSQL := s.db.Dialect().UpsertSQL("cloud_samples",
		[]string{"cloud_account_id", "res_ref", "metric_code", "ts_ms"},
		[]string{"value"},
	)

	for _, pt := range points {
		if _, err := s.db.Exec(ctx, upsertSQL, accountID, resRef, metricCode, pt.TsMs, pt.Value); err != nil {
			return fmt.Errorf("upsert cloud sample point: %w", err)
		}
	}

	return nil
}

// GetLastTs returns the latest ts_ms for a given (cloud_account_id, res_ref, metric_code).
// Returns 0 if no samples exist.
func (s *Store) GetLastTs(ctx context.Context, accountID, resRef, metricCode string) (int64, error) {
	q := "SELECT MAX(ts_ms) FROM cloud_samples WHERE cloud_account_id = ? AND res_ref = ? AND metric_code = ?"
	var maxTs sql.NullInt64
	err := s.db.QueryRow(ctx, q, accountID, resRef, metricCode).Scan(&maxTs)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	if !maxTs.Valid {
		return 0, nil
	}
	return maxTs.Int64, nil
}

// QuerySeries queries cloud_samples for a given resource and metric codes within [fromMs, toMs].
// Returns columnar response matching docs/08-field-map.md §4.
func (s *Store) QuerySeries(ctx context.Context, accountID, resRef string, metricCodes []string, fromMs, toMs int64) (*MetricQueryResponse, error) {
	var conditions []string
	var args []any

	conditions = append(conditions, "cloud_account_id = ?")
	args = append(args, accountID)

	conditions = append(conditions, "res_ref = ?")
	args = append(args, resRef)

	if fromMs > 0 {
		conditions = append(conditions, "ts_ms >= ?")
		args = append(args, fromMs)
	}
	if toMs > 0 {
		conditions = append(conditions, "ts_ms <= ?")
		args = append(args, toMs)
	}

	if len(metricCodes) == 1 {
		conditions = append(conditions, "metric_code = ?")
		args = append(args, metricCodes[0])
	} else if len(metricCodes) > 1 {
		placeholders := make([]string, len(metricCodes))
		for i, mc := range metricCodes {
			placeholders[i] = "?"
			args = append(args, mc)
		}
		conditions = append(conditions, fmt.Sprintf("metric_code IN (%s)", strings.Join(placeholders, ", ")))
	}

	query := fmt.Sprintf("SELECT ts_ms, metric_code, value FROM cloud_samples WHERE %s ORDER BY ts_ms ASC",
		strings.Join(conditions, " AND "))

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query cloud_samples: %w", err)
	}
	defer rows.Close()

	type sampleItem struct {
		ts   int64
		code string
		val  float64
	}

	var items []sampleItem
	uniqueTsMap := make(map[int64]bool)
	allCodesMap := make(map[string]bool)

	for rows.Next() {
		var it sampleItem
		if err := rows.Scan(&it.ts, &it.code, &it.val); err != nil {
			return nil, fmt.Errorf("scan cloud sample: %w", err)
		}
		items = append(items, it)
		uniqueTsMap[it.ts] = true
		allCodesMap[it.code] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var tsList []int64
	for t := range uniqueTsMap {
		tsList = append(tsList, t)
	}
	sort.Slice(tsList, func(i, j int) bool {
		return tsList[i] < tsList[j]
	})

	tsIndex := make(map[int64]int, len(tsList))
	for i, t := range tsList {
		tsIndex[t] = i
	}

	// Ensure all requested metric codes exist in output series map
	seriesMap := make(map[string][]*float64)
	for _, mc := range metricCodes {
		seriesMap[mc] = make([]*float64, len(tsList))
	}
	for mc := range allCodesMap {
		if _, ok := seriesMap[mc]; !ok {
			seriesMap[mc] = make([]*float64, len(tsList))
		}
	}

	for _, it := range items {
		idx, ok := tsIndex[it.ts]
		if !ok {
			continue
		}
		valCopy := it.val
		if sArr, exists := seriesMap[it.code]; exists {
			sArr[idx] = &valCopy
		}
	}

	return &MetricQueryResponse{
		CloudAccountID: accountID,
		ResRef:         resRef,
		Source:         "cloud_samples",
		FromMs:         fromMs,
		ToMs:           toMs,
		StepMs:         300000,
		TsMs:           tsList,
		Series:         seriesMap,
	}, nil
}
