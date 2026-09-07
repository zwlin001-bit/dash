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
	"dash/internal/events"
	"dash/internal/ulid"
)

// GroupInfo 概览列表中的简要分组信息。
type GroupInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// BillingSummary 概览列表中的简要计费信息。
type BillingSummary struct {
	ExpiresAtMs      *int64  `json:"expires_at_ms,omitempty"`
	TrafficLimit     *int64  `json:"traffic_limit,omitempty"`
	TrafficLimitKind *string `json:"traffic_limit_kind,omitempty"`
	Currency         *string `json:"currency,omitempty"`
	Price            *float64 `json:"price,omitempty"`
}

// Node 表示完整的节点信息。
type Node struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	NodeGroupID    *string         `json:"node_group_id,omitempty"`
	DisplayOrder   int             `json:"display_order"`
	IsHidden       bool            `json:"is_hidden"`
	AgentTokenHash *string         `json:"-"`
	AgentVersion   *string         `json:"agent_version,omitempty"`
	ConnState      string          `json:"conn_state"`
	LastSeenAtMs   *int64          `json:"last_seen_at_ms,omitempty"`
	ClockSkewMs    *int64          `json:"clock_skew_ms,omitempty"`
	Note           *string         `json:"note,omitempty"`
	CreatedAtMs    int64           `json:"created_at_ms"`
	UpdatedAtMs    int64           `json:"updated_at_ms"`

	// 扩展聚合信息（批量填充，防止前端 N+1 查询）
	Group   *GroupInfo      `json:"group,omitempty"`
	Tags    []*Tag          `json:"tags"`
	Billing *BillingSummary `json:"billing,omitempty"`
	Latest  any             `json:"latest,omitempty"`
	Facts   *NodeFacts      `json:"facts,omitempty"`
}

type NodeService struct {
	db           *db.DB
	tags         *TagService
	facts        *FactsService
	billing      *BillingService
	latestGetter func(nodeID string) (any, bool)
}

func NewNodeService(database *db.DB, tags *TagService, facts *FactsService, billing *BillingService) *NodeService {
	return &NodeService{
		db:      database,
		tags:    tags,
		facts:   facts,
		billing: billing,
	}
}

// SetLatestGetter 注入内存最新指标获取函数（从 Ingest LatestCache 纯内存读取）。
func (s *NodeService) SetLatestGetter(fn func(nodeID string) (any, bool)) {
	s.latestGetter = fn
}

type ListNodesFilter struct {
	GroupID   string
	TagID     string
	ConnState string
	Q         string
	IsHidden  *bool
	Sort      string
	Order     string
	Page      int
	PageSize  int
}

// ListNodes 查询节点列表，支持多条件过滤与批量关联字段装配。
func (s *NodeService) ListNodes(ctx context.Context, f ListNodesFilter) (*PageResult, error) {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = 50
	}
	if f.PageSize > 200 {
		f.PageSize = 200
	}

	whereClauses := []string{"1=1"}
	var args []any

	if f.GroupID != "" {
		whereClauses = append(whereClauses, "node_group_id = ?")
		args = append(args, f.GroupID)
	}

	if f.TagID != "" {
		whereClauses = append(whereClauses, "id IN (SELECT node_id FROM node_tags WHERE tag_id = ?)")
		args = append(args, f.TagID)
	}

	if f.ConnState != "" {
		whereClauses = append(whereClauses, "conn_state = ?")
		args = append(args, f.ConnState)
	}

	if f.Q != "" {
		qPattern := "%" + strings.TrimSpace(f.Q) + "%"
		whereClauses = append(whereClauses, "(name LIKE ? OR note LIKE ?)")
		args = append(args, qPattern, qPattern)
	}

	if f.IsHidden != nil {
		hVal := 0
		if *f.IsHidden {
			hVal = 1
		}
		whereClauses = append(whereClauses, "is_hidden = ?")
		args = append(args, hVal)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// 1. 查询总数
	countSQL := fmt.Sprintf("SELECT count(1) FROM nodes WHERE %s", whereSQL)
	var total int64
	if err := s.db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count nodes: %w", err)
	}

	// 2. 校验排序字段（防 SQL 注入）
	sortCol := "display_order"
	switch strings.ToLower(strings.TrimSpace(f.Sort)) {
	case "name":
		sortCol = "name"
	case "last_seen_at_ms":
		sortCol = "last_seen_at_ms"
	case "created_at_ms":
		sortCol = "created_at_ms"
	case "display_order":
		sortCol = "display_order"
	}

	orderDir := "ASC"
	if strings.EqualFold(strings.TrimSpace(f.Order), "desc") {
		orderDir = "DESC"
	}

	var orderClause string
	if sortCol == "display_order" {
		orderClause = fmt.Sprintf("display_order %s, name ASC", orderDir)
	} else {
		orderClause = fmt.Sprintf("%s %s", sortCol, orderDir)
	}

	// 3. 分页查询节点基础列
	querySQL := fmt.Sprintf(`SELECT id, name, node_group_id, display_order, is_hidden,
agent_version, conn_state, last_seen_at_ms, clock_skew_ms, note, created_at_ms, updated_at_ms
FROM nodes
WHERE %s
ORDER BY %s`, whereSQL, orderClause)

	offset := (f.Page - 1) * f.PageSize
	rows, err := s.db.QueryPage(ctx, querySQL, f.PageSize, offset, args...)
	if err != nil {
		return nil, fmt.Errorf("query nodes page: %w", err)
	}
	defer rows.Close()

	var nodes []*Node
	var nodeIDs []string
	groupIDSet := make(map[string]bool)

	for rows.Next() {
		var n Node
		var groupID, agentVer, note sql.NullString
		var lastSeen, clockSkew sql.NullInt64
		var isHiddenInt int

		err := rows.Scan(
			&n.ID, &n.Name, &groupID, &n.DisplayOrder, &isHiddenInt,
			&agentVer, &n.ConnState, &lastSeen, &clockSkew, &note,
			&n.CreatedAtMs, &n.UpdatedAtMs,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}

		n.IsHidden = (isHiddenInt == 1)
		if groupID.Valid {
			n.NodeGroupID = &groupID.String
			groupIDSet[groupID.String] = true
		}
		if agentVer.Valid {
			n.AgentVersion = &agentVer.String
		}
		if lastSeen.Valid {
			n.LastSeenAtMs = &lastSeen.Int64
		}
		if clockSkew.Valid {
			n.ClockSkewMs = &clockSkew.Int64
		}
		if note.Valid {
			n.Note = &note.String
		}

		n.Tags = []*Tag{}
		nodes = append(nodes, &n)
		nodeIDs = append(nodeIDs, n.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(nodes) == 0 {
		return &PageResult{
			Items:    []*Node{},
			Total:    total,
			Page:     f.Page,
			PageSize: f.PageSize,
		}, nil
	}

	// 4. 批量填充分组信息
	if len(groupIDSet) > 0 {
		groupsMap, err := s.batchGetGroups(ctx, groupIDSet)
		if err == nil {
			for _, n := range nodes {
				if n.NodeGroupID != nil {
					if g, ok := groupsMap[*n.NodeGroupID]; ok {
						n.Group = g
					}
				}
			}
		}
	}

	// 5. 批量填充标签信息
	tagsMap, err := s.batchGetTags(ctx, nodeIDs)
	if err == nil {
		for _, n := range nodes {
			if tList, ok := tagsMap[n.ID]; ok {
				n.Tags = tList
			}
		}
	}

	// 6. 批量填充计费信息
	billingMap, err := s.batchGetBilling(ctx, nodeIDs)
	if err == nil {
		for _, n := range nodes {
			if b, ok := billingMap[n.ID]; ok {
				n.Billing = b
			}
		}
	}

	// 7. 批量装配 Latest 内存最新指标（仅对在线节点装配，离线/未上线节点留空，契约 docs/12-api-spec.md §4）
	if s.latestGetter != nil {
		for _, n := range nodes {
			if n.ConnState == "online" {
				if lat, ok := s.latestGetter(n.ID); ok {
					n.Latest = lat
				}
			}
		}
	}

	return &PageResult{
		Items:    nodes,
		Total:    total,
		Page:     f.Page,
		PageSize: f.PageSize,
	}, nil
}

func (s *NodeService) batchGetGroups(ctx context.Context, groupIDSet map[string]bool) (map[string]*GroupInfo, error) {
	if len(groupIDSet) == 0 {
		return nil, nil
	}
	var placeholders []string
	var args []any
	for gid := range groupIDSet {
		placeholders = append(placeholders, "?")
		args = append(args, gid)
	}

	q := fmt.Sprintf("SELECT id, name FROM node_groups WHERE id IN (%s)", strings.Join(placeholders, ","))
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]*GroupInfo)
	for rows.Next() {
		var g GroupInfo
		if err := rows.Scan(&g.ID, &g.Name); err == nil {
			res[g.ID] = &g
		}
	}
	return res, nil
}

func (s *NodeService) batchGetTags(ctx context.Context, nodeIDs []string) (map[string][]*Tag, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var placeholders []string
	var args []any
	for _, nid := range nodeIDs {
		placeholders = append(placeholders, "?")
		args = append(args, nid)
	}

	q := fmt.Sprintf(`SELECT nt.node_id, t.id, t.name, t.color, t.created_at_ms, t.updated_at_ms
FROM node_tags nt
JOIN tags t ON nt.tag_id = t.id
WHERE nt.node_id IN (%s)
ORDER BY t.name ASC`, strings.Join(placeholders, ","))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string][]*Tag)
	for rows.Next() {
		var nid string
		var t Tag
		var color sql.NullString
		if err := rows.Scan(&nid, &t.ID, &t.Name, &color, &t.CreatedAtMs, &t.UpdatedAtMs); err == nil {
			if color.Valid {
				t.Color = &color.String
			}
			res[nid] = append(res[nid], &t)
		}
	}
	return res, nil
}

func (s *NodeService) batchGetBilling(ctx context.Context, nodeIDs []string) (map[string]*BillingSummary, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var placeholders []string
	var args []any
	for _, nid := range nodeIDs {
		placeholders = append(placeholders, "?")
		args = append(args, nid)
	}

	q := fmt.Sprintf(`SELECT node_id, expires_at_ms, traffic_limit, traffic_limit_kind, currency, price
FROM node_billing
WHERE node_id IN (%s)`, strings.Join(placeholders, ","))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]*BillingSummary)
	for rows.Next() {
		var nid string
		var b BillingSummary
		var expiresAt, trafficLimit sql.NullInt64
		var trafficKind, currency sql.NullString
		var price sql.NullFloat64

		if err := rows.Scan(&nid, &expiresAt, &trafficLimit, &trafficKind, &currency, &price); err == nil {
			if expiresAt.Valid {
				b.ExpiresAtMs = &expiresAt.Int64
			}
			if trafficLimit.Valid {
				b.TrafficLimit = &trafficLimit.Int64
			}
			if trafficKind.Valid {
				b.TrafficLimitKind = &trafficKind.String
			}
			if currency.Valid {
				b.Currency = &currency.String
			}
			if price.Valid {
				b.Price = &price.Float64
			}
			res[nid] = &b
		}
	}
	return res, nil
}

// GetNode 获取单个节点详情（含 Group、Tags、Facts、Billing）。
func (s *NodeService) GetNode(ctx context.Context, id string) (*Node, error) {
	q := `SELECT id, name, node_group_id, display_order, is_hidden,
agent_version, conn_state, last_seen_at_ms, clock_skew_ms, note, created_at_ms, updated_at_ms
FROM nodes WHERE id = ?`

	var n Node
	var groupID, agentVer, note sql.NullString
	var lastSeen, clockSkew sql.NullInt64
	var isHiddenInt int

	row := s.db.QueryRow(ctx, q, id)
	err := row.Scan(
		&n.ID, &n.Name, &groupID, &n.DisplayOrder, &isHiddenInt,
		&agentVer, &n.ConnState, &lastSeen, &clockSkew, &note,
		&n.CreatedAtMs, &n.UpdatedAtMs,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}

	n.IsHidden = (isHiddenInt == 1)
	if groupID.Valid {
		n.NodeGroupID = &groupID.String
		// 获取 Group 详情
		var g GroupInfo
		if err := s.db.QueryRow(ctx, `SELECT id, name FROM node_groups WHERE id = ?`, groupID.String).Scan(&g.ID, &g.Name); err == nil {
			n.Group = &g
		}
	}
	if agentVer.Valid {
		n.AgentVersion = &agentVer.String
	}
	if lastSeen.Valid {
		n.LastSeenAtMs = &lastSeen.Int64
	}
	if clockSkew.Valid {
		n.ClockSkewMs = &clockSkew.Int64
	}
	if note.Valid {
		n.Note = &note.String
	}

	// 填充标签
	tags, err := s.tags.GetTagsByNodeID(ctx, id)
	if err == nil {
		n.Tags = tags
	} else {
		n.Tags = []*Tag{}
	}

	// 填充 Facts
	facts, err := s.facts.GetFacts(ctx, id)
	if err == nil {
		n.Facts = facts
	}

	// 填充 Billing
	billing, err := s.billing.GetBilling(ctx, id)
	if err == nil && billing != nil {
		n.Billing = &BillingSummary{
			ExpiresAtMs:      billing.ExpiresAtMs,
			TrafficLimit:     billing.TrafficLimit,
			TrafficLimitKind: billing.TrafficLimitKind,
			Currency:         billing.Currency,
			Price:            billing.Price,
		}
	}

	// 填充 Latest 内存最新指标（仅在线节点装配）
	if n.ConnState == "online" && s.latestGetter != nil {
		if lat, ok := s.latestGetter(id); ok {
			n.Latest = lat
		}
	}

	return &n, nil
}

type CreateNodeParams struct {
	Name         string  `json:"name"`
	NodeGroupID  *string `json:"node_group_id"`
	DisplayOrder int     `json:"display_order"`
	IsHidden     bool    `json:"is_hidden"`
	Note         *string `json:"note"`
}

// CreateNode 手工创建节点并写审计日志。
func (s *NodeService) CreateNode(ctx context.Context, params CreateNodeParams, actorKind, actorID, ip string) (*Node, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, errors.New("name_empty")
	}

	// 如果指定了分组，验证其是否存在
	if params.NodeGroupID != nil && *params.NodeGroupID != "" {
		var groupExists int
		_ = s.db.QueryRow(ctx, `SELECT count(1) FROM node_groups WHERE id = ?`, *params.NodeGroupID).Scan(&groupExists)
		if groupExists == 0 {
			return nil, errors.New("group_not_found")
		}
	}

	id := ulid.New()
	now := time.Now().UnixMilli()

	hiddenVal := 0
	if params.IsHidden {
		hiddenVal = 1
	}

	var groupVal, noteVal any
	if params.NodeGroupID != nil && strings.TrimSpace(*params.NodeGroupID) != "" {
		groupVal = strings.TrimSpace(*params.NodeGroupID)
	}
	if params.Note != nil && strings.TrimSpace(*params.Note) != "" {
		noteVal = strings.TrimSpace(*params.Note)
	}

	q := `INSERT INTO nodes (
id, name, node_group_id, display_order, is_hidden, conn_state, note, created_at_ms, updated_at_ms
) VALUES (?, ?, ?, ?, ?, 'never', ?, ?, ?)`

	_, err := s.db.Exec(ctx, q, id, name, groupVal, params.DisplayOrder, hiddenVal, noteVal, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert node: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "node.create",
		TargetKind: "node",
		TargetID:   id,
		Detail:     fmt.Sprintf(`{"name":%q}`, name),
		Result:     "ok",
		IP:         ip,
	})

	return s.GetNode(ctx, id)
}

type UpdateNodeParams struct {
	Name         *string `json:"name"`
	NodeGroupID  *string `json:"node_group_id"`
	DisplayOrder *int    `json:"display_order"`
	IsHidden     *bool   `json:"is_hidden"`
	Note         *string `json:"note"`
}

// UpdateNode 部分更新节点属性并写审计日志。
func (s *NodeService) UpdateNode(ctx context.Context, id string, params UpdateNodeParams, actorKind, actorID, ip string) (*Node, error) {
	node, err := s.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()
	if params.Name != nil {
		name := strings.TrimSpace(*params.Name)
		if name == "" {
			return nil, errors.New("name_empty")
		}
		node.Name = name
	}
	if params.NodeGroupID != nil {
		gid := strings.TrimSpace(*params.NodeGroupID)
		if gid != "" {
			var groupExists int
			_ = s.db.QueryRow(ctx, `SELECT count(1) FROM node_groups WHERE id = ?`, gid).Scan(&groupExists)
			if groupExists == 0 {
				return nil, errors.New("group_not_found")
			}
			node.NodeGroupID = &gid
		} else {
			node.NodeGroupID = nil
		}
	}
	if params.DisplayOrder != nil {
		node.DisplayOrder = *params.DisplayOrder
	}
	if params.IsHidden != nil {
		node.IsHidden = *params.IsHidden
	}
	if params.Note != nil {
		n := strings.TrimSpace(*params.Note)
		if n != "" {
			node.Note = &n
		} else {
			node.Note = nil
		}
	}
	node.UpdatedAtMs = now

	hiddenVal := 0
	if node.IsHidden {
		hiddenVal = 1
	}

	var groupVal, noteVal any
	if node.NodeGroupID != nil {
		groupVal = *node.NodeGroupID
	}
	if node.Note != nil {
		noteVal = *node.Note
	}

	q := `UPDATE nodes SET
name = ?, node_group_id = ?, display_order = ?, is_hidden = ?, note = ?, updated_at_ms = ?
WHERE id = ?`

	_, err = s.db.Exec(ctx, q, node.Name, groupVal, node.DisplayOrder, hiddenVal, noteVal, node.UpdatedAtMs, id)
	if err != nil {
		return nil, fmt.Errorf("update node: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "node.update",
		TargetKind: "node",
		TargetID:   id,
		Detail:     fmt.Sprintf(`{"name":%q}`, node.Name),
		Result:     "ok",
		IP:         ip,
	})

	return s.GetNode(ctx, id)
}

// DeleteNode 级联清理节点关联数据（node_tags -> node_facts -> node_billing -> nodes）。
// 时序数据不同步删，交给保留期自然过期（防长事务）。
func (s *NodeService) DeleteNode(ctx context.Context, id string, actorKind, actorID, ip string) error {
	node, err := s.GetNode(ctx, id)
	if err != nil {
		return err
	}

	err = s.db.WithTx(ctx, func(tx *db.Tx) error {
		// 1. 删除 node_tags
		if _, err := tx.Exec(ctx, `DELETE FROM node_tags WHERE node_id = ?`, id); err != nil {
			return fmt.Errorf("delete node_tags: %w", err)
		}

		// 2. 删除 node_facts
		if _, err := tx.Exec(ctx, `DELETE FROM node_facts WHERE node_id = ?`, id); err != nil {
			return fmt.Errorf("delete node_facts: %w", err)
		}

		// 3. 删除 node_billing
		if _, err := tx.Exec(ctx, `DELETE FROM node_billing WHERE node_id = ?`, id); err != nil {
			return fmt.Errorf("delete node_billing: %w", err)
		}

		// 4. 删除 nodes
		if _, err := tx.Exec(ctx, `DELETE FROM nodes WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete node: %w", err)
		}

		// 5. 记录审计日志
		return audit.LogTx(ctx, tx, audit.Entry{
			ActorKind:  actorKind,
			ActorID:    actorID,
			Action:     "node.delete",
			TargetKind: "node",
			TargetID:   id,
			Detail:     fmt.Sprintf(`{"name":%q}`, node.Name),
			Result:     "ok",
			IP:         ip,
		})
	})
	if err != nil {
		return err
	}

	operator := actorID
	if operator == "" {
		operator = actorKind
	}
	events.Emit(ctx, events.Event{
		Type:       "node.removed",
		Source:     "inventory",
		TargetKind: "node",
		TargetID:   id,
		Title:      fmt.Sprintf("节点 %s 已删除", node.Name),
		DedupKey:   fmt.Sprintf("node.removed:%s", id),
		Payload: map[string]any{
			"node_name": node.Name,
			"operator":  operator,
		},
	})

	return nil
}

// RevokeAgentToken 吊销节点的 Agent 长期令牌。
func (s *NodeService) RevokeAgentToken(ctx context.Context, id string, actorKind, actorID, ip string) error {
	var exists int
	_ = s.db.QueryRow(ctx, `SELECT count(1) FROM nodes WHERE id = ?`, id).Scan(&exists)
	if exists == 0 {
		return db.ErrNotFound
	}

	now := time.Now().UnixMilli()
	q := `UPDATE nodes SET agent_token_hash = NULL, updated_at_ms = ? WHERE id = ?`
	if _, err := s.db.Exec(ctx, q, now, id); err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "node.token_revoke",
		TargetKind: "node",
		TargetID:   id,
		Result:     "ok",
		IP:         ip,
	})
	return nil
}

// HTTP Handlers

func (s *NodeService) HandleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, pageSize := ParsePagination(r)

	filter := ListNodesFilter{
		GroupID:   strings.TrimSpace(q.Get("group_id")),
		TagID:     strings.TrimSpace(q.Get("tag_id")),
		ConnState: strings.TrimSpace(q.Get("conn_state")),
		Q:         strings.TrimSpace(q.Get("q")),
		Sort:      strings.TrimSpace(q.Get("sort")),
		Order:     strings.TrimSpace(q.Get("order")),
		Page:      page,
		PageSize:  pageSize,
	}
	if hiddenStr := q.Get("is_hidden"); hiddenStr != "" {
		h := (hiddenStr == "1" || strings.EqualFold(hiddenStr, "true"))
		filter.IsHidden = &h
	}

	result, err := s.ListNodes(r.Context(), filter)
	if err != nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "获取节点列表失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, result)
}

func (s *NodeService) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID", "id")
		return
	}

	node, err := s.GetNode(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "node_not_found", "节点不存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "获取节点详情失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, node)
}

func (s *NodeService) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var params CreateNodeParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	node, err := s.CreateNode(r.Context(), params, actorKind, actorID, ip)
	if err != nil {
		if err.Error() == "name_empty" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "节点名称不能为空", "name")
			return
		}
		if err.Error() == "group_not_found" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "指定的分组不存在", "node_group_id")
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "创建节点失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, node)
}

func (s *NodeService) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID", "id")
		return
	}

	var params UpdateNodeParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	node, err := s.UpdateNode(r.Context(), id, params, actorKind, actorID, ip)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "node_not_found", "节点不存在", nil)
			return
		}
		if err.Error() == "name_empty" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "节点名称不能为空", "name")
			return
		}
		if err.Error() == "group_not_found" {
			JSONError(w, http.StatusBadRequest, "invalid_param", "指定的分组不存在", "node_group_id")
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "更新节点失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, node)
}

func (s *NodeService) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID", "id")
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	if err := s.DeleteNode(r.Context(), id, actorKind, actorID, ip); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "node_not_found", "节点不存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "删除节点失败", nil)
		return
	}

	JSONSuccess(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "节点已删除，时序监控数据将在保留期后自动过期清理",
	})
}

func (s *NodeService) HandleRevokeToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID", "id")
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	if err := s.RevokeAgentToken(r.Context(), id, actorKind, actorID, ip); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "node_not_found", "节点不存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "吊销令牌失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, map[string]any{"ok": true})
}
