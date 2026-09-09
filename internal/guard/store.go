package guard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"dash/internal/db"
	"dash/internal/ulid"
)

var (
	ErrNotFound = errors.New("guard: not found")
)

// Store 提供 guard_rules 与 guard_cycles 表的数据库访问方法。
type Store struct {
	db *db.DB
}

// NewStore 创建 Store 实例。
func NewStore(d *db.DB) *Store {
	return &Store{db: d}
}

// DB 返回底层数据库连接。
func (s *Store) DB() *db.DB {
	return s.db
}

// GetRuleByResourceID 根据 cloud_resource_id 获取守卫规则。若不存在返回 nil, nil。
func (s *Store) GetRuleByResourceID(ctx context.Context, resourceID string) (*GuardRule, error) {
	q := `SELECT id, cloud_resource_id, is_enabled, actions_enabled, traffic_limit_gb, traffic_action,
schedule_enabled, schedule_start, schedule_stop, schedule_tz, inherit_account, last_eval_at_ms, last_action, last_action_at_ms,
created_at_ms, updated_at_ms
FROM guard_rules WHERE cloud_resource_id = ?`

	var r GuardRule
	var isEn, actEn, schedEn, inheritAcc int
	var trafficLimit sql.NullFloat64
	var schedStart, schedStop, lastAction sql.NullString
	var lastEval, lastActAt sql.NullInt64

	err := s.db.QueryRow(ctx, q, resourceID).Scan(
		&r.ID, &r.CloudResourceID, &isEn, &actEn, &trafficLimit, &r.TrafficAction,
		&schedEn, &schedStart, &schedStop, &r.ScheduleTZ, &inheritAcc, &lastEval, &lastAction, &lastActAt,
		&r.CreatedAtMs, &r.UpdatedAtMs,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("guard: get rule by resource failed: %w", err)
	}

	r.IsEnabled = isEn == 1
	r.ActionsEnabled = actEn == 1
	r.ScheduleEnabled = schedEn == 1
	r.InheritAccount = inheritAcc == 1
	if trafficLimit.Valid {
		r.TrafficLimitGB = &trafficLimit.Float64
	}
	if schedStart.Valid {
		r.ScheduleStart = &schedStart.String
	}
	if schedStop.Valid {
		r.ScheduleStop = &schedStop.String
	}
	if lastAction.Valid {
		r.LastAction = &lastAction.String
	}
	if lastEval.Valid {
		r.LastEvalAtMs = &lastEval.Int64
	}
	if lastActAt.Valid {
		r.LastActionAtMs = &lastActAt.Int64
	}

	return &r, nil
}

// ListRules 获取全部规则映射，key 为 cloud_resource_id。
func (s *Store) ListRules(ctx context.Context) (map[string]*GuardRule, error) {
	q := `SELECT id, cloud_resource_id, is_enabled, actions_enabled, traffic_limit_gb, traffic_action,
schedule_enabled, schedule_start, schedule_stop, schedule_tz, inherit_account, last_eval_at_ms, last_action, last_action_at_ms,
created_at_ms, updated_at_ms
FROM guard_rules`

	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("guard: list rules failed: %w", err)
	}
	defer rows.Close()

	res := make(map[string]*GuardRule)
	for rows.Next() {
		var r GuardRule
		var isEn, actEn, schedEn, inheritAcc int
		var trafficLimit sql.NullFloat64
		var schedStart, schedStop, lastAction sql.NullString
		var lastEval, lastActAt sql.NullInt64

		if err := rows.Scan(
			&r.ID, &r.CloudResourceID, &isEn, &actEn, &trafficLimit, &r.TrafficAction,
			&schedEn, &schedStart, &schedStop, &r.ScheduleTZ, &inheritAcc, &lastEval, &lastAction, &lastActAt,
			&r.CreatedAtMs, &r.UpdatedAtMs,
		); err != nil {
			return nil, err
		}

		r.IsEnabled = isEn == 1
		r.ActionsEnabled = actEn == 1
		r.ScheduleEnabled = schedEn == 1
		r.InheritAccount = inheritAcc == 1
		if trafficLimit.Valid {
			r.TrafficLimitGB = &trafficLimit.Float64
		}
		if schedStart.Valid {
			r.ScheduleStart = &schedStart.String
		}
		if schedStop.Valid {
			r.ScheduleStop = &schedStop.String
		}
		if lastAction.Valid {
			r.LastAction = &lastAction.String
		}
		if lastEval.Valid {
			r.LastEvalAtMs = &lastEval.Int64
		}
		if lastActAt.Valid {
			r.LastActionAtMs = &lastActAt.Int64
		}

		res[r.CloudResourceID] = &r
	}
	return res, rows.Err()
}

// UpsertRule 插入或更新守卫规则。
func (s *Store) UpsertRule(ctx context.Context, rule *GuardRule) error {
	existing, err := s.GetRuleByResourceID(ctx, rule.CloudResourceID)
	if err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	rule.UpdatedAtMs = now

	isEnInt := 0
	if rule.IsEnabled {
		isEnInt = 1
	}
	actEnInt := 0
	if rule.ActionsEnabled {
		actEnInt = 1
	}
	schedEnInt := 0
	if rule.ScheduleEnabled {
		schedEnInt = 1
	}
	inheritAccInt := 0
	if rule.InheritAccount {
		inheritAccInt = 1
	}
	if rule.TrafficAction == "" {
		rule.TrafficAction = "stop"
	}
	if rule.ScheduleTZ == "" {
		rule.ScheduleTZ = "Asia/Shanghai"
	}

	if existing == nil {
		if rule.ID == "" {
			rule.ID = ulid.New()
		}
		rule.CreatedAtMs = now

		q := `INSERT INTO guard_rules (
id, cloud_resource_id, is_enabled, actions_enabled, traffic_limit_gb, traffic_action,
schedule_enabled, schedule_start, schedule_stop, schedule_tz, inherit_account, last_eval_at_ms, last_action, last_action_at_ms,
created_at_ms, updated_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

		_, err := s.db.Exec(ctx, q,
			rule.ID, rule.CloudResourceID, isEnInt, actEnInt, rule.TrafficLimitGB, rule.TrafficAction,
			schedEnInt, rule.ScheduleStart, rule.ScheduleStop, rule.ScheduleTZ, inheritAccInt, rule.LastEvalAtMs, rule.LastAction, rule.LastActionAtMs,
			rule.CreatedAtMs, rule.UpdatedAtMs,
		)
		if err != nil {
			return fmt.Errorf("guard: insert rule failed: %w", err)
		}
	} else {
		rule.ID = existing.ID
		rule.CreatedAtMs = existing.CreatedAtMs

		q := `UPDATE guard_rules SET
is_enabled = ?, actions_enabled = ?, traffic_limit_gb = ?, traffic_action = ?,
schedule_enabled = ?, schedule_start = ?, schedule_stop = ?, schedule_tz = ?, inherit_account = ?,
updated_at_ms = ?
WHERE id = ?`

		_, err := s.db.Exec(ctx, q,
			isEnInt, actEnInt, rule.TrafficLimitGB, rule.TrafficAction,
			schedEnInt, rule.ScheduleStart, rule.ScheduleStop, rule.ScheduleTZ, inheritAccInt,
			rule.UpdatedAtMs, rule.ID,
		)
		if err != nil {
			return fmt.Errorf("guard: update rule failed: %w", err)
		}
	}

	return nil
}

// UpdateRuleLastAction 更新规则的最新评估与动作记录。
func (s *Store) UpdateRuleLastAction(ctx context.Context, ruleID string, lastAction string, lastActAtMs, evalAtMs int64) error {
	q := `UPDATE guard_rules SET last_action = ?, last_action_at_ms = ?, last_eval_at_ms = ?, updated_at_ms = ? WHERE id = ?`
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(ctx, q, lastAction, lastActAtMs, evalAtMs, now, ruleID)
	return err
}

// UpdateRuleLastEval 更新最新评估时间。
func (s *Store) UpdateRuleLastEval(ctx context.Context, ruleID string, evalAtMs int64) error {
	q := `UPDATE guard_rules SET last_eval_at_ms = ?, updated_at_ms = ? WHERE id = ?`
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(ctx, q, evalAtMs, now, ruleID)
	return err
}

// GetAccountPolicy 获取指定账号的保活策略。若未设置返回 nil, nil。
func (s *Store) GetAccountPolicy(ctx context.Context, accountID string) (*GuardAccountPolicy, error) {
	q := `SELECT cloud_account_id, is_enabled, actions_enabled, traffic_limit_gb, traffic_action,
warn_ratio, schedule_enabled, schedule_start, schedule_stop, schedule_tz, eval_interval_s,
created_at_ms, updated_at_ms
FROM guard_account_policies WHERE cloud_account_id = ?`

	var p GuardAccountPolicy
	var isEn, actEn, schedEn int
	var trafficLimit, warnRatio sql.NullFloat64
	var schedStart, schedStop sql.NullString

	err := s.db.QueryRow(ctx, q, accountID).Scan(
		&p.CloudAccountID, &isEn, &actEn, &trafficLimit, &p.TrafficAction,
		&warnRatio, &schedEn, &schedStart, &schedStop, &p.ScheduleTZ, &p.EvalIntervalS,
		&p.CreatedAtMs, &p.UpdatedAtMs,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("guard: get account policy failed: %w", err)
	}

	p.IsEnabled = isEn == 1
	p.ActionsEnabled = actEn == 1
	p.ScheduleEnabled = schedEn == 1
	if trafficLimit.Valid {
		p.TrafficLimitGB = &trafficLimit.Float64
	}
	if warnRatio.Valid {
		p.WarnRatio = warnRatio.Float64
	} else {
		p.WarnRatio = 0.8
	}
	if schedStart.Valid {
		p.ScheduleStart = &schedStart.String
	}
	if schedStop.Valid {
		p.ScheduleStop = &schedStop.String
	}

	return &p, nil
}

// ListAccountPolicies 获取所有账号的策略映射，key 为 cloud_account_id。
func (s *Store) ListAccountPolicies(ctx context.Context) (map[string]*GuardAccountPolicy, error) {
	q := `SELECT cloud_account_id, is_enabled, actions_enabled, traffic_limit_gb, traffic_action,
warn_ratio, schedule_enabled, schedule_start, schedule_stop, schedule_tz, eval_interval_s,
created_at_ms, updated_at_ms
FROM guard_account_policies`

	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("guard: list account policies failed: %w", err)
	}
	defer rows.Close()

	res := make(map[string]*GuardAccountPolicy)
	for rows.Next() {
		var p GuardAccountPolicy
		var isEn, actEn, schedEn int
		var trafficLimit, warnRatio sql.NullFloat64
		var schedStart, schedStop sql.NullString

		if err := rows.Scan(
			&p.CloudAccountID, &isEn, &actEn, &trafficLimit, &p.TrafficAction,
			&warnRatio, &schedEn, &schedStart, &schedStop, &p.ScheduleTZ, &p.EvalIntervalS,
			&p.CreatedAtMs, &p.UpdatedAtMs,
		); err != nil {
			return nil, err
		}

		p.IsEnabled = isEn == 1
		p.ActionsEnabled = actEn == 1
		p.ScheduleEnabled = schedEn == 1
		if trafficLimit.Valid {
			p.TrafficLimitGB = &trafficLimit.Float64
		}
		if warnRatio.Valid {
			p.WarnRatio = warnRatio.Float64
		} else {
			p.WarnRatio = 0.8
		}
		if schedStart.Valid {
			p.ScheduleStart = &schedStart.String
		}
		if schedStop.Valid {
			p.ScheduleStop = &schedStop.String
		}

		res[p.CloudAccountID] = &p
	}
	return res, rows.Err()
}

// UpsertAccountPolicy 插入或更新账号保活策略。
func (s *Store) UpsertAccountPolicy(ctx context.Context, p *GuardAccountPolicy) error {
	existing, err := s.GetAccountPolicy(ctx, p.CloudAccountID)
	if err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	p.UpdatedAtMs = now

	isEnInt := 0
	if p.IsEnabled {
		isEnInt = 1
	}
	actEnInt := 0
	if p.ActionsEnabled {
		actEnInt = 1
	}
	schedEnInt := 0
	if p.ScheduleEnabled {
		schedEnInt = 1
	}
	if p.TrafficAction == "" {
		p.TrafficAction = "stop"
	}
	if p.WarnRatio <= 0 {
		p.WarnRatio = 0.8
	}
	if p.ScheduleTZ == "" {
		p.ScheduleTZ = "Asia/Shanghai"
	}
	if p.EvalIntervalS <= 0 {
		p.EvalIntervalS = 60
	}

	if existing == nil {
		p.CreatedAtMs = now
		q := `INSERT INTO guard_account_policies (
cloud_account_id, is_enabled, actions_enabled, traffic_limit_gb, traffic_action,
warn_ratio, schedule_enabled, schedule_start, schedule_stop, schedule_tz, eval_interval_s,
created_at_ms, updated_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

		_, err := s.db.Exec(ctx, q,
			p.CloudAccountID, isEnInt, actEnInt, p.TrafficLimitGB, p.TrafficAction,
			p.WarnRatio, schedEnInt, p.ScheduleStart, p.ScheduleStop, p.ScheduleTZ, p.EvalIntervalS,
			p.CreatedAtMs, p.UpdatedAtMs,
		)
		if err != nil {
			return fmt.Errorf("guard: insert account policy failed: %w", err)
		}
	} else {
		p.CreatedAtMs = existing.CreatedAtMs
		q := `UPDATE guard_account_policies SET
is_enabled = ?, actions_enabled = ?, traffic_limit_gb = ?, traffic_action = ?,
warn_ratio = ?, schedule_enabled = ?, schedule_start = ?, schedule_stop = ?,
schedule_tz = ?, eval_interval_s = ?, updated_at_ms = ?
WHERE cloud_account_id = ?`

		_, err := s.db.Exec(ctx, q,
			isEnInt, actEnInt, p.TrafficLimitGB, p.TrafficAction,
			p.WarnRatio, schedEnInt, p.ScheduleStart, p.ScheduleStop,
			p.ScheduleTZ, p.EvalIntervalS, p.UpdatedAtMs, p.CloudAccountID,
		)
		if err != nil {
			return fmt.Errorf("guard: update account policy failed: %w", err)
		}
	}

	return nil
}

// RecordCycle 保存一次守卫周期记录。
func (s *Store) RecordCycle(ctx context.Context, c *GuardCycle) error {
	if c.ID == "" {
		c.ID = ulid.New()
	}
	if c.CreatedAtMs == 0 {
		c.CreatedAtMs = time.Now().UnixMilli()
	}

	q := `INSERT INTO guard_cycles (
id, cloud_account_id, started_at_ms, duration_ms, cdt_used_gb, cdt_error, evaluated, acted, failed, created_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var cdtErrVal any
	if c.CDTError != "" {
		cdtErrVal = c.CDTError
	}

	_, err := s.db.Exec(ctx, q,
		c.ID, c.CloudAccountID, c.StartedAtMs, c.DurationMs, c.CDTUsedGB, cdtErrVal,
		c.Evaluated, c.Acted, c.Failed, c.CreatedAtMs,
	)
	if err != nil {
		return fmt.Errorf("guard: insert cycle failed: %w", err)
	}
	return nil
}

// ListRecentCycles 获取最近的守卫周期列表。
func (s *Store) ListRecentCycles(ctx context.Context, limit, offset int) ([]GuardCycle, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var total int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM guard_cycles`).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("guard: count cycles failed: %w", err)
	}

	q := `SELECT id, cloud_account_id, started_at_ms, duration_ms, cdt_used_gb, cdt_error, evaluated, acted, failed, created_at_ms
FROM guard_cycles ORDER BY started_at_ms DESC`

	rows, err := s.db.QueryPage(ctx, q, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("guard: query cycles failed: %w", err)
	}
	defer rows.Close()

	var cycles []GuardCycle
	for rows.Next() {
		var c GuardCycle
		var cdtGB sql.NullFloat64
		var cdtErr sql.NullString

		if err := rows.Scan(
			&c.ID, &c.CloudAccountID, &c.StartedAtMs, &c.DurationMs, &cdtGB, &cdtErr,
			&c.Evaluated, &c.Acted, &c.Failed, &c.CreatedAtMs,
		); err != nil {
			return nil, 0, err
		}

		if cdtGB.Valid {
			c.CDTUsedGB = &cdtGB.Float64
		}
		if cdtErr.Valid {
			c.CDTError = cdtErr.String
		}
		cycles = append(cycles, c)
	}
	return cycles, total, rows.Err()
}
