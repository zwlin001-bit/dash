package ingest

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"dash/internal/db"
	"dash/internal/ulid"
)

// SeriesKey 唯一标识一条维度时序 (node_id, metric_code, dim_key)。
type SeriesKey struct {
	NodeID     string
	MetricCode string
	DimKey     string
}

// SeriesManager 维护 metric_series 维度序列的内存映射，并在首次出现时执行数据库 Upsert。
type SeriesManager struct {
	mu    sync.RWMutex
	cache map[SeriesKey]string
	db    *db.DB
}

// NewSeriesManager 创建维度时序管理器实例。
func NewSeriesManager(database *db.DB) *SeriesManager {
	return &SeriesManager{
		cache: make(map[SeriesKey]string),
		db:    database,
	}
}

// GetOrCreate 获取或新建维度序列 ID。
// 缓存在内存中；仅在内存首次缺失时回查/回写数据库，稳态下不产生任何数据库查询。
func (sm *SeriesManager) GetOrCreate(ctx context.Context, nodeID, metricCode, dimKey string, tsMs int64) (string, error) {
	key := SeriesKey{
		NodeID:     nodeID,
		MetricCode: metricCode,
		DimKey:     dimKey,
	}

	// 1. 优先读内存缓存
	sm.mu.RLock()
	id, ok := sm.cache[key]
	sm.mu.RUnlock()
	if ok {
		return id, nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Double-check
	if id, ok = sm.cache[key]; ok {
		return id, nil
	}

	// 2. 内存首次出现，回库查询
	if sm.db != nil {
		q := `SELECT id FROM metric_series WHERE node_id = ? AND metric_code = ? AND dim_key = ?`
		err := sm.db.QueryRow(ctx, q, nodeID, metricCode, dimKey).Scan(&id)
		if err == nil && id != "" {
			sm.cache[key] = id
			return id, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, db.ErrNotFound) {
			// 若由于网络瞬断等原因查库失败，降级生成内存临时 ID，保证采集链路不中断
			id = ulid.New()
			sm.cache[key] = id
			return id, nil
		}

		// 3. 库中不存在：生成 ULID 并 Upsert
		id = ulid.New()
		row := map[string]any{
			"id":            id,
			"node_id":       nodeID,
			"metric_code":   metricCode,
			"dim_key":       dimKey,
			"dim_json":      nil,
			"first_at_ms":   tsMs,
			"last_at_ms":    tsMs,
			"created_at_ms": tsMs,
			"updated_at_ms": tsMs,
		}
		keyCols := []string{"node_id", "metric_code", "dim_key"}
		updCols := []string{"id", "dim_json", "first_at_ms", "last_at_ms", "created_at_ms", "updated_at_ms"}
		_ = sm.db.Upsert(ctx, "metric_series", keyCols, updCols, row)
		sm.cache[key] = id
		return id, nil
	}

	// 无数据库环境（单元测试等）
	id = ulid.New()
	sm.cache[key] = id
	return id, nil
}
