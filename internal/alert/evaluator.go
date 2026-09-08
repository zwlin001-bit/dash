package alert

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"dash/internal/db"
)

// EvalResult holds the evaluation outcome of an alert rule against a single node.
type EvalResult struct {
	NodeID      string
	NodeName    string
	Breached    bool
	CurrentVal  float64
	Threshold   float64
	Detail      string
	EvaluatedAt int64
}

// CompareValues evaluates comparison operators: gt, gte, lt, lte.
func CompareValues(val float64, op string, threshold float64) bool {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case CompareOpGT:
		return val > threshold
	case CompareOpGTE:
		return val >= threshold
	case CompareOpLT:
		return val < threshold
	case CompareOpLTE:
		return val <= threshold
	default:
		return val > threshold
	}
}

// CalculateBillingCycleStart calculates the epoch millisecond start of the current billing cycle.
// Based on traffic_reset_day (1-31).
func CalculateBillingCycleStart(now time.Time, resetDay int) int64 {
	if resetDay <= 0 || resetDay > 31 {
		resetDay = 1
	}

	u := now.UTC()
	year, month, day := u.Date()

	var targetYear int
	var targetMonth time.Month

	if day >= resetDay {
		targetYear = year
		targetMonth = month
	} else {
		if month == time.January {
			targetYear = year - 1
			targetMonth = time.December
		} else {
			targetYear = year
			targetMonth = month - 1
		}
	}

	// Clamp resetDay to the max days in targetMonth
	// Date(year, month+1, 0) gives the last day of targetMonth
	lastDayOfMonth := time.Date(targetYear, targetMonth+1, 0, 0, 0, 0, 0, time.UTC).Day()
	effectiveDay := resetDay
	if effectiveDay > lastDayOfMonth {
		effectiveDay = lastDayOfMonth
	}

	cycleStart := time.Date(targetYear, targetMonth, effectiveDay, 0, 0, 0, 0, time.UTC)
	return cycleStart.UnixMilli()
}

// Evaluator evaluates alert rules against nodes based on metric, offline, expiry, or traffic conditions.
type Evaluator struct {
	store *Store
}

// NewEvaluator creates a new Evaluator.
func NewEvaluator(s *Store) *Evaluator {
	return &Evaluator{store: s}
}

// EvaluateRule evaluates an alert rule across all applicable nodes.
func (e *Evaluator) EvaluateRule(ctx context.Context, rule *AlertRule) ([]EvalResult, error) {
	nodeIDs, err := e.store.GetRuleNodeIDs(ctx, rule)
	if err != nil {
		return nil, fmt.Errorf("alert: resolve nodes for rule %s failed: %w", rule.ID, err)
	}
	if len(nodeIDs) == 0 {
		return nil, nil
	}

	var results []EvalResult
	for _, nodeID := range nodeIDs {
		res, err := e.EvaluateNode(ctx, rule, nodeID)
		if err != nil {
			// Skip or log individual node errors without aborting rule evaluation
			continue
		}
		results = append(results, res)
	}
	return results, nil
}

// EvaluateNode evaluates a rule for a specific node.
func (e *Evaluator) EvaluateNode(ctx context.Context, rule *AlertRule, nodeID string) (EvalResult, error) {
	nowMs := time.Now().UnixMilli()
	nodeName := e.getNodeName(ctx, nodeID)

	res := EvalResult{
		NodeID:      nodeID,
		NodeName:    nodeName,
		Threshold:   rule.Threshold,
		EvaluatedAt: nowMs,
	}

	switch rule.RuleKind {
	case RuleKindMetric:
		return e.evalMetric(ctx, rule, nodeID, nodeName, nowMs)
	case RuleKindOffline:
		return e.evalOffline(ctx, rule, nodeID, nodeName, nowMs)
	case RuleKindExpiry:
		return e.evalExpiry(ctx, rule, nodeID, nodeName, nowMs)
	case RuleKindTraffic:
		return e.evalTraffic(ctx, rule, nodeID, nodeName, nowMs)
	default:
		return res, fmt.Errorf("unknown rule_kind: %s", rule.RuleKind)
	}
}

func (e *Evaluator) getNodeName(ctx context.Context, nodeID string) string {
	if e.store.DB() == nil {
		return nodeID
	}
	var name string
	err := e.store.DB().QueryRow(ctx, "SELECT name FROM nodes WHERE id = ?", nodeID).Scan(&name)
	if err == nil && name != "" {
		return name
	}
	return nodeID
}

// evalMetric reads from sample_host_1m or sample_dim_1m (never from raw sample_host / sample_dim).
// 07-monitoring.md §2.2: 从 _1m rollup 读，不从 raw 读。
func (e *Evaluator) evalMetric(ctx context.Context, rule *AlertRule, nodeID, nodeName string, nowMs int64) (EvalResult, error) {
	res := EvalResult{
		NodeID:      nodeID,
		NodeName:    nodeName,
		Threshold:   rule.Threshold,
		EvaluatedAt: nowMs,
	}

	database := e.store.DB()
	if database == nil {
		return res, errors.New("alert: db is nil")
	}

	metricCode := strings.ToLower(strings.TrimSpace(rule.MetricCode))
	if metricCode == "" {
		metricCode = "cpu_pct"
	}

	// 1. Check if it is a host-level metric in sample_host_1m
	hostColMap := map[string]string{
		"cpu_pct":      "cpu_pct_avg",
		"mem_used":     "mem_used_avg",
		"swap_used":    "swap_used_avg",
		"load1":        "load1_avg",
		"load5":        "load5_avg",
		"load15":       "load15_avg",
		"disk_used":    "disk_used_avg",
		"net_up_bps":   "net_up_bps_avg",
		"net_down_bps": "net_down_bps_avg",
		"proc_count":   "proc_count_avg",
		"tcp_count":    "tcp_count_avg",
		"udp_count":    "udp_count_avg",
	}

	if colName, ok := hostColMap[metricCode]; ok {
		// Query the latest 1m rollup row for this node
		// Portable SQL: query recent buckets ordered by bucket_ms DESC, scan first row
		q := fmt.Sprintf("SELECT bucket_ms, %s FROM sample_host_1m WHERE node_id = ? ORDER BY bucket_ms DESC", colName)
		rows, err := database.Query(ctx, q, nodeID)
		if err != nil {
			return res, err
		}
		defer rows.Close()

		if !rows.Next() {
			// No metric sample in rollup yet
			return res, nil
		}

		var bucketMs int64
		var val sql.NullFloat64
		if err := rows.Scan(&bucketMs, &val); err != nil {
			return res, err
		}

		if !val.Valid {
			return res, nil
		}

		currentVal := val.Float64
		res.CurrentVal = currentVal
		res.Breached = CompareValues(currentVal, rule.CompareOp, rule.Threshold)
		if res.Breached {
			res.Detail = fmt.Sprintf("指标 %s 当前均值 %.2f %s 阈值 %.2f", metricCode, currentVal, rule.CompareOp, rule.Threshold)
		}
		return res, nil
	}

	// Special case: mem_pct calculated from mem_used_avg / mem_total
	if metricCode == "mem_pct" {
		var memTotal int64
		err := database.QueryRow(ctx, "SELECT mem_total FROM node_facts WHERE node_id = ?", nodeID).Scan(&memTotal)
		if err != nil || memTotal <= 0 {
			return res, nil
		}

		q := "SELECT bucket_ms, mem_used_avg FROM sample_host_1m WHERE node_id = ? ORDER BY bucket_ms DESC"
		rows, err := database.Query(ctx, q, nodeID)
		if err != nil {
			return res, err
		}
		defer rows.Close()

		if !rows.Next() {
			return res, nil
		}

		var bucketMs int64
		var memUsed sql.NullFloat64
		if err := rows.Scan(&bucketMs, &memUsed); err != nil || !memUsed.Valid {
			return res, nil
		}

		pct := (memUsed.Float64 / float64(memTotal)) * 100.0
		res.CurrentVal = pct
		res.Breached = CompareValues(pct, rule.CompareOp, rule.Threshold)
		if res.Breached {
			res.Detail = fmt.Sprintf("内存使用率 %.1f%% %s 阈值 %.1f%%", pct, rule.CompareOp, rule.Threshold)
		}
		return res, nil
	}

	// 2. Check if it is a dimension metric in sample_dim_1m
	qDim := `SELECT sd.bucket_ms, sd.val_avg FROM sample_dim_1m sd
		JOIN metric_series ms ON sd.series_id = ms.id
		WHERE ms.node_id = ? AND ms.metric_code = ?
		ORDER BY sd.bucket_ms DESC`

	rows, err := database.Query(ctx, qDim, nodeID, metricCode)
	if err != nil {
		return res, err
	}
	defer rows.Close()

	if !rows.Next() {
		return res, nil
	}

	var bucketMs int64
	var val sql.NullFloat64
	if err := rows.Scan(&bucketMs, &val); err != nil || !val.Valid {
		return res, nil
	}

	currentVal := val.Float64
	res.CurrentVal = currentVal
	res.Breached = CompareValues(currentVal, rule.CompareOp, rule.Threshold)
	if res.Breached {
		res.Detail = fmt.Sprintf("指标 %s 当前值 %.2f %s 阈值 %.2f", metricCode, currentVal, rule.CompareOp, rule.Threshold)
	}
	return res, nil
}

// evalOffline evaluates nodes.last_seen_at_ms against threshold seconds (07-monitoring.md §2.1).
func (e *Evaluator) evalOffline(ctx context.Context, rule *AlertRule, nodeID, nodeName string, nowMs int64) (EvalResult, error) {
	res := EvalResult{
		NodeID:      nodeID,
		NodeName:    nodeName,
		Threshold:   rule.Threshold,
		EvaluatedAt: nowMs,
	}

	database := e.store.DB()
	if database == nil {
		return res, errors.New("alert: db is nil")
	}

	var connState string
	var lastSeenMs sql.NullInt64
	err := database.QueryRow(ctx, "SELECT conn_state, last_seen_at_ms FROM nodes WHERE id = ?", nodeID).Scan(&connState, &lastSeenMs)
	if err != nil {
		return res, err
	}

	var offlineSeconds float64
	if !lastSeenMs.Valid || lastSeenMs.Int64 <= 0 {
		offlineSeconds = 999999
	} else {
		offlineSeconds = float64(nowMs-lastSeenMs.Int64) / 1000.0
		if offlineSeconds < 0 {
			offlineSeconds = 0
		}
	}

	res.CurrentVal = offlineSeconds
	threshold := rule.Threshold
	if threshold <= 0 {
		threshold = 300 // Default 5 minutes
	}

	res.Breached = (connState == "offline") || CompareValues(offlineSeconds, rule.CompareOp, threshold)
	if res.Breached {
		res.Detail = fmt.Sprintf("节点离线 %.0f 秒 (阈值: %.0f 秒)", offlineSeconds, threshold)
	}
	return res, nil
}

// evalExpiry evaluates node_billing.expires_at_ms against threshold days (07-monitoring.md §4.1).
func (e *Evaluator) evalExpiry(ctx context.Context, rule *AlertRule, nodeID, nodeName string, nowMs int64) (EvalResult, error) {
	res := EvalResult{
		NodeID:      nodeID,
		NodeName:    nodeName,
		Threshold:   rule.Threshold,
		EvaluatedAt: nowMs,
	}

	database := e.store.DB()
	if database == nil {
		return res, errors.New("alert: db is nil")
	}

	var expiresAtMs sql.NullInt64
	var isAutoRenew int
	err := database.QueryRow(ctx, "SELECT expires_at_ms, is_auto_renew FROM node_billing WHERE node_id = ?", nodeID).Scan(&expiresAtMs, &isAutoRenew)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
			// No billing record for node
			return res, nil
		}
		return res, err
	}

	if !expiresAtMs.Valid || expiresAtMs.Int64 <= 0 {
		return res, nil
	}

	// is_auto_renew=1 的节点默认不提醒，规则里可开关 (07-monitoring.md §4.1)
	if isAutoRenew == 1 && !rule.IncludeAutoRenew {
		return res, nil
	}

	remainingDays := float64(expiresAtMs.Int64-nowMs) / (86400.0 * 1000.0)
	res.CurrentVal = math.Round(remainingDays*10) / 10

	op := rule.CompareOp
	if op == "" {
		op = CompareOpLTE
	}

	res.Breached = CompareValues(remainingDays, op, rule.Threshold)
	if res.Breached {
		res.Detail = fmt.Sprintf("VPS 还有 %.1f 天到期 (阈值: %.0f 天)", remainingDays, rule.Threshold)
	}
	return res, nil
}

// evalTraffic evaluates bandwidth usage from sample_host_1h traffic_*_sum against node_billing.traffic_limit (07-monitoring.md §4.2).
func (e *Evaluator) evalTraffic(ctx context.Context, rule *AlertRule, nodeID, nodeName string, nowMs int64) (EvalResult, error) {
	res := EvalResult{
		NodeID:      nodeID,
		NodeName:    nodeName,
		Threshold:   rule.Threshold,
		EvaluatedAt: nowMs,
	}

	database := e.store.DB()
	if database == nil {
		return res, errors.New("alert: db is nil")
	}

	var trafficLimit sql.NullInt64
	var trafficLimitKind sql.NullString
	var trafficResetDay sql.NullInt32

	err := database.QueryRow(ctx, "SELECT traffic_limit, traffic_limit_kind, traffic_reset_day FROM node_billing WHERE node_id = ?", nodeID).
		Scan(&trafficLimit, &trafficLimitKind, &trafficResetDay)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
			return res, nil
		}
		return res, err
	}

	if !trafficLimit.Valid || trafficLimit.Int64 <= 0 {
		return res, nil
	}

	resetDay := 1
	if trafficResetDay.Valid && trafficResetDay.Int32 >= 1 && trafficResetDay.Int32 <= 31 {
		resetDay = int(trafficResetDay.Int32)
	}

	now := time.UnixMilli(nowMs)
	cycleStartMs := CalculateBillingCycleStart(now, resetDay)

	// Sum traffic_up_sum and traffic_down_sum from sample_host_1h within current billing cycle
	// Notice: sample_host_1h aggregates traffic_up/down delta (which handles resets gracefully per 02-database.md §5.7)
	q := `SELECT SUM(traffic_up_sum), SUM(traffic_down_sum) FROM sample_host_1h
		WHERE node_id = ? AND bucket_ms >= ?`

	var upSum, downSum sql.NullInt64
	if err := database.QueryRow(ctx, q, nodeID, cycleStartMs).Scan(&upSum, &downSum); err != nil {
		return res, err
	}

	var upBytes, downBytes int64
	if upSum.Valid {
		upBytes = upSum.Int64
	}
	if downSum.Valid {
		downBytes = downSum.Int64
	}

	limitKind := "sum"
	if trafficLimitKind.Valid && trafficLimitKind.String != "" {
		limitKind = strings.ToLower(trafficLimitKind.String)
	}

	var usedBytes int64
	switch limitKind {
	case "max":
		usedBytes = upBytes
		if downBytes > usedBytes {
			usedBytes = downBytes
		}
	case "min":
		usedBytes = upBytes
		if downBytes < usedBytes {
			usedBytes = downBytes
		}
	case "up":
		usedBytes = upBytes
	case "down":
		usedBytes = downBytes
	case "sum":
		fallthrough
	default:
		usedBytes = upBytes + downBytes
	}

	limitBytes := trafficLimit.Int64
	ratio := float64(usedBytes) / float64(limitBytes)
	res.CurrentVal = math.Round(ratio*1000) / 1000

	op := rule.CompareOp
	if op == "" {
		op = CompareOpGTE
	}

	res.Breached = CompareValues(ratio, op, rule.Threshold)
	if res.Breached {
		res.Detail = fmt.Sprintf("月度流量用量占比达到 %.1f%% (已用 %s / 配额 %s, 阈值: %.1f%%)",
			ratio*100, formatBytes(usedBytes), formatBytes(limitBytes), rule.Threshold*100)
	}
	return res, nil
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
