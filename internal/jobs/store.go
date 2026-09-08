package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dash/internal/db"
	"dash/internal/ulid"
)

// Store 提供 jobs 和 job_steps 表的数据库持久化操作。
type Store struct {
	db *db.DB
}

// NewStore 创建 Store 实例。
func NewStore(database *db.DB) *Store {
	return &Store{db: database}
}

// CreateJob 在事务中原子创建 Job 及其初始 Step 记录。
func (s *Store) CreateJob(ctx context.Context, job *Job, steps []*JobStep) error {
	now := time.Now().UnixMilli()
	if job.ID == "" {
		job.ID = ulid.New()
	}
	if job.CreatedAtMs == 0 {
		job.CreatedAtMs = now
	}
	job.UpdatedAtMs = now
	if job.ScheduledAtMs == 0 {
		job.ScheduledAtMs = now
	}
	if job.JobState == "" {
		job.JobState = StatePending
	}
	if job.MaxAttempt <= 0 {
		job.MaxAttempt = 1
	}

	if job.Params != nil && job.ParamsJSON == "" {
		data, err := json.Marshal(job.Params)
		if err == nil {
			job.ParamsJSON = string(data)
		}
	}

	return s.db.WithTx(ctx, func(tx *db.Tx) error {
		qJob := `INSERT INTO jobs (
			id, job_kind, job_state, target_kind, target_id,
			params_json, result_json, error_text,
			attempt, max_attempt, scheduled_at_ms,
			started_at_ms, finished_at_ms, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

		var targetKind, targetID, paramsJSON, resultJSON, errorText any
		if job.TargetKind != "" {
			targetKind = job.TargetKind
		}
		if job.TargetID != "" {
			targetID = job.TargetID
		}
		if job.ParamsJSON != "" {
			paramsJSON = job.ParamsJSON
		}
		if job.ResultJSON != "" {
			resultJSON = job.ResultJSON
		}
		if job.ErrorText != "" {
			errorText = job.ErrorText
		}

		_, err := tx.Exec(ctx, qJob,
			job.ID, job.JobKind, string(job.JobState), targetKind, targetID,
			paramsJSON, resultJSON, errorText,
			job.Attempt, job.MaxAttempt, job.ScheduledAtMs,
			job.StartedAtMs, job.FinishedAtMs, job.CreatedAtMs, job.UpdatedAtMs,
		)
		if err != nil {
			return fmt.Errorf("insert job: %w", err)
		}

		qStep := `INSERT INTO job_steps (
			id, job_id, step_index, name, step_state,
			log_text, started_at_ms, finished_at_ms, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

		for i, st := range steps {
			if st.ID == "" {
				st.ID = ulid.New()
			}
			st.JobID = job.ID
			st.StepIndex = i
			if st.StepState == "" {
				st.StepState = StepPending
			}
			st.CreatedAtMs = now
			st.UpdatedAtMs = now

			var logText any
			if st.LogText != "" {
				logText = st.LogText
			}

			_, err := tx.Exec(ctx, qStep,
				st.ID, st.JobID, st.StepIndex, st.Name, string(st.StepState),
				logText, st.StartedAtMs, st.FinishedAtMs, st.CreatedAtMs, st.UpdatedAtMs,
			)
			if err != nil {
				return fmt.Errorf("insert job_step %d: %w", i, err)
			}
		}

		return nil
	})
}

// GetJob 查询单个 Job 详情及其包含的所有 Steps。
func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) {
	q := `SELECT id, job_kind, job_state, target_kind, target_id,
		params_json, result_json, error_text,
		attempt, max_attempt, scheduled_at_ms,
		started_at_ms, finished_at_ms, created_at_ms, updated_at_ms
	FROM jobs WHERE id = ?`

	var j Job
	var stateStr string
	var targetKind, targetID, paramsJSON, resultJSON, errorText sql.NullString
	var startedAtMs, finishedAtMs sql.NullInt64

	err := s.db.QueryRow(ctx, q, id).Scan(
		&j.ID, &j.JobKind, &stateStr, &targetKind, &targetID,
		&paramsJSON, &resultJSON, &errorText,
		&j.Attempt, &j.MaxAttempt, &j.ScheduledAtMs,
		&startedAtMs, &finishedAtMs, &j.CreatedAtMs, &j.UpdatedAtMs,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get job: %w", err)
	}

	j.JobState = JobState(stateStr)
	if targetKind.Valid {
		j.TargetKind = targetKind.String
	}
	if targetID.Valid {
		j.TargetID = targetID.String
	}
	if paramsJSON.Valid {
		j.ParamsJSON = paramsJSON.String
		_ = json.Unmarshal([]byte(j.ParamsJSON), &j.Params)
	}
	if resultJSON.Valid {
		j.ResultJSON = resultJSON.String
		_ = json.Unmarshal([]byte(j.ResultJSON), &j.Result)
	}
	if errorText.Valid {
		j.ErrorText = errorText.String
	}
	if startedAtMs.Valid {
		v := startedAtMs.Int64
		j.StartedAtMs = &v
	}
	if finishedAtMs.Valid {
		v := finishedAtMs.Int64
		j.FinishedAtMs = &v
	}

	steps, err := s.GetSteps(ctx, id)
	if err != nil {
		return nil, err
	}
	j.Steps = steps

	return &j, nil
}

// GetSteps 查询指定 Job 的所有步骤。
func (s *Store) GetSteps(ctx context.Context, jobID string) ([]*JobStep, error) {
	q := `SELECT id, job_id, step_index, name, step_state,
		log_text, started_at_ms, finished_at_ms, created_at_ms, updated_at_ms
	FROM job_steps WHERE job_id = ? ORDER BY step_index ASC`

	rows, err := s.db.Query(ctx, q, jobID)
	if err != nil {
		return nil, fmt.Errorf("query job steps: %w", err)
	}
	defer rows.Close()

	var steps []*JobStep
	for rows.Next() {
		var st JobStep
		var stateStr string
		var logText sql.NullString
		var startedAtMs, finishedAtMs sql.NullInt64

		if err := rows.Scan(
			&st.ID, &st.JobID, &st.StepIndex, &st.Name, &stateStr,
			&logText, &startedAtMs, &finishedAtMs, &st.CreatedAtMs, &st.UpdatedAtMs,
		); err != nil {
			return nil, fmt.Errorf("scan job step: %w", err)
		}

		st.StepState = StepState(stateStr)
		if logText.Valid {
			st.LogText = logText.String
		}
		if startedAtMs.Valid {
			v := startedAtMs.Int64
			st.StartedAtMs = &v
		}
		if finishedAtMs.Valid {
			v := finishedAtMs.Int64
			st.FinishedAtMs = &v
		}
		steps = append(steps, &st)
	}
	return steps, rows.Err()
}

// ListJobs 分页并条件查询 Job 列表。
func (s *Store) ListJobs(ctx context.Context, f Filter) ([]*Job, int, error) {
	var whereClauses []string
	var args []any

	if f.Kind != "" {
		whereClauses = append(whereClauses, "job_kind = ?")
		args = append(args, f.Kind)
	}
	if f.State != "" {
		whereClauses = append(whereClauses, "job_state = ?")
		args = append(args, string(f.State))
	}
	if f.TargetKind != "" {
		whereClauses = append(whereClauses, "target_kind = ?")
		args = append(args, f.TargetKind)
	}
	if f.TargetID != "" {
		whereClauses = append(whereClauses, "target_id = ?")
		args = append(args, f.TargetID)
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	countSQL := "SELECT COUNT(*) FROM jobs" + whereSQL
	var total int
	if err := s.db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count jobs: %w", err)
	}

	if total == 0 {
		return []*Job{}, 0, nil
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	querySQL := `SELECT id, job_kind, job_state, target_kind, target_id,
		params_json, result_json, error_text,
		attempt, max_attempt, scheduled_at_ms,
		started_at_ms, finished_at_ms, created_at_ms, updated_at_ms
	FROM jobs` + whereSQL + " ORDER BY created_at_ms DESC"

	rows, err := s.db.QueryPage(ctx, querySQL, limit, offset, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query page jobs: %w", err)
	}
	defer rows.Close()

	var jobsList []*Job
	for rows.Next() {
		var j Job
		var stateStr string
		var targetKind, targetID, paramsJSON, resultJSON, errorText sql.NullString
		var startedAtMs, finishedAtMs sql.NullInt64

		if err := rows.Scan(
			&j.ID, &j.JobKind, &stateStr, &targetKind, &targetID,
			&paramsJSON, &resultJSON, &errorText,
			&j.Attempt, &j.MaxAttempt, &j.ScheduledAtMs,
			&startedAtMs, &finishedAtMs, &j.CreatedAtMs, &j.UpdatedAtMs,
		); err != nil {
			return nil, 0, fmt.Errorf("scan job: %w", err)
		}

		j.JobState = JobState(stateStr)
		if targetKind.Valid {
			j.TargetKind = targetKind.String
		}
		if targetID.Valid {
			j.TargetID = targetID.String
		}
		if paramsJSON.Valid {
			j.ParamsJSON = paramsJSON.String
			_ = json.Unmarshal([]byte(j.ParamsJSON), &j.Params)
		}
		if resultJSON.Valid {
			j.ResultJSON = resultJSON.String
			_ = json.Unmarshal([]byte(j.ResultJSON), &j.Result)
		}
		if errorText.Valid {
			j.ErrorText = errorText.String
		}
		if startedAtMs.Valid {
			v := startedAtMs.Int64
			j.StartedAtMs = &v
		}
		if finishedAtMs.Valid {
			v := finishedAtMs.Int64
			j.FinishedAtMs = &v
		}

		jobsList = append(jobsList, &j)
	}

	return jobsList, total, rows.Err()
}

// HasActiveJobOnTarget 检查指定 Target 是否已有未完成的任务（pending 或 running）。
func (s *Store) HasActiveJobOnTarget(ctx context.Context, targetKind, targetID, jobKind string) (bool, error) {
	if targetKind == "" || targetID == "" {
		return false, nil
	}
	q := `SELECT COUNT(*) FROM jobs WHERE target_kind = ? AND target_id = ? AND job_kind = ? AND job_state IN (?, ?)`
	var count int
	err := s.db.QueryRow(ctx, q, targetKind, targetID, jobKind, string(StatePending), string(StateRunning)).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// FindPendingCandidates 查找待执行的任务候选（按 scheduled_at_ms ASC, created_at_ms ASC 排序）。
func (s *Store) FindPendingCandidates(ctx context.Context, maxLimit int) ([]*Job, error) {
	now := time.Now().UnixMilli()
	q := `SELECT id, job_kind, job_state, target_kind, target_id,
		params_json, result_json, error_text,
		attempt, max_attempt, scheduled_at_ms,
		started_at_ms, finished_at_ms, created_at_ms, updated_at_ms
	FROM jobs
	WHERE job_state = ? AND scheduled_at_ms <= ?
	ORDER BY scheduled_at_ms ASC, created_at_ms ASC`

	rows, err := s.db.QueryPage(ctx, q, maxLimit, 0, string(StatePending), now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*Job
	for rows.Next() {
		var j Job
		var stateStr string
		var targetKind, targetID, paramsJSON, resultJSON, errorText sql.NullString
		var startedAtMs, finishedAtMs sql.NullInt64

		if err := rows.Scan(
			&j.ID, &j.JobKind, &stateStr, &targetKind, &targetID,
			&paramsJSON, &resultJSON, &errorText,
			&j.Attempt, &j.MaxAttempt, &j.ScheduledAtMs,
			&startedAtMs, &finishedAtMs, &j.CreatedAtMs, &j.UpdatedAtMs,
		); err != nil {
			return nil, err
		}

		j.JobState = JobState(stateStr)
		if targetKind.Valid {
			j.TargetKind = targetKind.String
		}
		if targetID.Valid {
			j.TargetID = targetID.String
		}
		if paramsJSON.Valid {
			j.ParamsJSON = paramsJSON.String
			_ = json.Unmarshal([]byte(j.ParamsJSON), &j.Params)
		}
		if resultJSON.Valid {
			j.ResultJSON = resultJSON.String
		}
		if errorText.Valid {
			j.ErrorText = errorText.String
		}
		if startedAtMs.Valid {
			v := startedAtMs.Int64
			j.StartedAtMs = &v
		}
		if finishedAtMs.Valid {
			v := finishedAtMs.Int64
			j.FinishedAtMs = &v
		}

		res = append(res, &j)
	}
	return res, rows.Err()
}

// FindRunningJobs 查找所有状态为 running 的任务（用于崩溃恢复）。
func (s *Store) FindRunningJobs(ctx context.Context) ([]*Job, error) {
	q := `SELECT id, job_kind, job_state, target_kind, target_id,
		params_json, result_json, error_text,
		attempt, max_attempt, scheduled_at_ms,
		started_at_ms, finished_at_ms, created_at_ms, updated_at_ms
	FROM jobs
	WHERE job_state = ?
	ORDER BY created_at_ms ASC`

	rows, err := s.db.Query(ctx, q, string(StateRunning))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*Job
	for rows.Next() {
		var j Job
		var stateStr string
		var targetKind, targetID, paramsJSON, resultJSON, errorText sql.NullString
		var startedAtMs, finishedAtMs sql.NullInt64

		if err := rows.Scan(
			&j.ID, &j.JobKind, &stateStr, &targetKind, &targetID,
			&paramsJSON, &resultJSON, &errorText,
			&j.Attempt, &j.MaxAttempt, &j.ScheduledAtMs,
			&startedAtMs, &finishedAtMs, &j.CreatedAtMs, &j.UpdatedAtMs,
		); err != nil {
			return nil, err
		}

		j.JobState = JobState(stateStr)
		if targetKind.Valid {
			j.TargetKind = targetKind.String
		}
		if targetID.Valid {
			j.TargetID = targetID.String
		}
		if paramsJSON.Valid {
			j.ParamsJSON = paramsJSON.String
			_ = json.Unmarshal([]byte(j.ParamsJSON), &j.Params)
		}
		if resultJSON.Valid {
			j.ResultJSON = resultJSON.String
		}
		if errorText.Valid {
			j.ErrorText = errorText.String
		}
		if startedAtMs.Valid {
			v := startedAtMs.Int64
			j.StartedAtMs = &v
		}
		if finishedAtMs.Valid {
			v := finishedAtMs.Int64
			j.FinishedAtMs = &v
		}

		res = append(res, &j)
	}
	return res, rows.Err()
}

// ClaimJob 认领 pending 任务为 running（原子条件更新）。
func (s *Store) ClaimJob(ctx context.Context, jobID string) (*Job, error) {
	now := time.Now().UnixMilli()
	q := `UPDATE jobs
	SET job_state = ?, started_at_ms = ?, attempt = attempt + 1, updated_at_ms = ?
	WHERE id = ? AND job_state = ?`

	res, err := s.db.Exec(ctx, q, string(StateRunning), now, now, jobID, string(StatePending))
	if err != nil {
		return nil, fmt.Errorf("claim job exec: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rowsAffected == 0 {
		return nil, nil // 已被其他 worker 认领或已取消
	}

	return s.GetJob(ctx, jobID)
}

// UpdateJobTerminalState 将任务状态更新为终态（succeeded/failed/cancelled）。
func (s *Store) UpdateJobTerminalState(ctx context.Context, id string, state JobState, resultJSON, errorText string) error {
	now := time.Now().UnixMilli()
	q := `UPDATE jobs
	SET job_state = ?, result_json = ?, error_text = ?, finished_at_ms = ?, updated_at_ms = ?
	WHERE id = ?`

	var rJSON, errTxt any
	if resultJSON != "" {
		rJSON = resultJSON
	}
	if errorText != "" {
		errTxt = errorText
	}

	_, err := s.db.Exec(ctx, q, string(state), rJSON, errTxt, now, now, id)
	return err
}

// UpdateStep 更新单个步骤的状态和时间。
func (s *Store) UpdateStep(ctx context.Context, step *JobStep) error {
	now := time.Now().UnixMilli()
	step.UpdatedAtMs = now
	q := `UPDATE job_steps
	SET step_state = ?, log_text = ?, started_at_ms = ?, finished_at_ms = ?, updated_at_ms = ?
	WHERE id = ?`

	var logTxt any
	if step.LogText != "" {
		logTxt = step.LogText
	}

	_, err := s.db.Exec(ctx, q, string(step.StepState), logTxt, step.StartedAtMs, step.FinishedAtMs, now, step.ID)
	return err
}

// AppendStepLog 追加步骤日志。
func (s *Store) AppendStepLog(ctx context.Context, stepID string, newLog string) error {
	now := time.Now().UnixMilli()
	// 查询现有日志
	var existing sql.NullString
	err := s.db.QueryRow(ctx, "SELECT log_text FROM job_steps WHERE id = ?", stepID).Scan(&existing)
	if err != nil {
		return err
	}

	combined := newLog
	if existing.Valid && existing.String != "" {
		combined = existing.String + "\n" + newLog
	}

	q := `UPDATE job_steps SET log_text = ?, updated_at_ms = ? WHERE id = ?`
	_, err = s.db.Exec(ctx, q, combined, now, stepID)
	return err
}

// CancelJob 将任务置为 cancelled 终态。
func (s *Store) CancelJob(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	q := `UPDATE jobs
	SET job_state = ?, finished_at_ms = ?, updated_at_ms = ?
	WHERE id = ? AND job_state IN (?, ?)`

	_, err := s.db.Exec(ctx, q, string(StateCancelled), now, now, id, string(StatePending), string(StateRunning))
	return err
}

// ResetJobForRetry 将已失败或已取消的任务重置为 pending 状态。
func (s *Store) ResetJobForRetry(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	q := `UPDATE jobs
	SET job_state = ?, error_text = NULL, finished_at_ms = NULL, updated_at_ms = ?
	WHERE id = ? AND job_state IN (?, ?)`

	res, err := s.db.Exec(ctx, q, string(StatePending), now, id, string(StateFailed), string(StateCancelled))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidState
	}
	return nil
}

// ResetInterruptedJob 将崩溃中断的 running 任务重置为 pending 状态。
func (s *Store) ResetInterruptedJob(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	q := `UPDATE jobs
	SET job_state = ?, error_text = NULL, finished_at_ms = NULL, updated_at_ms = ?
	WHERE id = ? AND job_state = ?`

	res, err := s.db.Exec(ctx, q, string(StatePending), now, id, string(StateRunning))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidState
	}
	return nil
}
