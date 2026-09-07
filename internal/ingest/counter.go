package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"dash/internal/db"
	"dash/internal/logx"
)

type nodeCounterState struct {
	prevUp      *int64
	prevDown    *int64
	hasPrevUp   bool
	hasPrevDown bool
	lastSeenMs  int64
	recovered   bool
}

// CounterManager 管理各节点的网络累计字节计数器，并在机器重启/计数器回绕及服务重启时感知并计算增量。
// 增量计算规格依据 docs/02-database.md §5.7 与 docs/08-field-map.md §1。
type CounterManager struct {
	mu    sync.RWMutex
	nodes map[string]*nodeCounterState
	db    *db.DB
}

// NewCounterManager 创建计数器管理器实例。
func NewCounterManager(database *db.DB) *CounterManager {
	return &CounterManager{
		nodes: make(map[string]*nodeCounterState),
		db:    database,
	}
}

// RecoverAll 进程启动时为每个节点从数据库检索最近一小时内的最近一行累计值作为 prev。
// 超过 1 小时的旧值不可信，当作无 prev 处理。
func (cm *CounterManager) RecoverAll(ctx context.Context, nowMs int64) error {
	if cm.db == nil {
		return nil
	}

	cutoffMs := nowMs - 3600*1000 // 1 小时窗口
	q := `SELECT node_id, net_total_up, net_total_down, ts_ms
	      FROM sample_host
	      WHERE ts_ms >= ?
	      ORDER BY ts_ms DESC`

	rows, err := cm.db.Query(ctx, q, cutoffMs)
	if err != nil {
		return fmt.Errorf("ingest/counter: failed to query sample_host for prev recovery: %w", err)
	}
	defer rows.Close()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	for rows.Next() {
		var nodeID string
		var up, down sql.NullInt64
		var tsMs int64

		if err := rows.Scan(&nodeID, &up, &down, &tsMs); err != nil {
			logx.Warn(fmt.Sprintf("ingest/counter: scan row error during recovery: %v", err))
			continue
		}

		// 按 ts_ms DESC 排序，每个 node_id 首次遇到的行即为最新一行
		state, exists := cm.nodes[nodeID]
		if !exists {
			state = &nodeCounterState{
				lastSeenMs: tsMs,
				recovered:  true,
			}
			if up.Valid {
				v := up.Int64
				state.prevUp = &v
				state.hasPrevUp = true
			}
			if down.Valid {
				v := down.Int64
				state.prevDown = &v
				state.hasPrevDown = true
			}
			cm.nodes[nodeID] = state
		}
	}

	return rows.Err()
}

// RecoverNode 为单个节点从数据库恢复最近一行的累计值（延迟恢复或新节点首次接入兜底）。
func (cm *CounterManager) RecoverNode(ctx context.Context, nodeID string, nowMs int64) error {
	if cm.db == nil {
		return nil
	}

	cutoffMs := nowMs - 3600*1000
	q := `SELECT net_total_up, net_total_down, ts_ms
	      FROM sample_host
	      WHERE node_id = ? AND ts_ms >= ?
	      ORDER BY ts_ms DESC`

	rows, err := cm.db.Query(ctx, q, nodeID, cutoffMs)
	if err != nil {
		return err
	}
	defer rows.Close()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	state, exists := cm.nodes[nodeID]
	if !exists {
		state = &nodeCounterState{}
		cm.nodes[nodeID] = state
	}
	state.recovered = true

	if rows.Next() {
		var up, down sql.NullInt64
		var tsMs int64
		if err := rows.Scan(&up, &down, &tsMs); err == nil {
			state.lastSeenMs = tsMs
			if up.Valid {
				v := up.Int64
				state.prevUp = &v
				state.hasPrevUp = true
			}
			if down.Valid {
				v := down.Int64
				state.prevDown = &v
				state.hasPrevDown = true
			}
		}
	}

	return rows.Err()
}

// CalculateDelta 计算当期报告的 traffic_up 与 traffic_down 增量：
// - 若无 prev（如新机初次上报或超过 1 小时），增量返回 nil (写入数据库为 NULL)，避免产生虚假的巨大增量；
// - 若 cur >= prev，delta = cur - prev；
// - 若 cur < prev（机器重启或计数器归零），delta = cur；
// - 计算后更新内存中该节点的 prev。
func (cm *CounterManager) CalculateDelta(ctx context.Context, nodeID string, tsMs int64, curUp, curDown *int64) (deltaUp, deltaDown *int64) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	state, exists := cm.nodes[nodeID]
	if !exists {
		state = &nodeCounterState{}
		cm.nodes[nodeID] = state
		// 若尚未执行过单节点 recovery 且数据库可用，尝试按需恢复
		if cm.db != nil {
			cm.mu.Unlock()
			_ = cm.RecoverNode(ctx, nodeID, tsMs)
			cm.mu.Lock()
			state = cm.nodes[nodeID]
		}
	}

	// 1. 处理 Upload 流量增量
	if curUp != nil {
		cur := *curUp
		if state.hasPrevUp && state.prevUp != nil {
			var d int64
			if cur >= *state.prevUp {
				d = cur - *state.prevUp
			} else {
				// 机器重启 / 计数器归零重置
				d = cur
			}
			deltaUp = &d
		}
		// 更新 prev
		v := cur
		state.prevUp = &v
		state.hasPrevUp = true
	}

	// 2. 处理 Download 流量增量
	if curDown != nil {
		cur := *curDown
		if state.hasPrevDown && state.prevDown != nil {
			var d int64
			if cur >= *state.prevDown {
				d = cur - *state.prevDown
			} else {
				// 机器重启 / 计数器归零重置
				d = cur
			}
			deltaDown = &d
		}
		// 更新 prev
		v := cur
		state.prevDown = &v
		state.hasPrevDown = true
	}

	state.lastSeenMs = tsMs
	return deltaUp, deltaDown
}
