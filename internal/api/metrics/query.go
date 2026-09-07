package metrics

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"dash/internal/db"
)

// FieldCategory 定义指标字段的类型归属。
type FieldCategory int

const (
	CategoryAvgMaxMin FieldCategory = iota // 支持 avg / max / min
	CategoryCounter                        // 单调递增计数器，rollup 存 _last
	CategoryDelta                          // 区间增量，rollup 存 _sum
)

type fieldMeta struct {
	category FieldCategory
}

// allSupportedFields 记录 08-field-map.md §1 定义的所有 17 项主机级固定指标。
var allSupportedFields = map[string]fieldMeta{
	"cpu_pct":        {category: CategoryAvgMaxMin},
	"mem_used":       {category: CategoryAvgMaxMin},
	"swap_used":      {category: CategoryAvgMaxMin},
	"load1":          {category: CategoryAvgMaxMin},
	"load5":          {category: CategoryAvgMaxMin},
	"load15":         {category: CategoryAvgMaxMin},
	"disk_used":      {category: CategoryAvgMaxMin},
	"net_up_bps":     {category: CategoryAvgMaxMin},
	"net_down_bps":   {category: CategoryAvgMaxMin},
	"net_total_up":   {category: CategoryCounter},
	"net_total_down": {category: CategoryCounter},
	"traffic_up":     {category: CategoryDelta},
	"traffic_down":   {category: CategoryDelta},
	"proc_count":     {category: CategoryAvgMaxMin},
	"tcp_count":      {category: CategoryAvgMaxMin},
	"udp_count":      {category: CategoryAvgMaxMin},
	"uptime_s":       {category: CategoryCounter},
}

// defaultFieldOrder 定义省略 fields 参数时默认返回的字段顺序。
var defaultFieldOrder = []string{
	"cpu_pct", "mem_used", "swap_used",
	"load1", "load5", "load15", "disk_used",
	"net_up_bps", "net_down_bps",
	"net_total_up", "net_total_down",
	"traffic_up", "traffic_down",
	"proc_count", "tcp_count", "udp_count",
	"uptime_s",
}

// FieldQuery 表示单个待查字段在目标表中的列映射与抽稀策略。
type FieldQuery struct {
	FieldName string // 统一返回的 key（无后缀）
	DBCol     string // 对应数据库中的实际物理列名
	IsCounter bool   // 是否为计数器（抽稀时取窗口末尾值）
}

// SelectTable 根据查询时间跨度自动选择存储表与对应桶宽（P1-13 约束）。
func SelectTable(spanMs int64) (tableName string, stepMs int64, tsCol string) {
	switch {
	case spanMs <= 21_600_000: // ≤ 6 小时 (6 * 3600 * 1000)
		return "sample_host", 0, "ts_ms"
	case spanMs <= 259_200_000: // ≤ 3 天 (3 * 86400 * 1000)
		return "sample_host_1m", 60_000, "bucket_ms"
	case spanMs <= 5_184_000_000: // ≤ 60 天 (60 * 86400 * 1000)
		return "sample_host_1h", 3_600_000, "bucket_ms"
	default: // 更长跨度 (> 60 天)
		return "sample_host_1d", 86_400_000, "bucket_ms"
	}
}

// ParseFields 解析并验证 fields 参数。
func ParseFields(tableName string, fieldsParam string) ([]FieldQuery, error) {
	var requested []string
	if strings.TrimSpace(fieldsParam) == "" {
		requested = defaultFieldOrder
	} else {
		for _, part := range strings.Split(fieldsParam, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				requested = append(requested, part)
			}
		}
	}

	seen := make(map[string]bool)
	var queries []FieldQuery

	for _, item := range requested {
		parts := strings.SplitN(item, ":", 2)
		baseName := strings.TrimSpace(parts[0])
		agg := ""
		if len(parts) == 2 {
			agg = strings.ToLower(strings.TrimSpace(parts[1]))
		}

		meta, ok := allSupportedFields[baseName]
		if !ok {
			return nil, fmt.Errorf("unknown field: %q", baseName)
		}

		if seen[baseName] {
			continue
		}
		seen[baseName] = true

		var dbCol string
		isCounter := meta.category == CategoryCounter

		if tableName == "sample_host" {
			// raw 表直接使用基础列名
			dbCol = baseName
		} else {
			// rollup 表分类型处理
			switch meta.category {
			case CategoryAvgMaxMin:
				if agg == "" {
					agg = "avg"
				}
				if agg != "avg" && agg != "max" && agg != "min" {
					return nil, fmt.Errorf("field %s does not support %q aggregation (expected avg, max, or min)", baseName, agg)
				}
				dbCol = fmt.Sprintf("%s_%s", baseName, agg)

			case CategoryCounter:
				if agg != "" && agg != "last" && agg != "max" {
					return nil, fmt.Errorf("counter field %s only supports :last aggregation", baseName)
				}
				dbCol = fmt.Sprintf("%s_last", baseName)

			case CategoryDelta:
				if agg != "" && agg != "sum" {
					return nil, fmt.Errorf("delta field %s only supports :sum aggregation", baseName)
				}
				dbCol = fmt.Sprintf("%s_sum", baseName)
			}
		}

		queries = append(queries, FieldQuery{
			FieldName: baseName,
			DBCol:     dbCol,
			IsCounter: isCounter,
		})
	}

	return queries, nil
}

// NullableFloat 用于安全扫描各类数值列（float64, int64, int32, string, []byte, NULL）。
type NullableFloat struct {
	Val *float64
}

// Scan 实现 sql.Scanner 接口。
func (n *NullableFloat) Scan(src any) error {
	if src == nil {
		n.Val = nil
		return nil
	}
	switch v := src.(type) {
	case float64:
		val := v
		n.Val = &val
	case float32:
		val := float64(v)
		n.Val = &val
	case int64:
		val := float64(v)
		n.Val = &val
	case int32:
		val := float64(v)
		n.Val = &val
	case int:
		val := float64(v)
		n.Val = &val
	case []byte:
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			return err
		}
		n.Val = &f
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return err
		}
		n.Val = &f
	default:
		return fmt.Errorf("cannot scan %T into NullableFloat", src)
	}
	return nil
}

// RawPoint 表示从数据库读取的单点原始行数据。
type RawPoint struct {
	TsMs   int64
	Values map[string]*float64
}

// Downsample 执行应用层抽稀（P1-13 核心算法）：
// step = ceil(n / max_points)
// 每 step 个点取一个窗口 → 取该窗口的最大值（计数器取最后一个值）
// 时间戳取窗口内被选中那个点的真实时间戳。
func Downsample(points []RawPoint, fields []FieldQuery, maxPoints int) ([]int64, map[string][]*float64) {
	n := len(points)
	resSeries := make(map[string][]*float64, len(fields))
	for _, f := range fields {
		resSeries[f.FieldName] = make([]*float64, 0)
	}
	if n == 0 {
		return make([]int64, 0), resSeries
	}

	if n <= maxPoints {
		resTsMs := make([]int64, n)
		for i, pt := range points {
			resTsMs[i] = pt.TsMs
			for _, f := range fields {
				resSeries[f.FieldName] = append(resSeries[f.FieldName], pt.Values[f.FieldName])
			}
		}
		return resTsMs, resSeries
	}

	step := (n + maxPoints - 1) / maxPoints
	numWindows := (n + step - 1) / step
	resTsMs := make([]int64, 0, numWindows)

	primary := fields[0]

	for w := 0; w < numWindows; w++ {
		startIdx := w * step
		endIdx := (w + 1) * step
		if endIdx > n {
			endIdx = n
		}
		window := points[startIdx:endIdx]
		if len(window) == 0 {
			continue
		}

		// 时间戳取窗口内被选中那个点的真实时间戳
		var winTs int64
		if primary.IsCounter {
			winTs = window[len(window)-1].TsMs
		} else {
			winTs = window[0].TsMs
			var maxPrimaryVal *float64
			for _, pt := range window {
				v := pt.Values[primary.FieldName]
				if v != nil && (maxPrimaryVal == nil || *v > *maxPrimaryVal) {
					maxPrimaryVal = v
					winTs = pt.TsMs
				}
			}
		}
		resTsMs = append(resTsMs, winTs)

		// 各字段抽稀取值：计数器取最后一个非空值，其余取最大值（保留尖峰）
		for _, f := range fields {
			if f.IsCounter {
				var lastVal *float64
				for i := len(window) - 1; i >= 0; i-- {
					if window[i].Values[f.FieldName] != nil {
						lastVal = window[i].Values[f.FieldName]
						break
					}
				}
				resSeries[f.FieldName] = append(resSeries[f.FieldName], lastVal)
			} else {
				var maxVal *float64
				for _, pt := range window {
					v := pt.Values[f.FieldName]
					if v != nil && (maxVal == nil || *v > *maxVal) {
						maxVal = v
					}
				}
				resSeries[f.FieldName] = append(resSeries[f.FieldName], maxVal)
			}
		}
	}

	return resTsMs, resSeries
}

// QueryEngine 负责处理时序数据的查询、自动选表与抽稀。
type QueryEngine struct {
	db *db.DB
}

// NewQueryEngine 创建 QueryEngine 实例。
func NewQueryEngine(database *db.DB) *QueryEngine {
	return &QueryEngine{db: database}
}

// QueryMetrics 执行时序查询并返回列式结果。
func (e *QueryEngine) QueryMetrics(ctx context.Context, nodeID string, fromMs, toMs int64, fieldsParam string, maxPoints int) (*MetricsQueryResponse, error) {
	if toMs <= fromMs {
		return nil, fmt.Errorf("to_ms must be greater than from_ms")
	}

	if maxPoints <= 0 {
		maxPoints = 400
	} else if maxPoints > 1000 {
		maxPoints = 1000
	}

	spanMs := toMs - fromMs
	tableName, stepMs, tsCol := SelectTable(spanMs)

	fieldQueries, err := ParseFields(tableName, fieldsParam)
	if err != nil {
		return nil, err
	}

	if e.db == nil {
		// 无数据库连接时（如纯自测或未初始），返回空序列
		resTsMs, resSeries := Downsample(nil, fieldQueries, maxPoints)
		return &MetricsQueryResponse{
			NodeID: nodeID,
			Source: tableName,
			FromMs: fromMs,
			ToMs:   toMs,
			StepMs: stepMs,
			TsMs:   resTsMs,
			Series: resSeries,
		}, nil
	}

	colNames := make([]string, len(fieldQueries))
	for i, q := range fieldQueries {
		colNames[i] = q.DBCol
	}

	// 严守 P1-03 / 02-database.md §6：单表范围扫描，禁止 join，不使用分页关键字
	querySQL := fmt.Sprintf(
		"SELECT %s, %s FROM %s WHERE node_id = ? AND %s >= ? AND %s < ? ORDER BY %s",
		tsCol, strings.Join(colNames, ", "), tableName, tsCol, tsCol, tsCol,
	)

	rows, err := e.db.Query(ctx, querySQL, nodeID, fromMs, toMs)
	if err != nil {
		return nil, fmt.Errorf("db query failed: %w", err)
	}
	defer rows.Close()

	var rawPoints []RawPoint
	for rows.Next() {
		var ts int64
		scanners := make([]any, 1+len(fieldQueries))
		scanners[0] = &ts

		nullableSlice := make([]NullableFloat, len(fieldQueries))
		for i := range fieldQueries {
			scanners[1+i] = &nullableSlice[i]
		}

		if err := rows.Scan(scanners...); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}

		vals := make(map[string]*float64, len(fieldQueries))
		for i, q := range fieldQueries {
			vals[q.FieldName] = nullableSlice[i].Val
		}

		rawPoints = append(rawPoints, RawPoint{
			TsMs:   ts,
			Values: vals,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	resTsMs, resSeries := Downsample(rawPoints, fieldQueries, maxPoints)

	return &MetricsQueryResponse{
		NodeID: nodeID,
		Source: tableName,
		FromMs: fromMs,
		ToMs:   toMs,
		StepMs: stepMs,
		TsMs:   resTsMs,
		Series: resSeries,
	}, nil
}
