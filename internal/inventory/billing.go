package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"dash/internal/audit"
	"dash/internal/db"
)

// NodeBilling 对应 node_billing 表，记录节点计费与周期、流量配额信息。
type NodeBilling struct {
	NodeID           string   `json:"node_id"`
	Currency         *string  `json:"currency,omitempty"`
	Price            *float64 `json:"price,omitempty"`
	CycleDays        *int     `json:"cycle_days,omitempty"`
	IsAutoRenew      bool     `json:"is_auto_renew"`
	ExpiresAtMs      *int64   `json:"expires_at_ms,omitempty"`
	TrafficLimit     *int64   `json:"traffic_limit,omitempty"`
	TrafficLimitKind *string  `json:"traffic_limit_kind,omitempty"`
	TrafficResetDay  *int     `json:"traffic_reset_day,omitempty"`
	UpdatedAtMs      int64    `json:"updated_at_ms"`
}

type BillingService struct {
	db *db.DB
}

func NewBillingService(database *db.DB) *BillingService {
	return &BillingService{db: database}
}

// GetBilling 获取节点计费详情。
func (s *BillingService) GetBilling(ctx context.Context, nodeID string) (*NodeBilling, error) {
	q := `SELECT node_id, currency, price, cycle_days, is_auto_renew, expires_at_ms,
traffic_limit, traffic_limit_kind, traffic_reset_day, updated_at_ms
FROM node_billing WHERE node_id = ?`

	var b NodeBilling
	var currency, trafficKind sql.NullString
	var price sql.NullFloat64
	var cycleDays, autoRenew, resetDay sql.NullInt32
	var expiresAt, trafficLimit sql.NullInt64

	row := s.db.QueryRow(ctx, q, nodeID)
	err := row.Scan(
		&b.NodeID, &currency, &price, &cycleDays, &autoRenew, &expiresAt,
		&trafficLimit, &trafficKind, &resetDay, &b.UpdatedAtMs,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}

	if currency.Valid {
		b.Currency = &currency.String
	}
	if price.Valid {
		b.Price = &price.Float64
	}
	if cycleDays.Valid {
		v := int(cycleDays.Int32)
		b.CycleDays = &v
	}
	if autoRenew.Valid {
		b.IsAutoRenew = (autoRenew.Int32 == 1)
	}
	if expiresAt.Valid {
		b.ExpiresAtMs = &expiresAt.Int64
	}
	if trafficLimit.Valid {
		b.TrafficLimit = &trafficLimit.Int64
	}
	if trafficKind.Valid {
		b.TrafficLimitKind = &trafficKind.String
	}
	if resetDay.Valid {
		v := int(resetDay.Int32)
		b.TrafficResetDay = &v
	}

	return &b, nil
}

type UpdateBillingParams struct {
	Currency         *string  `json:"currency"`
	Price            *float64 `json:"price"`
	CycleDays        *int     `json:"cycle_days"`
	IsAutoRenew      *bool    `json:"is_auto_renew"`
	ExpiresAtMs      *int64   `json:"expires_at_ms"`
	TrafficLimit     *int64   `json:"traffic_limit"`
	TrafficLimitKind *string  `json:"traffic_limit_kind"`
	TrafficResetDay  *int     `json:"traffic_reset_day"`
}

// SaveBilling 增量更新或新建节点计费信息（部分更新）。
func (s *BillingService) SaveBilling(ctx context.Context, nodeID string, params UpdateBillingParams, actorKind, actorID, ip string) (*NodeBilling, error) {
	// 验证节点是否存在
	var nodeExists int
	_ = s.db.QueryRow(ctx, `SELECT count(1) FROM nodes WHERE id = ?`, nodeID).Scan(&nodeExists)
	if nodeExists == 0 {
		return nil, db.ErrNotFound
	}

	existing, err := s.GetBilling(ctx, nodeID)
	now := time.Now().UnixMilli()

	if err != nil && errors.Is(err, db.ErrNotFound) {
		// 新建
		existing = &NodeBilling{
			NodeID:      nodeID,
			UpdatedAtMs: now,
		}
	}

	if params.Currency != nil {
		existing.Currency = params.Currency
	}
	if params.Price != nil {
		existing.Price = params.Price
	}
	if params.CycleDays != nil {
		existing.CycleDays = params.CycleDays
	}
	if params.IsAutoRenew != nil {
		existing.IsAutoRenew = *params.IsAutoRenew
	}
	if params.ExpiresAtMs != nil {
		existing.ExpiresAtMs = params.ExpiresAtMs
	}
	if params.TrafficLimit != nil {
		existing.TrafficLimit = params.TrafficLimit
	}
	if params.TrafficLimitKind != nil {
		existing.TrafficLimitKind = params.TrafficLimitKind
	}
	if params.TrafficResetDay != nil {
		existing.TrafficResetDay = params.TrafficResetDay
	}
	existing.UpdatedAtMs = now

	autoRenewVal := 0
	if existing.IsAutoRenew {
		autoRenewVal = 1
	}

	var exists int
	_ = s.db.QueryRow(ctx, `SELECT count(1) FROM node_billing WHERE node_id = ?`, nodeID).Scan(&exists)

	if exists > 0 {
		q := `UPDATE node_billing SET
currency = ?, price = ?, cycle_days = ?, is_auto_renew = ?, expires_at_ms = ?,
traffic_limit = ?, traffic_limit_kind = ?, traffic_reset_day = ?, updated_at_ms = ?
WHERE node_id = ?`
		_, err = s.db.Exec(ctx, q,
			existing.Currency, existing.Price, existing.CycleDays, autoRenewVal, existing.ExpiresAtMs,
			existing.TrafficLimit, existing.TrafficLimitKind, existing.TrafficResetDay, existing.UpdatedAtMs,
			nodeID,
		)
	} else {
		q := `INSERT INTO node_billing (
node_id, currency, price, cycle_days, is_auto_renew, expires_at_ms,
traffic_limit, traffic_limit_kind, traffic_reset_day, updated_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		_, err = s.db.Exec(ctx, q,
			nodeID, existing.Currency, existing.Price, existing.CycleDays, autoRenewVal, existing.ExpiresAtMs,
			existing.TrafficLimit, existing.TrafficLimitKind, existing.TrafficResetDay, existing.UpdatedAtMs,
		)
	}

	if err != nil {
		return nil, fmt.Errorf("save billing: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "node.billing_update",
		TargetKind: "node",
		TargetID:   nodeID,
		Result:     "ok",
		IP:         ip,
	})

	return existing, nil
}

// DeleteBilling 删除节点的计费信息。
func (s *BillingService) DeleteBilling(ctx context.Context, nodeID string, actorKind, actorID, ip string) error {
	q := `DELETE FROM node_billing WHERE node_id = ?`
	_, err := s.db.Exec(ctx, q, nodeID)
	if err != nil {
		return fmt.Errorf("delete billing: %w", err)
	}

	_ = audit.Log(ctx, s.db, audit.Entry{
		ActorKind:  actorKind,
		ActorID:    actorID,
		Action:     "node.billing_delete",
		TargetKind: "node",
		TargetID:   nodeID,
		Result:     "ok",
		IP:         ip,
	})
	return nil
}

func (s *BillingService) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID", "id")
		return
	}

	billing, err := s.GetBilling(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONSuccess(w, http.StatusOK, nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "获取计费信息失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, billing)
}

func (s *BillingService) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID", "id")
		return
	}

	var params UpdateBillingParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid_param", "请求体解析失败", nil)
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	b, err := s.SaveBilling(r.Context(), id, params, actorKind, actorID, ip)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			JSONError(w, http.StatusNotFound, "node_not_found", "节点不存在", nil)
			return
		}
		JSONError(w, http.StatusInternalServerError, "server_error", "更新计费信息失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, b)
}

func (s *BillingService) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		JSONError(w, http.StatusBadRequest, "invalid_param", "缺少节点 ID", "id")
		return
	}

	actorKind, actorID, ip := ActorInfo(r)
	if err := s.DeleteBilling(r.Context(), id, actorKind, actorID, ip); err != nil {
		JSONError(w, http.StatusInternalServerError, "server_error", "删除计费信息失败", nil)
		return
	}
	JSONSuccess(w, http.StatusOK, map[string]any{"ok": true})
}
