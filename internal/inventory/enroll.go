package inventory

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"dash/internal/audit"
	"dash/internal/db"
	"dash/internal/ulid"
)

// EnrollToken 表示注册令牌信息。
type EnrollToken struct {
	ID            string  `json:"id"`
	PresetName    *string `json:"preset_name,omitempty"`
	PresetGroupID *string `json:"preset_group_id,omitempty"`
	ExpiresAtMs   int64   `json:"expires_at_ms"`
	UsedAtMs      *int64  `json:"used_at_ms,omitempty"`
	UsedNodeID    *string `json:"used_node_id,omitempty"`
	CreatedBy     *string `json:"created_by,omitempty"`
	CreatedAtMs   int64   `json:"created_at_ms"`
	IsUsed        bool    `json:"is_used"`
	IsExpired     bool    `json:"is_expired"`
}

type CreateEnrollTokenResponse struct {
	ID          string `json:"id"`
	Token       string `json:"token"`
	ExpiresAtMs int64  `json:"expires_at_ms"`
	InstallCmd  string `json:"install_cmd"`
}

type EnrollService struct {
	db *db.DB
}

func NewEnrollService(database *db.DB) *EnrollService {
	return &EnrollService{db: database}
}

type CreateEnrollTokenParams struct {
	PresetName     *string `json:"preset_name"`
	PresetGroupID  *string `json:"preset_group_id"`
	ExpiresInHours *int    `json:"expires_in_hours"`
}

// CreateToken 生成注册令牌与一键安装命令，并写审计日志。
func (s *EnrollService) CreateToken(ctx context.Context, params CreateEnrollTokenParams, domain string, actorKind, actorID, ip string) (*CreateEnrollTokenResponse, error) {
	// 验证预设分组是否存在
	if params.PresetGroupID != nil && *params.PresetGroupID != "" {
		var groupExists int
		_ = s.db.QueryRow(ctx, `SELECT count(1) FROM node_groups WHERE id = ?`, *params.PresetGroupID).Scan(&groupExists)
		if groupExists == 0 {
			return nil, errors.New("group_not_found")
		}
	}

	// 生成 32 字节随机令牌
	plainToken, tokenHash, err := generateEnrollToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	id := ulid.New()
	now := time.Now().UnixMilli()

	// 计算过期时间（默认 15 分钟，或指定小时数）
	var expiresAt int64
	if params.ExpiresInHours != nil && *params.ExpiresInHours > 0 {
		expiresAt = now + int64(*params.ExpiresInHours)*3600*1000
	} else {
		expiresAt = now + 15*60*1000 // 默认 15 分钟
	}

	var nameVal, groupVal, createdByVal any
	if params.PresetName != nil && strings.TrimSpace(*params.PresetName) != "" {
		nameVal = strings.TrimSpace(*params.PresetName)
	}
	if params.PresetGroupID != nil && strings.TrimSpace(*params.PresetGroupID) != "" {
		groupVal = strings.TrimSpace(*params.PresetGroupID)
	}
	if actorID != "" {
		createdByVal = actorID
	}

	q := `INSERT INTO enroll_tokens (id, token_hash, preset_name, preset_group_id, expires_at_ms, created_by, created_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.Exec(ctx, q, id, tokenHash, nameVal, groupVal, expiresAt, createdByVal, now)
	if err != nil {
		return nil, fmt.Errorf("insert enroll token: %w", err)
	}

	// 查询系统配置域名
	configuredDomain := s.getSystemDomain(ctx)
	if configuredDomain != "" {
		domain = configuredDomain
	}
	if domain == "" {
		domain = "localhost:8080"
	}

	endpoint := domain
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "https://" + endpoint
	}
	installCmd := fmt.Sprintf("curl -fsSL %s/install.sh | sh -s -- --enroll %s", endpoint, plainToken)

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "enroll_token.create",
		TargetKind: "enroll_token",
		TargetID:   id,
		Detail:     fmt.Sprintf(`{"expires_at_ms":%d}`, expiresAt),
		Result:     "ok",
		IP:         ip,
	})

	return &CreateEnrollTokenResponse{
		ID:          id,
		Token:       plainToken,
		ExpiresAtMs: expiresAt,
		InstallCmd:  installCmd,
	}, nil
}

func (s *EnrollService) getSystemDomain(ctx context.Context) string {
	var domain string
	q := `SELECT setting_val FROM settings WHERE setting_key = 'site.domain'`
	_ = s.db.QueryRow(ctx, q).Scan(&domain)
	return strings.TrimSpace(domain)
}

// ListTokens 列出注册令牌。
func (s *EnrollService) ListTokens(ctx context.Context, page, pageSize int) (*PageResult, error) {
	var total int64
	err := s.db.QueryRow(ctx, `SELECT count(1) FROM enroll_tokens`).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("count tokens: %w", err)
	}

	q := `SELECT id, preset_name, preset_group_id, expires_at_ms, used_at_ms, used_node_id, created_by, created_at_ms
FROM enroll_tokens
ORDER BY created_at_ms DESC`

	offset := (page - 1) * pageSize
	rows, err := s.db.QueryPage(ctx, q, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("query tokens page: %w", err)
	}
	defer rows.Close()

	now := time.Now().UnixMilli()
	var items []*EnrollToken
	for rows.Next() {
		var t EnrollToken
		var presetName, presetGroup, usedNode, createdBy sql.NullString
		var usedAt sql.NullInt64

		if err := rows.Scan(
			&t.ID, &presetName, &presetGroup, &t.ExpiresAtMs,
			&usedAt, &usedNode, &createdBy, &t.CreatedAtMs,
		); err != nil {
			return nil, fmt.Errorf("scan token: %w", err)
		}

		if presetName.Valid {
			t.PresetName = &presetName.String
		}
		if presetGroup.Valid {
			t.PresetGroupID = &presetGroup.String
		}
		if usedAt.Valid {
			t.UsedAtMs = &usedAt.Int64
		}
		if usedNode.Valid {
			t.UsedNodeID = &usedNode.String
		}
		if createdBy.Valid {
			t.CreatedBy = &createdBy.String
		}

		t.IsUsed = (t.UsedAtMs != nil)
		t.IsExpired = (t.ExpiresAtMs < now)
		items = append(items, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if items == nil {
		items = []*EnrollToken{}
	}

	return &PageResult{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// DeleteToken 删除/作废注册令牌。
func (s *EnrollService) DeleteToken(ctx context.Context, id string, actorKind, actorID, ip string) error {
	q := `DELETE FROM enroll_tokens WHERE id = ?`
	_, err := s.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete token: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "enroll_token.delete",
		TargetKind: "enroll_token",
		TargetID:   id,
		Result:     "ok",
		IP:         ip,
	})
	return nil
}

func generateEnrollToken() (plainToken string, tokenHash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plainToken = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(plainToken))
	tokenHash = hex.EncodeToString(h[:])
	return plainToken, tokenHash, nil
}

func (s *EnrollService) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var params CreateEnrollTokenParams
	if r.Body != nil && r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&params)
	}

	domain := r.Host
	actorKind, actorID, ip := ActorInfo(r)

	resp, err := s.CreateToken(r.Context(), params, domain, actorKind, actorID, ip)
	if err != nil {
		if err.Error() == "group_not_found" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "预设分组不存在", "preset_group_id")
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "生成注册令牌失败", nil)
		return
	}

	JSONSuccess(w, http.StatusOK, resp)
}

func (s *EnrollService) HandleList(w http.ResponseWriter, r *http.Request) {
	page, pageSize := ParsePagination(r)
	res, err := s.ListTokens(r.Context(), page, pageSize)
	if err != nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "获取注册令牌列表失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, res)
}

func (s *EnrollService) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少令牌 ID", "id")
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	if err := s.DeleteToken(r.Context(), id, actorKind, actorID, ip); err != nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "删除注册令牌失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, map[string]any{"ok": true})
}
