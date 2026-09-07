package inventory

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"

	"dash/internal/auth"
)

// PageResult 统一的分页返回结构。
type PageResult struct {
	Items    any   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// JSONError 返回标准错误响应：{"error":{"code":"...","message":"...","detail":null}}
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

// JSONSuccess 返回标准 JSON 成功响应。
func JSONSuccess(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

// ParsePagination 从查询参数解析 page 与 page_size。
func ParsePagination(r *http.Request) (page, pageSize int) {
	page = 1
	pageSize = 50

	if pStr := r.URL.Query().Get("page"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			page = p
		}
	}
	if psStr := r.URL.Query().Get("page_size"); psStr != "" {
		if ps, err := strconv.Atoi(psStr); err == nil && ps > 0 {
			if ps > 200 {
				ps = 200
			}
			pageSize = ps
		}
	}
	return page, pageSize
}

// ActorInfo 从请求上下文中提取当前操作者标识与 IP。
func ActorInfo(r *http.Request) (actorKind, actorID, ip string) {
	actorKind = "user"
	u := auth.UserFromContext(r.Context())
	if u != nil {
		actorID = u.ID
	}
	ip = clientIP(r)
	return actorKind, actorID, ip
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
