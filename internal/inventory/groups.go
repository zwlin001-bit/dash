package inventory

import (
	"context"
	"database/sql"
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

// NodeGroup 表示节点分组。
type NodeGroup struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayOrder int    `json:"display_order"`
	CreatedAtMs  int64  `json:"created_at_ms"`
	UpdatedAtMs  int64  `json:"updated_at_ms"`
	NodeCount    int    `json:"node_count,omitempty"`
}

// GroupService 负责分组的业务逻辑。
type GroupService struct {
	db           *db.DB
	ListGroupsFn func(ctx context.Context, page, pageSize int) (*PageResult, error)
}

func NewGroupService(database *db.DB) *GroupService {
	return &GroupService{db: database}
}

// ListGroups 列出所有分组（支持分页与节点数量统计）。
func (s *GroupService) ListGroups(ctx context.Context, page, pageSize int) (*PageResult, error) {
	if s.ListGroupsFn != nil {
		return s.ListGroupsFn(ctx, page, pageSize)
	}
	var total int64
	err := s.db.QueryRow(ctx, `SELECT count(1) FROM node_groups`).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("count groups: %w", err)
	}

	q := `SELECT id, name, display_order, created_at_ms, updated_at_ms
FROM node_groups
ORDER BY display_order ASC, name ASC`

	offset := (page - 1) * pageSize
	rows, err := s.db.QueryPage(ctx, q, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("query groups page: %w", err)
	}
	defer rows.Close()

	var items []*NodeGroup
	var groupIDs []string
	for rows.Next() {
		var g NodeGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.DisplayOrder, &g.CreatedAtMs, &g.UpdatedAtMs); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		items = append(items, &g)
		groupIDs = append(groupIDs, g.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 批量统计各分组下的节点数
	if len(groupIDs) > 0 {
		counts, err := s.countNodesByGroup(ctx)
		if err == nil {
			for _, g := range items {
				g.NodeCount = counts[g.ID]
			}
		}
	}

	if items == nil {
		items = []*NodeGroup{}
	}

	return &PageResult{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (s *GroupService) countNodesByGroup(ctx context.Context) (map[string]int, error) {
	q := `SELECT node_group_id, count(1) FROM nodes WHERE node_group_id IS NOT NULL GROUP BY node_group_id`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var gid string
		var cnt int
		if err := rows.Scan(&gid, &cnt); err == nil {
			counts[gid] = cnt
		}
	}
	return counts, nil
}

// GetGroup 获取单个分组详情。
func (s *GroupService) GetGroup(ctx context.Context, id string) (*NodeGroup, error) {
	q := `SELECT id, name, display_order, created_at_ms, updated_at_ms FROM node_groups WHERE id = ?`
	var g NodeGroup
	err := s.db.QueryRow(ctx, q, id).Scan(&g.ID, &g.Name, &g.DisplayOrder, &g.CreatedAtMs, &g.UpdatedAtMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return &g, nil
}

type CreateGroupParams struct {
	Name         string `json:"name"`
	DisplayOrder int    `json:"display_order"`
}

// CreateGroup 创建分组并写审计日志。
func (s *GroupService) CreateGroup(ctx context.Context, params CreateGroupParams, actorKind, actorID, ip string) (*NodeGroup, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, errors.New("name_empty")
	}

	// 唯一性检查
	var exists int
	_ = s.db.QueryRow(ctx, `SELECT count(1) FROM node_groups WHERE name = ?`, name).Scan(&exists)
	if exists > 0 {
		return nil, errors.New("duplicate_name")
	}

	id := ulid.New()
	now := time.Now().UnixMilli()

	q := `INSERT INTO node_groups (id, name, display_order, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q, id, name, params.DisplayOrder, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert group: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "group.create",
		TargetKind: "node_group",
		TargetID:   id,
		Detail:     fmt.Sprintf(`{"name":%q}`, name),
		Result:     "ok",
		IP:         ip,
	})

	return &NodeGroup{
		ID:           id,
		Name:         name,
		DisplayOrder: params.DisplayOrder,
		CreatedAtMs:  now,
		UpdatedAtMs:  now,
	}, nil
}

type UpdateGroupParams struct {
	Name         *string `json:"name"`
	DisplayOrder *int    `json:"display_order"`
}

// UpdateGroup 更新分组（部分更新）并写审计日志。
func (s *GroupService) UpdateGroup(ctx context.Context, id string, params UpdateGroupParams, actorKind, actorID, ip string) (*NodeGroup, error) {
	group, err := s.GetGroup(ctx, id)
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()
	if params.Name != nil {
		name := strings.TrimSpace(*params.Name)
		if name == "" {
			return nil, errors.New("name_empty")
		}
		if name != group.Name {
			var exists int
			_ = s.db.QueryRow(ctx, `SELECT count(1) FROM node_groups WHERE name = ? AND id != ?`, name, id).Scan(&exists)
			if exists > 0 {
				return nil, errors.New("duplicate_name")
			}
			group.Name = name
		}
	}
	if params.DisplayOrder != nil {
		group.DisplayOrder = *params.DisplayOrder
	}
	group.UpdatedAtMs = now

	q := `UPDATE node_groups SET name = ?, display_order = ?, updated_at_ms = ? WHERE id = ?`
	_, err = s.db.Exec(ctx, q, group.Name, group.DisplayOrder, group.UpdatedAtMs, id)
	if err != nil {
		return nil, fmt.Errorf("update group: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "group.update",
		TargetKind: "node_group",
		TargetID:   id,
		Detail:     fmt.Sprintf(`{"name":%q,"display_order":%d}`, group.Name, group.DisplayOrder),
		Result:     "ok",
		IP:         ip,
	})

	return group, nil
}

// DeleteGroup 删除分组：把其下节点的 node_group_id 置 NULL，不级联删节点。
func (s *GroupService) DeleteGroup(ctx context.Context, id string, actorKind, actorID, ip string) error {
	group, err := s.GetGroup(ctx, id)
	if err != nil {
		return err
	}

	now := time.Now().UnixMilli()

	err = s.db.WithTx(ctx, func(tx *db.Tx) error {
		// 1. 其下节点的 node_group_id 置 NULL
		qUnset := `UPDATE nodes SET node_group_id = NULL, updated_at_ms = ? WHERE node_group_id = ?`
		if _, err := tx.Exec(ctx, qUnset, now, id); err != nil {
			return fmt.Errorf("unset nodes group: %w", err)
		}

		// 2. 删除分组本身
		qDel := `DELETE FROM node_groups WHERE id = ?`
		if _, err := tx.Exec(ctx, qDel, id); err != nil {
			return fmt.Errorf("delete group: %w", err)
		}

		// 3. 记录审计日志
		return audit.LogTx(ctx, tx, audit.Entry{
			ActorKind:  actorKind,
			ActorID:    actorID,
			Action:     "group.delete",
			TargetKind: "node_group",
			TargetID:   id,
			Detail:     fmt.Sprintf(`{"name":%q}`, group.Name),
			Result:     "ok",
			IP:         ip,
		})
	})
	return err
}

func (s *GroupService) HandleList(w http.ResponseWriter, r *http.Request) {
	page, pageSize := ParsePagination(r)
	result, err := s.ListGroups(r.Context(), page, pageSize)
	if err != nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "获取分组列表失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, result)
}

func (s *GroupService) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var params CreateGroupParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	g, err := s.CreateGroup(r.Context(), params, actorKind, actorID, ip)
	if err != nil {
		if err.Error() == "name_empty" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "分组名称不能为空", "name")
			return
		}
		if err.Error() == "duplicate_name" {
			JSONError(w, http.StatusConflict, "duplicate_name", "分组名称已存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "创建分组失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, g)
}

func (s *GroupService) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少分组 ID", "id")
		return
	}

	var params UpdateGroupParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	g, err := s.UpdateGroup(r.Context(), id, params, actorKind, actorID, ip)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "group_not_found", "分组不存在", nil)
			return
		}
		if err.Error() == "name_empty" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "分组名称不能为空", "name")
			return
		}
		if err.Error() == "duplicate_name" {
			JSONError(w, http.StatusConflict, "duplicate_name", "分组名称已存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "更新分组失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, g)
}

func (s *GroupService) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少分组 ID", "id")
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	err := s.DeleteGroup(r.Context(), id, actorKind, actorID, ip)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "group_not_found", "分组不存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "删除分组失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, map[string]any{"ok": true})
}
