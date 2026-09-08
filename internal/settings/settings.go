package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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
	ConsoleDomain        string `json:"site.console_domain,omitempty"`
	RetentionRawDays     int    `json:"retention.raw_days"`
	Retention1mDays      int    `json:"retention.1m_days"`
	Retention1hDays      int    `json:"retention.1h_days"`
	Retention1dDays      int    `json:"retention.1d_days"`
	CollectIntervalFastS int    `json:"collect.interval_fast_s"`
	CollectIntervalSlowS int    `json:"collect.interval_slow_s"`
	CollectEnableConns   bool   `json:"collect.enable_conns"`
}

type Service struct {
	db            *db.DB
	registry      *control.Registry
	GetSettingsFn func(ctx context.Context) (*SystemSettings, error)
}

func NewService(database *db.DB, registry *control.Registry) *Service {
	return &Service{
		db:       database,
		registry: registry,
	}
}

// GetSettings 从 settings 表读取配置，缺失项填充合理默认值。
func (s *Service) GetSettings(ctx context.Context) (*SystemSettings, error) {
	if s.GetSettingsFn != nil {
		return s.GetSettingsFn(ctx)
	}
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
			st.SiteDomain = v
		case "site.console_domain":
			st.ConsoleDomain = v
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

	validKeys := map[string]bool{
		"site.domain":             true,
		"site.console_domain":     true,
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
		case "site.domain", "site.console_domain":
			strVal, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("invalid_%s: must be a string", strings.ReplaceAll(k, ".", "_"))
			}
			strVal = strings.TrimSpace(strVal)
			if err := validateDomainOrHostPort(strVal); err != nil {
				return nil, fmt.Errorf("invalid_%s: %w", strings.ReplaceAll(k, ".", "_"), err)
			}
			dbUpdates[k] = strVal
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
		if strings.HasPrefix(err.Error(), "invalid_") {
			parts := strings.SplitN(err.Error(), ": ", 2)
			field := strings.TrimPrefix(parts[0], "invalid_")
			msg := "参数校验失败: " + field
			if len(parts) > 1 {
				msg = parts[1]
			}
			JSONError(w, http.StatusBadRequest, "invalid_param", msg, field)
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

// validateDomainOrHostPort 校验域名或 域名:端口 格式。
// 规则：允许为空字符串（清空域名）；禁止带 scheme（如 http://, https://）；禁止带路径（/）、参数（?）、哈希（#）或空格；
// 格式必须为合法主机名/IP，或主机名:端口 (1-65535)。
func validateDomainOrHostPort(val string) error {
	if val == "" {
		return nil
	}

	lower := strings.ToLower(val)
	if strings.Contains(lower, "://") || strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:") {
		return errors.New("域名不能包含协议头 (如 http:// 或 https://)")
	}
	if strings.ContainsAny(val, "/?# \t\r\n") {
		return errors.New("域名不能包含路径 (/)、参数或空格")
	}

	host := val
	if strings.Contains(val, ":") {
		h, portStr, err := net.SplitHostPort(val)
		if err != nil {
			return errors.New("域名端口格式错误，必须为 域名:端口 或合法域名")
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return errors.New("端口号必须在 1 ~ 65535 之间")
		}
		host = h
	}

	if host == "" {
		return errors.New("域名不能为空")
	}

	// 校验 host 格式：可以为 IPv4/IPv6 或 hostname
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}

	// 校验 hostname
	if len(host) > 253 {
		return errors.New("域名长度不能超过 253 个字符")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return errors.New("域名各标签长度必须在 1 ~ 63 个字符之间")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("域名标签不能以短横线 '-' 开头或结尾")
		}
		for _, ch := range label {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return errors.New("域名只能包含英文字母、数字和短横线 '-'")
			}
		}
	}

	return nil
}

