package alert

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"dash/internal/db"
	"dash/internal/ulid"
)

// Store handles database persistence for alert rules, rule-channel mappings, and alert events.
type Store struct {
	db *db.DB
}

// NewStore creates a new alert Store.
func NewStore(database *db.DB) *Store {
	return &Store{db: database}
}

// DB returns the underlying database instance.
func (s *Store) DB() *db.DB {
	return s.db
}

// CreateRule inserts a new alert rule and its associated channels.
func (s *Store) CreateRule(ctx context.Context, r *AlertRule) error {
	if s.db == nil {
		return errors.New("alert: database is nil")
	}
	if r.ID == "" {
		r.ID = ulid.New()
	}
	nowMs := time.Now().UnixMilli()
	if r.CreatedAtMs == 0 {
		r.CreatedAtMs = nowMs
	}
	r.UpdatedAtMs = nowMs

	if r.Severity == "" {
		r.Severity = SeverityWarning
	}
	if r.CompareOp == "" {
		r.CompareOp = CompareOpGT
	}
	if r.ScopeKind == "" {
		r.ScopeKind = ScopeKindAll
	}

	autoRenewInt := 0
	if r.IncludeAutoRenew {
		autoRenewInt = 1
	}

	enabledInt := 0
	if r.IsEnabled {
		enabledInt = 1
	}

	var scopeRefVal, metricCodeVal, extraJSONVal any
	if r.ScopeRef != "" {
		scopeRefVal = r.ScopeRef
	}
	if r.MetricCode != "" {
		metricCodeVal = r.MetricCode
	}
	if r.ExtraJSON != "" {
		extraJSONVal = r.ExtraJSON
	}

	q := `INSERT INTO alert_rules (
		id, name, is_enabled, rule_kind, scope_kind, scope_ref,
		metric_code, compare_op, threshold, duration_s, severity, silence_s,
		include_auto_renew, extra_json, created_at_ms, updated_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(ctx, q,
		r.ID, r.Name, enabledInt, r.RuleKind, r.ScopeKind, scopeRefVal,
		metricCodeVal, r.CompareOp, r.Threshold, r.DurationS, r.Severity, r.SilenceS,
		autoRenewInt, extraJSONVal, r.CreatedAtMs, r.UpdatedAtMs,
	)
	if err != nil {
		return fmt.Errorf("alert: insert rule failed: %w", err)
	}

	if len(r.ChannelIDs) > 0 {
		if err := s.SetRuleChannels(ctx, r.ID, r.ChannelIDs); err != nil {
			return err
		}
	}

	return nil
}

// GetRule retrieves a rule by ID including its channels.
func (s *Store) GetRule(ctx context.Context, id string) (*AlertRule, error) {
	if s.db == nil {
		return nil, errors.New("alert: database is nil")
	}

	q := `SELECT id, name, is_enabled, rule_kind, scope_kind, scope_ref,
		metric_code, compare_op, threshold, duration_s, severity, silence_s,
		include_auto_renew, extra_json, created_at_ms, updated_at_ms
		FROM alert_rules WHERE id = ?`

	var r AlertRule
	var enabledInt, autoRenewInt int
	var scopeRef, metricCode, extraJSON sql.NullString

	row := s.db.QueryRow(ctx, q, id)
	err := row.Scan(
		&r.ID, &r.Name, &enabledInt, &r.RuleKind, &r.ScopeKind, &scopeRef,
		&metricCode, &r.CompareOp, &r.Threshold, &r.DurationS, &r.Severity, &r.SilenceS,
		&autoRenewInt, &extraJSON, &r.CreatedAtMs, &r.UpdatedAtMs,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, fmt.Errorf("alert: query rule failed: %w", err)
	}

	r.IsEnabled = (enabledInt == 1)
	r.IncludeAutoRenew = (autoRenewInt == 1)
	if scopeRef.Valid {
		r.ScopeRef = scopeRef.String
	}
	if metricCode.Valid {
		r.MetricCode = metricCode.String
	}
	if extraJSON.Valid {
		r.ExtraJSON = extraJSON.String
	}

	channels, err := s.GetRuleChannels(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	r.ChannelIDs = channels

	return &r, nil
}

// UpdateRule modifies an existing alert rule.
func (s *Store) UpdateRule(ctx context.Context, r *AlertRule) error {
	if s.db == nil {
		return errors.New("alert: database is nil")
	}

	nowMs := time.Now().UnixMilli()
	r.UpdatedAtMs = nowMs

	enabledInt := 0
	if r.IsEnabled {
		enabledInt = 1
	}
	autoRenewInt := 0
	if r.IncludeAutoRenew {
		autoRenewInt = 1
	}

	var scopeRefVal, metricCodeVal, extraJSONVal any
	if r.ScopeRef != "" {
		scopeRefVal = r.ScopeRef
	}
	if r.MetricCode != "" {
		metricCodeVal = r.MetricCode
	}
	if r.ExtraJSON != "" {
		extraJSONVal = r.ExtraJSON
	}

	q := `UPDATE alert_rules SET
		name = ?, is_enabled = ?, rule_kind = ?, scope_kind = ?, scope_ref = ?,
		metric_code = ?, compare_op = ?, threshold = ?, duration_s = ?, severity = ?,
		silence_s = ?, include_auto_renew = ?, extra_json = ?, updated_at_ms = ?
		WHERE id = ?`

	res, err := s.db.Exec(ctx, q,
		r.Name, enabledInt, r.RuleKind, r.ScopeKind, scopeRefVal,
		metricCodeVal, r.CompareOp, r.Threshold, r.DurationS, r.Severity,
		r.SilenceS, autoRenewInt, extraJSONVal, r.UpdatedAtMs, r.ID,
	)
	if err != nil {
		return fmt.Errorf("alert: update rule failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return db.ErrNotFound
	}

	return s.SetRuleChannels(ctx, r.ID, r.ChannelIDs)
}

// DeleteRule deletes an alert rule, its channel mappings, and its events.
func (s *Store) DeleteRule(ctx context.Context, id string) error {
	if s.db == nil {
		return errors.New("alert: database is nil")
	}

	_, _ = s.db.Exec(ctx, "DELETE FROM alert_rule_channels WHERE alert_rule_id = ?", id)
	_, _ = s.db.Exec(ctx, "DELETE FROM alert_events WHERE alert_rule_id = ?", id)

	res, err := s.db.Exec(ctx, "DELETE FROM alert_rules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("alert: delete rule failed: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return db.ErrNotFound
	}
	return nil
}

// ListRules returns all configured alert rules with their channel bindings.
func (s *Store) ListRules(ctx context.Context) ([]*AlertRule, error) {
	if s.db == nil {
		return nil, errors.New("alert: database is nil")
	}

	q := `SELECT id, name, is_enabled, rule_kind, scope_kind, scope_ref,
		metric_code, compare_op, threshold, duration_s, severity, silence_s,
		include_auto_renew, extra_json, created_at_ms, updated_at_ms
		FROM alert_rules ORDER BY created_at_ms ASC`

	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("alert: list rules failed: %w", err)
	}
	defer rows.Close()

	var rules []*AlertRule
	for rows.Next() {
		var r AlertRule
		var enabledInt, autoRenewInt int
		var scopeRef, metricCode, extraJSON sql.NullString

		err := rows.Scan(
			&r.ID, &r.Name, &enabledInt, &r.RuleKind, &r.ScopeKind, &scopeRef,
			&metricCode, &r.CompareOp, &r.Threshold, &r.DurationS, &r.Severity, &r.SilenceS,
			&autoRenewInt, &extraJSON, &r.CreatedAtMs, &r.UpdatedAtMs,
		)
		if err != nil {
			return nil, fmt.Errorf("alert: scan rule failed: %w", err)
		}

		r.IsEnabled = (enabledInt == 1)
		r.IncludeAutoRenew = (autoRenewInt == 1)
		if scopeRef.Valid {
			r.ScopeRef = scopeRef.String
		}
		if metricCode.Valid {
			r.MetricCode = metricCode.String
		}
		if extraJSON.Valid {
			r.ExtraJSON = extraJSON.String
		}
		rules = append(rules, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Populate channels for all rules
	for _, r := range rules {
		channels, err := s.GetRuleChannels(ctx, r.ID)
		if err == nil {
			r.ChannelIDs = channels
		}
	}

	return rules, nil
}

// GetRulesForKind retrieves all enabled rules for a specific rule kind.
func (s *Store) GetRulesForKind(ctx context.Context, ruleKind string) ([]*AlertRule, error) {
	if s.db == nil {
		return nil, errors.New("alert: database is nil")
	}

	q := `SELECT id, name, is_enabled, rule_kind, scope_kind, scope_ref,
		metric_code, compare_op, threshold, duration_s, severity, silence_s,
		include_auto_renew, extra_json, created_at_ms, updated_at_ms
		FROM alert_rules WHERE rule_kind = ? AND is_enabled = 1 ORDER BY created_at_ms ASC`

	rows, err := s.db.Query(ctx, q, ruleKind)
	if err != nil {
		return nil, fmt.Errorf("alert: get rules for kind %s failed: %w", ruleKind, err)
	}
	defer rows.Close()

	var rules []*AlertRule
	for rows.Next() {
		var r AlertRule
		var enabledInt, autoRenewInt int
		var scopeRef, metricCode, extraJSON sql.NullString

		err := rows.Scan(
			&r.ID, &r.Name, &enabledInt, &r.RuleKind, &r.ScopeKind, &scopeRef,
			&metricCode, &r.CompareOp, &r.Threshold, &r.DurationS, &r.Severity, &r.SilenceS,
			&autoRenewInt, &extraJSON, &r.CreatedAtMs, &r.UpdatedAtMs,
		)
		if err != nil {
			return nil, fmt.Errorf("alert: scan rule failed: %w", err)
		}

		r.IsEnabled = (enabledInt == 1)
		r.IncludeAutoRenew = (autoRenewInt == 1)
		if scopeRef.Valid {
			r.ScopeRef = scopeRef.String
		}
		if metricCode.Valid {
			r.MetricCode = metricCode.String
		}
		if extraJSON.Valid {
			r.ExtraJSON = extraJSON.String
		}
		rules = append(rules, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, r := range rules {
		channels, err := s.GetRuleChannels(ctx, r.ID)
		if err == nil {
			r.ChannelIDs = channels
		}
	}

	return rules, nil
}

// SetRuleChannels updates the channel bindings for an alert rule.
func (s *Store) SetRuleChannels(ctx context.Context, ruleID string, channelIDs []string) error {
	if s.db == nil {
		return errors.New("alert: database is nil")
	}

	// Delete existing bindings
	_, err := s.db.Exec(ctx, "DELETE FROM alert_rule_channels WHERE alert_rule_id = ?", ruleID)
	if err != nil {
		return fmt.Errorf("alert: clear rule channels failed: %w", err)
	}

	// Insert new bindings
	for _, cid := range channelIDs {
		cid = strings.TrimSpace(cid)
		if cid == "" {
			continue
		}
		_, err := s.db.Exec(ctx, "INSERT INTO alert_rule_channels (alert_rule_id, notify_channel_id) VALUES (?, ?)", ruleID, cid)
		if err != nil {
			return fmt.Errorf("alert: insert rule channel failed: %w", err)
		}
	}
	return nil
}

// GetRuleChannels returns channel IDs bound to an alert rule.
func (s *Store) GetRuleChannels(ctx context.Context, ruleID string) ([]string, error) {
	if s.db == nil {
		return nil, errors.New("alert: database is nil")
	}

	q := "SELECT notify_channel_id FROM alert_rule_channels WHERE alert_rule_id = ?"
	rows, err := s.db.Query(ctx, q, ruleID)
	if err != nil {
		return nil, fmt.Errorf("alert: query rule channels failed: %w", err)
	}
	defer rows.Close()

	var channels []string
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			return nil, err
		}
		channels = append(channels, cid)
	}
	return channels, rows.Err()
}

// Scope Resolution: Get applicable node IDs for a rule
func (s *Store) GetRuleNodeIDs(ctx context.Context, r *AlertRule) ([]string, error) {
	if s.db == nil {
		return nil, errors.New("alert: database is nil")
	}

	switch r.ScopeKind {
	case ScopeKindNode:
		if r.ScopeRef == "" {
			return nil, nil
		}
		return []string{r.ScopeRef}, nil

	case ScopeKindGroup:
		if r.ScopeRef == "" {
			return nil, nil
		}
		rows, err := s.db.Query(ctx, "SELECT id FROM nodes WHERE node_group_id = ? ORDER BY id ASC", r.ScopeRef)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err == nil {
				ids = append(ids, id)
			}
		}
		return ids, rows.Err()

	case ScopeKindTag:
		if r.ScopeRef == "" {
			return nil, nil
		}
		// Match either tag_id or tag_name
		q := `SELECT nt.node_id FROM node_tags nt
			JOIN tags t ON nt.tag_id = t.id
			WHERE t.id = ? OR t.name = ?
			ORDER BY nt.node_id ASC`
		rows, err := s.db.Query(ctx, q, r.ScopeRef, r.ScopeRef)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err == nil {
				ids = append(ids, id)
			}
		}
		return ids, rows.Err()

	case ScopeKindAll:
		fallthrough
	default:
		rows, err := s.db.Query(ctx, "SELECT id FROM nodes ORDER BY id ASC")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err == nil {
				ids = append(ids, id)
			}
		}
		return ids, rows.Err()
	}
}

// AlertEvent operations

// CreateEvent persists a new alert event record.
func (s *Store) CreateEvent(ctx context.Context, ev *AlertEvent) error {
	if s.db == nil {
		return errors.New("alert: database is nil")
	}
	if ev.ID == "" {
		ev.ID = ulid.New()
	}
	nowMs := time.Now().UnixMilli()
	if ev.FiredAtMs == 0 {
		ev.FiredAtMs = nowMs
	}
	if ev.CreatedAtMs == 0 {
		ev.CreatedAtMs = nowMs
	}
	ev.UpdatedAtMs = nowMs

	var peakVal, resolvedVal, notifiedVal, detailVal any
	if ev.PeakValue != nil {
		peakVal = *ev.PeakValue
	}
	if ev.ResolvedAtMs != nil {
		resolvedVal = *ev.ResolvedAtMs
	}
	if ev.NotifiedAtMs != nil {
		notifiedVal = *ev.NotifiedAtMs
	}
	if ev.Detail != "" {
		detailVal = ev.Detail
	}

	q := `INSERT INTO alert_events (
		id, alert_rule_id, node_id, event_state, fired_at_ms, resolved_at_ms,
		peak_value, detail, notified_at_ms, created_at_ms, updated_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(ctx, q,
		ev.ID, ev.AlertRuleID, ev.NodeID, ev.EventState, ev.FiredAtMs, resolvedVal,
		peakVal, detailVal, notifiedVal, ev.CreatedAtMs, ev.UpdatedAtMs,
	)
	if err != nil {
		return fmt.Errorf("alert: insert event failed: %w", err)
	}
	return nil
}

// UpdateEvent updates an existing alert event (e.g. peak_value or notified_at_ms).
func (s *Store) UpdateEvent(ctx context.Context, ev *AlertEvent) error {
	if s.db == nil {
		return errors.New("alert: database is nil")
	}
	ev.UpdatedAtMs = time.Now().UnixMilli()

	var peakVal, resolvedVal, notifiedVal, detailVal any
	if ev.PeakValue != nil {
		peakVal = *ev.PeakValue
	}
	if ev.ResolvedAtMs != nil {
		resolvedVal = *ev.ResolvedAtMs
	}
	if ev.NotifiedAtMs != nil {
		notifiedVal = *ev.NotifiedAtMs
	}
	if ev.Detail != "" {
		detailVal = ev.Detail
	}

	q := `UPDATE alert_events SET
		event_state = ?, resolved_at_ms = ?, peak_value = ?, detail = ?,
		notified_at_ms = ?, updated_at_ms = ?
		WHERE id = ?`

	_, err := s.db.Exec(ctx, q,
		ev.EventState, resolvedVal, peakVal, detailVal, notifiedVal, ev.UpdatedAtMs, ev.ID,
	)
	return err
}

// ResolveEvent transitions an active event to resolved state.
func (s *Store) ResolveEvent(ctx context.Context, eventID string, resolvedAtMs int64) error {
	if s.db == nil {
		return errors.New("alert: database is nil")
	}
	nowMs := time.Now().UnixMilli()
	if resolvedAtMs == 0 {
		resolvedAtMs = nowMs
	}

	q := `UPDATE alert_events SET event_state = ?, resolved_at_ms = ?, updated_at_ms = ? WHERE id = ?`
	_, err := s.db.Exec(ctx, q, EventStateResolved, resolvedAtMs, nowMs, eventID)
	return err
}

// GetEvent fetches a single alert event by ID.
func (s *Store) GetEvent(ctx context.Context, id string) (*AlertEvent, error) {
	if s.db == nil {
		return nil, errors.New("alert: database is nil")
	}

	q := `SELECT e.id, e.alert_rule_id, e.node_id, e.event_state, e.fired_at_ms,
		e.resolved_at_ms, e.peak_value, e.detail, e.notified_at_ms, e.created_at_ms, e.updated_at_ms,
		r.name, n.name, r.rule_kind, r.severity
		FROM alert_events e
		LEFT JOIN alert_rules r ON e.alert_rule_id = r.id
		LEFT JOIN nodes n ON e.node_id = n.id
		WHERE e.id = ?`

	var ev AlertEvent
	var resMs, notMs sql.NullInt64
	var peak sql.NullFloat64
	var detail, ruleName, nodeName, ruleKind, severity sql.NullString

	row := s.db.QueryRow(ctx, q, id)
	err := row.Scan(
		&ev.ID, &ev.AlertRuleID, &ev.NodeID, &ev.EventState, &ev.FiredAtMs,
		&resMs, &peak, &detail, &notMs, &ev.CreatedAtMs, &ev.UpdatedAtMs,
		&ruleName, &nodeName, &ruleKind, &severity,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, fmt.Errorf("alert: query event failed: %w", err)
	}

	if resMs.Valid {
		ev.ResolvedAtMs = &resMs.Int64
	}
	if notMs.Valid {
		ev.NotifiedAtMs = &notMs.Int64
	}
	if peak.Valid {
		ev.PeakValue = &peak.Float64
	}
	if detail.Valid {
		ev.Detail = detail.String
	}
	if ruleName.Valid {
		ev.RuleName = ruleName.String
	}
	if nodeName.Valid {
		ev.NodeName = nodeName.String
	}
	if ruleKind.Valid {
		ev.RuleKind = ruleKind.String
	}
	if severity.Valid {
		ev.Severity = severity.String
	}

	return &ev, nil
}

// GetActiveFiringEvents retrieves all events currently in 'firing' state across the system.
// This is essential on process startup to restore state without generating duplicate alerts (07-monitoring.md §2.2).
func (s *Store) GetActiveFiringEvents(ctx context.Context) ([]*AlertEvent, error) {
	if s.db == nil {
		return nil, errors.New("alert: database is nil")
	}

	q := `SELECT e.id, e.alert_rule_id, e.node_id, e.event_state, e.fired_at_ms,
		e.resolved_at_ms, e.peak_value, e.detail, e.notified_at_ms, e.created_at_ms, e.updated_at_ms,
		r.name, n.name, r.rule_kind, r.severity
		FROM alert_events e
		LEFT JOIN alert_rules r ON e.alert_rule_id = r.id
		LEFT JOIN nodes n ON e.node_id = n.id
		WHERE e.event_state = 'firing'
		ORDER BY e.fired_at_ms DESC`

	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("alert: query active firing events failed: %w", err)
	}
	defer rows.Close()

	var events []*AlertEvent
	for rows.Next() {
		var ev AlertEvent
		var resMs, notMs sql.NullInt64
		var peak sql.NullFloat64
		var detail, ruleName, nodeName, ruleKind, severity sql.NullString

		err := rows.Scan(
			&ev.ID, &ev.AlertRuleID, &ev.NodeID, &ev.EventState, &ev.FiredAtMs,
			&resMs, &peak, &detail, &notMs, &ev.CreatedAtMs, &ev.UpdatedAtMs,
			&ruleName, &nodeName, &ruleKind, &severity,
		)
		if err != nil {
			return nil, fmt.Errorf("alert: scan active event failed: %w", err)
		}

		if resMs.Valid {
			ev.ResolvedAtMs = &resMs.Int64
		}
		if notMs.Valid {
			ev.NotifiedAtMs = &notMs.Int64
		}
		if peak.Valid {
			ev.PeakValue = &peak.Float64
		}
		if detail.Valid {
			ev.Detail = detail.String
		}
		if ruleName.Valid {
			ev.RuleName = ruleName.String
		}
		if nodeName.Valid {
			ev.NodeName = nodeName.String
		}
		if ruleKind.Valid {
			ev.RuleKind = ruleKind.String
		}
		if severity.Valid {
			ev.Severity = severity.String
		}

		events = append(events, &ev)
	}
	return events, rows.Err()
}

// ListEvents queries alert history with filtering and pagination.
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]*AlertEvent, int, error) {
	if s.db == nil {
		return nil, 0, errors.New("alert: database is nil")
	}

	var where []string
	var args []any

	if f.RuleID != "" {
		where = append(where, "e.alert_rule_id = ?")
		args = append(args, f.RuleID)
	}
	if f.NodeID != "" {
		where = append(where, "e.node_id = ?")
		args = append(args, f.NodeID)
	}
	if f.State != "" {
		where = append(where, "e.event_state = ?")
		args = append(args, f.State)
	}
	if f.FromMs > 0 {
		where = append(where, "e.fired_at_ms >= ?")
		args = append(args, f.FromMs)
	}
	if f.ToMs > 0 {
		where = append(where, "e.fired_at_ms <= ?")
		args = append(args, f.ToMs)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	// Portable count query
	countSQL := "SELECT COUNT(*) FROM alert_events e " + whereClause
	var total int
	if err := s.db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("alert: count events failed: %w", err)
	}

	selectSQL := `SELECT e.id, e.alert_rule_id, e.node_id, e.event_state, e.fired_at_ms,
		e.resolved_at_ms, e.peak_value, e.detail, e.notified_at_ms, e.created_at_ms, e.updated_at_ms,
		r.name, n.name, r.rule_kind, r.severity
		FROM alert_events e
		LEFT JOIN alert_rules r ON e.alert_rule_id = r.id
		LEFT JOIN nodes n ON e.node_id = n.id ` + whereClause + " ORDER BY e.fired_at_ms DESC"

	rows, err := s.db.Query(ctx, selectSQL, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("alert: query events failed: %w", err)
	}
	defer rows.Close()

	var allEvents []*AlertEvent
	for rows.Next() {
		var ev AlertEvent
		var resMs, notMs sql.NullInt64
		var peak sql.NullFloat64
		var detail, ruleName, nodeName, ruleKind, severity sql.NullString

		err := rows.Scan(
			&ev.ID, &ev.AlertRuleID, &ev.NodeID, &ev.EventState, &ev.FiredAtMs,
			&resMs, &peak, &detail, &notMs, &ev.CreatedAtMs, &ev.UpdatedAtMs,
			&ruleName, &nodeName, &ruleKind, &severity,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("alert: scan event failed: %w", err)
		}

		if resMs.Valid {
			ev.ResolvedAtMs = &resMs.Int64
		}
		if notMs.Valid {
			ev.NotifiedAtMs = &notMs.Int64
		}
		if peak.Valid {
			ev.PeakValue = &peak.Float64
		}
		if detail.Valid {
			ev.Detail = detail.String
		}
		if ruleName.Valid {
			ev.RuleName = ruleName.String
		}
		if nodeName.Valid {
			ev.NodeName = nodeName.String
		}
		if ruleKind.Valid {
			ev.RuleKind = ruleKind.String
		}
		if severity.Valid {
			ev.Severity = severity.String
		}

		allEvents = append(allEvents, &ev)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Portable pagination in Go
	start := f.Offset
	if start < 0 {
		start = 0
	}
	if start > len(allEvents) {
		start = len(allEvents)
	}
	end := len(allEvents)
	if f.Limit > 0 && start+f.Limit < end {
		end = start + f.Limit
	}

	return allEvents[start:end], total, nil
}

// SyncBuiltinRules seeds default expiry reminder rules (07-monitoring.md §4.1).
func (s *Store) SyncBuiltinRules(ctx context.Context) error {
	if s.db == nil {
		return errors.New("alert: database is nil")
	}

	defaults := []AlertRule{
		{
			Name:             "VPS到期提前30天提醒",
			IsEnabled:        true,
			RuleKind:         RuleKindExpiry,
			ScopeKind:        ScopeKindAll,
			CompareOp:        CompareOpLTE,
			Threshold:        30,
			DurationS:        0,
			Severity:         SeverityInfo,
			SilenceS:         86400 * 30,
			IncludeAutoRenew: false,
		},
		{
			Name:             "VPS到期提前7天提醒",
			IsEnabled:        true,
			RuleKind:         RuleKindExpiry,
			ScopeKind:        ScopeKindAll,
			CompareOp:        CompareOpLTE,
			Threshold:        7,
			DurationS:        0,
			Severity:         SeverityWarning,
			SilenceS:         86400 * 7,
			IncludeAutoRenew: false,
		},
		{
			Name:             "VPS到期提前1天提醒",
			IsEnabled:        true,
			RuleKind:         RuleKindExpiry,
			ScopeKind:        ScopeKindAll,
			CompareOp:        CompareOpLTE,
			Threshold:        1,
			DurationS:        0,
			Severity:         SeverityCritical,
			SilenceS:         86400,
			IncludeAutoRenew: false,
		},
		{
			Name:             "云账单月度预算告警",
			IsEnabled:        true,
			RuleKind:         RuleKindBudget,
			ScopeKind:        ScopeKindAll,
			CompareOp:        CompareOpGTE,
			Threshold:        0.8,
			DurationS:        0,
			Severity:         SeverityWarning,
			SilenceS:         86400,
			IncludeAutoRenew: false,
		},
	}

	for _, d := range defaults {
		var count int
		err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM alert_rules WHERE name = ?", d.Name).Scan(&count)
		if err != nil {
			return err
		}
		if count == 0 {
			rule := d
			rule.ID = ulid.New()
			if err := s.CreateRule(ctx, &rule); err != nil {
				return err
			}
		}
	}

	return nil
}
