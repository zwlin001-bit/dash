package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dash/internal/audit"
	"dash/internal/auth"
	"dash/internal/control"
	"dash/internal/db"
	"dash/internal/protocol"
)

// SystemSettings 定义第一期暴露给前端与外部调用的系统设置项。
type SystemSettings struct {
	SiteDomain           string `json:"site.domain"`
	RetentionRawDays     int    `json:"retention.raw_days"`
	Retention1mDays      int    `json:"retention.1m_days"`
	Retention1hDays      int    `json:"retention.1h_days"`
	Retention1dDays      int    `json:"retention.1d_days"`
	CollectIntervalFastS int    `json:"collect.interval_fast_s"`
	CollectIntervalSlowS int    `json:"collect.interval_slow_s"`
	CollectEnableConns   bool   `json:"collect.enable_conns"`
}

type Service struct {
	db       *db.DB
	registry *control.Registry
}

func NewService(database *db.DB, registry *control.Registry) *Service {
	return &Service{
		db:       database,
		registry: registry,
	}
}

// GetSettings 从 settings 表读取配置，缺失项填充合理默认值。
func (s *Service) GetSettings(ctx context.Context) (*SystemSettings, error) {
	st := &SystemSettings{
		SiteDomain:           "dash.example.com",
		RetentionRawDays:     3,
		Retention1mDays:      30,
		Retention1hDays:      365,
		Retention1dDays:      0,
		CollectIntervalFastS: 5,
		CollectIntervalSlowS: 60,
		CollectEnableConns:   true,
	}

	if s.db == nil {
		return st, nil
	}

	rows, err := s.db.Query(ctx, `SELECT setting_key, setting_val FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("query settings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var val sql.NullString
		if err := rows.Scan(&key, &val); err != nil {
			continue
		}
		if !val.Valid {
			continue
		}
		v := strings.TrimSpace(val.String)
		switch key {
		case "site.domain":
			if v != "" {
				st.SiteDomain = v
			}
		case "retention.raw_days":
			if n, err := strconv.Atoi(v); err == nil {
				st.RetentionRawDays = n
			}
		case "retention.1m_days":
			if n, err := strconv.Atoi(v); err == nil {
				st.Retention1mDays = n
			}
		case "retention.1h_days":
			if n, err := strconv.Atoi(v); err == nil {
				st.Retention1hDays = n
			}
		case "retention.1d_days":
			if n, err := strconv.Atoi(v); err == nil {
				st.Retention1dDays = n
			}
		case "collect.interval_fast_s":
			if n, err := strconv.Atoi(v); err == nil {
				st.CollectIntervalFastS = n
			}
		case "collect.interval_slow_s":
			if n, err := strconv.Atoi(v); err == nil {
				st.CollectIntervalSlowS = n
			}
		case "collect.enable_conns":
			st.CollectEnableConns = (v == "true" || v == "1")
		}
	}

	return st, nil
}

// UpdateSettings 更新设置项并广播变更给在线 agent。
func (s *Service) UpdateSettings(ctx context.Context, updates map[string]any, actorKind, actorID, ip string) (*SystemSettings, error) {
	if s.db == nil {
		return nil, errors.New("database not available")
	}

	var (
		serverConfigParams protocol.ServerConfigParams
		hasServerConfig    bool
	)

	// 不允许修改 site.domain（第一期只读展示）
	if dom, ok := updates["site.domain"]; ok {
		cur, _ := s.GetSettings(ctx)
		if cur != nil && fmt.Sprintf("%v", dom) != cur.SiteDomain {
			return nil, errors.New("domain_read_only")
		}
	}

	validKeys := map[string]bool{
		"site.domain":             true,
		"retention.raw_days":      true,
		"retention.1m_days":       true,
		"retention.1h_days":       true,
		"retention.1d_days":       true,
		"collect.interval_fast_s": true,
		"collect.interval_slow_s": true,
		"collect.enable_conns":    true,
	}

	dbUpdates := make(map[string]string)

	for k, v := range updates {
		if !validKeys[k] {
			continue
		}
		switch k {
		case "retention.raw_days", "retention.1m_days", "retention.1h_days", "retention.1d_days":
			num, err := toInt(v)
			if err != nil || num < 0 {
				return nil, fmt.Errorf("invalid_%s", strings.ReplaceAll(k, ".", "_"))
			}
			dbUpdates[k] = strconv.Itoa(num)
		case "collect.interval_fast_s":
			num, err := toInt(v)
			if err != nil || num < 1 || num > 60 {
				return nil, errors.New("invalid_interval_fast_s")
			}
			dbUpdates[k] = strconv.Itoa(num)
			serverConfigParams.IntervalFastS = &num
			hasServerConfig = true
		case "collect.interval_slow_s":
			num, err := toInt(v)
			if err != nil || num < 10 || num > 300 {
				return nil, errors.New("invalid_interval_slow_s")
			}
			dbUpdates[k] = strconv.Itoa(num)
			serverConfigParams.IntervalSlowS = &num
			hasServerConfig = true
		case "collect.enable_conns":
			b, ok := toBool(v)
			if !ok {
				return nil, errors.New("invalid_enable_conns")
			}
			valStr := "false"
			if b {
				valStr = "true"
			}
			dbUpdates[k] = valStr
			serverConfigParams.CollectConns = &b
			hasServerConfig = true
		}
	}

	// 事务持久化至 settings 表（标准可移植 Upsert）
	nowMs := time.Now().UnixMilli()
	err := s.db.WithTx(ctx, func(tx *db.Tx) error {
		for k, v := range dbUpdates {
			res, err := tx.Exec(ctx, `UPDATE settings SET setting_val = ?, updated_at_ms = ? WHERE setting_key = ?`, v, nowMs, k)
			if err != nil {
				return err
			}
			rows, _ := res.RowsAffected()
			if rows == 0 {
				_, err = tx.Exec(ctx, `INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES (?, ?, ?)`, k, v, nowMs)
				if err != nil {
					return err
				}
			}
		}

		b, _ := json.Marshal(dbUpdates)
		return audit.LogTx(ctx, tx, audit.Entry{
			ActorKind:  actorKind,
			ActorID:    actorID,
			Action:     "settings.update",
			TargetKind: "settings",
			Detail:     string(b),
			Result:     "ok",
			IP:         ip,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("update settings tx: %w", err)
	}

	// 若变更了 collect.* 参数，立即向在线长连接 Agent 下发 server.config 通知
	if hasServerConfig && s.registry != nil {
		_ = s.registry.BroadcastServerConfig(&serverConfigParams)
	}

	return s.GetSettings(ctx)
}

func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.GetSettings(r.Context())
	if err != nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "获取系统设置失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, st)
}

func (s *Service) HandlePatch(w http.ResponseWriter, r *http.Request) {
	var updates map[string]any
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体格式错误", nil)
		return
	}

	actorKind, actorID, ip := "system", "", ""
	if u := auth.UserFromContext(r.Context()); u != nil {
		actorKind = "user"
		actorID = u.ID
	}
	ip = auth.ClientIP(r)

	st, err := s.UpdateSettings(r.Context(), updates, actorKind, actorID, ip)
	if err != nil {
		if err.Error() == "domain_read_only" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "域名在第一期只读展示，不可在线修改", "site.domain")
			return
		}
		if strings.HasPrefix(err.Error(), "invalid_") {
			field := strings.TrimPrefix(err.Error(), "invalid_")
			JSONError(w, http.StatusBadRequest, "invalid_param", "参数校验失败: "+field, field)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "保存系统设置失败: "+err.Error(), nil)
		return
	}

	JSONSuccess(w, http.StatusOK, st)
}

func JSONError(w http.ResponseWriter, status int, code, message string, detail any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"detail":  detail,
		},
	})
}

func JSONSuccess(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

func toInt(v any) (int, error) {
	switch val := v.(type) {
	case int:
		return val, nil
	case int64:
		return int(val), nil
	case float64:
		return int(val), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(val))
	default:
		return 0, errors.New("not an integer")
	}
}

func toBool(v any) (bool, bool) {
	switch val := v.(type) {
	case bool:
		return val, true
	case string:
		s := strings.ToLower(strings.TrimSpace(val))
		if s == "true" || s == "1" {
			return true, true
		}
		if s == "false" || s == "0" {
			return false, true
		}
	}
	return false, false
}
