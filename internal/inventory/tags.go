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

// Tag 表示节点标签。
type Tag struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Color       *string `json:"color,omitempty"`
	CreatedAtMs int64   `json:"created_at_ms"`
	UpdatedAtMs int64   `json:"updated_at_ms"`
	NodeCount   int     `json:"node_count,omitempty"`
}

// TagService 负责标签及节点打标签的业务逻辑。
type TagService struct {
	db                *db.DB
	ListTagsFn        func(ctx context.Context, page, pageSize int) (*PageResult, error)
	ReplaceNodeTagsFn func(ctx context.Context, nodeID string, tagIDs []string, actorKind, actorID, ip string) error
}

func NewTagService(database *db.DB) *TagService {
	return &TagService{db: database}
}

// ListTags 列出所有标签（支持分页与关联节点计数）。
func (s *TagService) ListTags(ctx context.Context, page, pageSize int) (*PageResult, error) {
	if s.ListTagsFn != nil {
		return s.ListTagsFn(ctx, page, pageSize)
	}
	var total int64
	err := s.db.QueryRow(ctx, `SELECT count(1) FROM tags`).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("count tags: %w", err)
	}

	q := `SELECT id, name, color, created_at_ms, updated_at_ms FROM tags ORDER BY name ASC`
	offset := (page - 1) * pageSize
	rows, err := s.db.QueryPage(ctx, q, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("query tags page: %w", err)
	}
	defer rows.Close()

	var items []*Tag
	for rows.Next() {
		var t Tag
		var color sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &color, &t.CreatedAtMs, &t.UpdatedAtMs); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		if color.Valid {
			t.Color = &color.String
		}
		items = append(items, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(items) > 0 {
		counts, err := s.countNodesByTag(ctx)
		if err == nil {
			for _, t := range items {
				t.NodeCount = counts[t.ID]
			}
		}
	}

	if items == nil {
		items = []*Tag{}
	}

	return &PageResult{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (s *TagService) countNodesByTag(ctx context.Context) (map[string]int, error) {
	q := `SELECT tag_id, count(1) FROM node_tags GROUP BY tag_id`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var tid string
		var cnt int
		if err := rows.Scan(&tid, &cnt); err == nil {
			counts[tid] = cnt
		}
	}
	return counts, nil
}

// GetTag 获取单个标签。
func (s *TagService) GetTag(ctx context.Context, id string) (*Tag, error) {
	q := `SELECT id, name, color, created_at_ms, updated_at_ms FROM tags WHERE id = ?`
	var t Tag
	var color sql.NullString
	err := s.db.QueryRow(ctx, q, id).Scan(&t.ID, &t.Name, &color, &t.CreatedAtMs, &t.UpdatedAtMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	if color.Valid {
		t.Color = &color.String
	}
	return &t, nil
}

type CreateTagParams struct {
	Name  string  `json:"name"`
	Color *string `json:"color"`
}

// CreateTag 创建标签并写审计日志。
func (s *TagService) CreateTag(ctx context.Context, params CreateTagParams, actorKind, actorID, ip string) (*Tag, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, errors.New("name_empty")
	}

	var exists int
	_ = s.db.QueryRow(ctx, `SELECT count(1) FROM tags WHERE name = ?`, name).Scan(&exists)
	if exists > 0 {
		return nil, errors.New("duplicate_name")
	}

	id := ulid.New()
	now := time.Now().UnixMilli()

	var colorVal any
	if params.Color != nil && strings.TrimSpace(*params.Color) != "" {
		c := strings.TrimSpace(*params.Color)
		colorVal = c
	}

	q := `INSERT INTO tags (id, name, color, created_at_ms, updated_at_ms) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q, id, name, colorVal, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert tag: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "tag.create",
		TargetKind: "tag",
		TargetID:   id,
		Detail:     fmt.Sprintf(`{"name":%q}`, name),
		Result:     "ok",
		IP:         ip,
	})

	tag := &Tag{
		ID:          id,
		Name:        name,
		CreatedAtMs: now,
		UpdatedAtMs: now,
	}
	if params.Color != nil {
		tag.Color = params.Color
	}
	return tag, nil
}

type UpdateTagParams struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

// UpdateTag 更新标签（部分更新）并写审计日志。
func (s *TagService) UpdateTag(ctx context.Context, id string, params UpdateTagParams, actorKind, actorID, ip string) (*Tag, error) {
	tag, err := s.GetTag(ctx, id)
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()
	if params.Name != nil {
		name := strings.TrimSpace(*params.Name)
		if name == "" {
			return nil, errors.New("name_empty")
		}
		if name != tag.Name {
			var exists int
			_ = s.db.QueryRow(ctx, `SELECT count(1) FROM tags WHERE name = ? AND id != ?`, name, id).Scan(&exists)
			if exists > 0 {
				return nil, errors.New("duplicate_name")
			}
			tag.Name = name
		}
	}
	if params.Color != nil {
		c := strings.TrimSpace(*params.Color)
		if c == "" {
			tag.Color = nil
		} else {
			tag.Color = &c
		}
	}
	tag.UpdatedAtMs = now

	var colorVal any
	if tag.Color != nil {
		colorVal = *tag.Color
	}

	q := `UPDATE tags SET name = ?, color = ?, updated_at_ms = ? WHERE id = ?`
	_, err = s.db.Exec(ctx, q, tag.Name, colorVal, tag.UpdatedAtMs, id)
	if err != nil {
		return nil, fmt.Errorf("update tag: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "tag.update",
		TargetKind: "tag",
		TargetID:   id,
		Detail:     fmt.Sprintf(`{"name":%q}`, tag.Name),
		Result:     "ok",
		IP:         ip,
	})

	return tag, nil
}

// DeleteTag 删除标签：先删 node_tags 里的关联行，再删标签。
func (s *TagService) DeleteTag(ctx context.Context, id string, actorKind, actorID, ip string) error {
	tag, err := s.GetTag(ctx, id)
	if err != nil {
		return err
	}

	err = s.db.WithTx(ctx, func(tx *db.Tx) error {
		// 1. 清理 node_tags 里的关联行
		qRel := `DELETE FROM node_tags WHERE tag_id = ?`
		if _, err := tx.Exec(ctx, qRel, id); err != nil {
			return fmt.Errorf("delete tag associations: %w", err)
		}

		// 2. 删除标签
		qTag := `DELETE FROM tags WHERE id = ?`
		if _, err := tx.Exec(ctx, qTag, id); err != nil {
			return fmt.Errorf("delete tag: %w", err)
		}

		// 3. 记录审计日志
		return audit.LogTx(ctx, tx, audit.Entry{
			ActorKind:  actorKind,
			ActorID:    actorID,
			Action:     "tag.delete",
			TargetKind: "tag",
			TargetID:   id,
			Detail:     fmt.Sprintf(`{"name":%q}`, tag.Name),
			Result:     "ok",
			IP:         ip,
		})
	})
	return err
}

// AttachTag 为指定节点添加单个标签（幂等）。
func (s *TagService) AttachTag(ctx context.Context, nodeID, tagID string, actorKind, actorID, ip string) error {
	// 校验节点与标签是否存在
	var nodeExists, tagExists int
	_ = s.db.QueryRow(ctx, `SELECT count(1) FROM nodes WHERE id = ?`, nodeID).Scan(&nodeExists)
	if nodeExists == 0 {
		return db.ErrNotFound
	}
	_ = s.db.QueryRow(ctx, `SELECT count(1) FROM tags WHERE id = ?`, tagID).Scan(&tagExists)
	if tagExists == 0 {
		return errors.New("tag_not_found")
	}

	var relExists int
	_ = s.db.QueryRow(ctx, `SELECT count(1) FROM node_tags WHERE node_id = ? AND tag_id = ?`, nodeID, tagID).Scan(&relExists)
	if relExists == 0 {
		q := `INSERT INTO node_tags (node_id, tag_id) VALUES (?, ?)`
		if _, err := s.db.Exec(ctx, q, nodeID, tagID); err != nil {
			return fmt.Errorf("attach tag: %w", err)
		}
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "node.tag_add",
		TargetKind: "node",
		TargetID:   nodeID,
		Detail:     fmt.Sprintf(`{"tag_id":%q}`, tagID),
		Result:     "ok",
		IP:         ip,
	})
	return nil
}

// DetachTag 为指定节点移除单个标签。
func (s *TagService) DetachTag(ctx context.Context, nodeID, tagID string, actorKind, actorID, ip string) error {
	q := `DELETE FROM node_tags WHERE node_id = ? AND tag_id = ?`
	if _, err := s.db.Exec(ctx, q, nodeID, tagID); err != nil {
		return fmt.Errorf("detach tag: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "node.tag_remove",
		TargetKind: "node",
		TargetID:   nodeID,
		Detail:     fmt.Sprintf(`{"tag_id":%q}`, tagID),
		Result:     "ok",
		IP:         ip,
	})
	return nil
}

// ReplaceNodeTags 全量替换节点的标签。
func (s *TagService) ReplaceNodeTags(ctx context.Context, nodeID string, tagIDs []string, actorKind, actorID, ip string) error {
	if s.ReplaceNodeTagsFn != nil {
		return s.ReplaceNodeTagsFn(ctx, nodeID, tagIDs, actorKind, actorID, ip)
	}
	var nodeExists int
	_ = s.db.QueryRow(ctx, `SELECT count(1) FROM nodes WHERE id = ?`, nodeID).Scan(&nodeExists)
	if nodeExists == 0 {
		return db.ErrNotFound
	}

	err := s.db.WithTx(ctx, func(tx *db.Tx) error {
		// 1. 删除旧关联
		if _, err := tx.Exec(ctx, `DELETE FROM node_tags WHERE node_id = ?`, nodeID); err != nil {
			return err
		}

		// 2. 插入新关联
		for _, tid := range tagIDs {
			tid = strings.TrimSpace(tid)
			if tid == "" {
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO node_tags (node_id, tag_id) VALUES (?, ?)`, nodeID, tid); err != nil {
				return err
			}
		}

		// 3. 审计日志
		detail, _ := json.Marshal(map[string]any{"tag_ids": tagIDs})
		return audit.LogTx(ctx, tx, audit.Entry{
			ActorKind:  actorKind,
			ActorID:    actorID,
			Action:     "node.tags_replace",
			TargetKind: "node",
			TargetID:   nodeID,
			Detail:     string(detail),
			Result:     "ok",
			IP:         ip,
		})
	})
	return err
}

// BatchTagNodes 批量为多个节点添加或移除标签。
func (s *TagService) BatchTagNodes(ctx context.Context, nodeIDs, tagIDs []string, op string, actorKind, actorID, ip string) error {
	if len(nodeIDs) == 0 || len(tagIDs) == 0 {
		return nil
	}
	op = strings.ToLower(strings.TrimSpace(op))
	if op != "add" && op != "remove" {
		return errors.New("invalid_op")
	}

	err := s.db.WithTx(ctx, func(tx *db.Tx) error {
		for _, nid := range nodeIDs {
			nid = strings.TrimSpace(nid)
			if nid == "" {
				continue
			}
			for _, tid := range tagIDs {
				tid = strings.TrimSpace(tid)
				if tid == "" {
					continue
				}
				if op == "add" {
					var exists int
					_ = tx.QueryRow(ctx, `SELECT count(1) FROM node_tags WHERE node_id = ? AND tag_id = ?`, nid, tid).Scan(&exists)
					if exists == 0 {
						if _, err := tx.Exec(ctx, `INSERT INTO node_tags (node_id, tag_id) VALUES (?, ?)`, nid, tid); err != nil {
							return err
						}
					}
				} else {
					if _, err := tx.Exec(ctx, `DELETE FROM node_tags WHERE node_id = ? AND tag_id = ?`, nid, tid); err != nil {
						return err
					}
				}
			}
		}

		detail, _ := json.Marshal(map[string]any{"node_ids": nodeIDs, "tag_ids": tagIDs, "op": op})
		return audit.LogTx(ctx, tx, audit.Entry{
			ActorKind:  actorKind,
			ActorID:    actorID,
			Action:     "node.tags_batch",
			TargetKind: "node",
			Detail:     string(detail),
			Result:     "ok",
			IP:         ip,
		})
	})
	return err
}

// GetTagsByNodeID 获取单个节点的所有标签。
func (s *TagService) GetTagsByNodeID(ctx context.Context, nodeID string) ([]*Tag, error) {
	q := `SELECT t.id, t.name, t.color, t.created_at_ms, t.updated_at_ms
FROM node_tags nt
JOIN tags t ON nt.tag_id = t.id
WHERE nt.node_id = ?
ORDER BY t.name ASC`

	rows, err := s.db.Query(ctx, q, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*Tag
	for rows.Next() {
		var t Tag
		var color sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &color, &t.CreatedAtMs, &t.UpdatedAtMs); err != nil {
			return nil, err
		}
		if color.Valid {
			t.Color = &color.String
		}
		list = append(list, &t)
	}
	if list == nil {
		list = []*Tag{}
	}
	return list, rows.Err()
}

// GetNodeIDsByTagID 按标签反查关联的所有节点 ID。
func (s *TagService) GetNodeIDsByTagID(ctx context.Context, tagID string) ([]string, error) {
	q := `SELECT node_id FROM node_tags WHERE tag_id = ? ORDER BY node_id ASC`
	rows, err := s.db.Query(ctx, q, tagID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodeIDs []string
	for rows.Next() {
		var nid string
		if err := rows.Scan(&nid); err != nil {
			return nil, err
		}
		nodeIDs = append(nodeIDs, nid)
	}
	if nodeIDs == nil {
		nodeIDs = []string{}
	}
	return nodeIDs, rows.Err()
}

// HTTP Handlers

func (s *TagService) HandleList(w http.ResponseWriter, r *http.Request) {
	page, pageSize := ParsePagination(r)
	result, err := s.ListTags(r.Context(), page, pageSize)
	if err != nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "获取标签列表失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, result)
}

func (s *TagService) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var params CreateTagParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	t, err := s.CreateTag(r.Context(), params, actorKind, actorID, ip)
	if err != nil {
		if err.Error() == "name_empty" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "标签名称不能为空", "name")
			return
		}
		if err.Error() == "duplicate_name" {
			JSONError(w, http.StatusConflict, "duplicate_name", "标签名称已存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "创建标签失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, t)
}

func (s *TagService) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少标签 ID", "id")
		return
	}

	var params UpdateTagParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	t, err := s.UpdateTag(r.Context(), id, params, actorKind, actorID, ip)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "tag_not_found", "标签不存在", nil)
			return
		}
		if err.Error() == "name_empty" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "标签名称不能为空", "name")
			return
		}
		if err.Error() == "duplicate_name" {
			JSONError(w, http.StatusConflict, "duplicate_name", "标签名称已存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "更新标签失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, t)
}

func (s *TagService) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少标签 ID", "id")
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	err := s.DeleteTag(r.Context(), id, actorKind, actorID, ip)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "tag_not_found", "标签不存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "删除标签失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *TagService) HandleAttachNodeTag(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tagID := r.PathValue("tagId")
	if id == "" || tagID == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID 或标签 ID", nil)
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	err := s.AttachTag(r.Context(), id, tagID, actorKind, actorID, ip)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "node_not_found", "节点不存在", nil)
			return
		}
		if err.Error() == "tag_not_found" {
			JSONError(w, http.StatusNotFound, "tag_not_found", "标签不存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "节点打标签失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *TagService) HandleDetachNodeTag(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tagID := r.PathValue("tagId")
	if id == "" || tagID == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID 或标签 ID", nil)
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	err := s.DetachTag(r.Context(), id, tagID, actorKind, actorID, ip)
	if err != nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "节点移除标签失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, map[string]any{"ok": true})
}

type replaceTagsReq struct {
	TagIDs []string `json:"tag_ids"`
}

func (s *TagService) HandleReplaceNodeTags(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID", nil)
		return
	}

	var req replaceTagsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	err := s.ReplaceNodeTags(r.Context(), id, req.TagIDs, actorKind, actorID, ip)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "node_not_found", "节点不存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "替换节点标签失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, map[string]any{"ok": true})
}

type batchTagsReq struct {
	NodeIDs []string `json:"node_ids"`
	TagIDs  []string `json:"tag_ids"`
	Op      string   `json:"op"`
}

func (s *TagService) HandleBatchNodeTags(w http.ResponseWriter, r *http.Request) {
	var req batchTagsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	err := s.BatchTagNodes(r.Context(), req.NodeIDs, req.TagIDs, req.Op, actorKind, actorID, ip)
	if err != nil {
		if err.Error() == "invalid_op" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "操作类型仅支持 add 或 remove", "op")
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "批量打标签失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, map[string]any{"ok": true})
}
