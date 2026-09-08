package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"dash/internal/db"
	"dash/internal/ulid"
)

// Store handles database persistence for cloud billing.
type Store struct {
	db *db.DB
}

// NewStore creates a new billing Store.
func NewStore(database *db.DB) *Store {
	return &Store{db: database}
}

// DB returns the underlying DB instance.
func (s *Store) DB() *db.DB {
	return s.db
}

// UpsertPeriod inserts or updates a bill_periods row.
func (s *Store) UpsertPeriod(ctx context.Context, p *BillPeriod) error {
	if s.db == nil {
		return errors.New("billing: db is nil")
	}
	nowMs := time.Now().UnixMilli()
	if p.CreatedAtMs == 0 {
		p.CreatedAtMs = nowMs
	}
	p.UpdatedAtMs = nowMs

	if p.ID == "" {
		p.ID = ulid.New()
	}

	// Portable check and insert/update
	var existingID string
	checkQ := "SELECT id FROM bill_periods WHERE cloud_account_id = ? AND period = ?"
	err := s.db.QueryRow(ctx, checkQ, p.CloudAccountID, p.Period).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		insQ := `INSERT INTO bill_periods (
			id, cloud_account_id, period, currency, total_amount,
			pretax_amount, discount_amount, sync_state, synced_at_ms,
			error_text, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		_, err := s.db.Exec(ctx, insQ,
			p.ID, p.CloudAccountID, p.Period, p.Currency, p.TotalAmount,
			p.PretaxAmount, p.DiscountAmount, p.SyncState, p.SyncedAtMs,
			p.ErrorText, p.CreatedAtMs, p.UpdatedAtMs,
		)
		return err
	} else if err != nil {
		return err
	}

	p.ID = existingID
	updQ := `UPDATE bill_periods SET
		currency = ?, total_amount = ?, pretax_amount = ?, discount_amount = ?,
		sync_state = ?, synced_at_ms = ?, error_text = ?, updated_at_ms = ?
	WHERE id = ?`
	_, err = s.db.Exec(ctx, updQ,
		p.Currency, p.TotalAmount, p.PretaxAmount, p.DiscountAmount,
		p.SyncState, p.SyncedAtMs, p.ErrorText, p.UpdatedAtMs, p.ID,
	)
	return err
}

// GetPeriod retrieves a bill_periods row by cloud_account_id and period.
func (s *Store) GetPeriod(ctx context.Context, accountID, period string) (*BillPeriod, error) {
	if s.db == nil {
		return nil, errors.New("billing: db is nil")
	}

	q := `SELECT id, cloud_account_id, period, currency, total_amount,
		pretax_amount, discount_amount, sync_state, synced_at_ms,
		error_text, created_at_ms, updated_at_ms
	FROM bill_periods
	WHERE cloud_account_id = ? AND period = ?`

	var p BillPeriod
	var pretax, discount sql.NullFloat64
	var syncedAt sql.NullInt64
	var errText sql.NullString

	err := s.db.QueryRow(ctx, q, accountID, period).Scan(
		&p.ID, &p.CloudAccountID, &p.Period, &p.Currency, &p.TotalAmount,
		&pretax, &discount, &p.SyncState, &syncedAt,
		&errText, &p.CreatedAtMs, &p.UpdatedAtMs,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if pretax.Valid {
		p.PretaxAmount = &pretax.Float64
	}
	if discount.Valid {
		p.DiscountAmount = &discount.Float64
	}
	if syncedAt.Valid {
		p.SyncedAtMs = &syncedAt.Int64
	}
	if errText.Valid {
		p.ErrorText = &errText.String
	}

	return &p, nil
}

// ListPeriods retrieves all bill_periods rows for a given period.
func (s *Store) ListPeriods(ctx context.Context, period string) ([]*BillPeriod, error) {
	if s.db == nil {
		return nil, errors.New("billing: db is nil")
	}

	q := `SELECT bp.id, bp.cloud_account_id, bp.period, bp.currency, bp.total_amount,
		bp.pretax_amount, bp.discount_amount, bp.sync_state, bp.synced_at_ms,
		bp.error_text, bp.created_at_ms, bp.updated_at_ms, ca.name
	FROM bill_periods bp
	LEFT JOIN cloud_accounts ca ON bp.cloud_account_id = ca.id
	WHERE bp.period = ?
	ORDER BY bp.cloud_account_id ASC`

	rows, err := s.db.Query(ctx, q, period)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*BillPeriod
	for rows.Next() {
		var p BillPeriod
		var pretax, discount sql.NullFloat64
		var syncedAt sql.NullInt64
		var errText, accName sql.NullString

		if err := rows.Scan(
			&p.ID, &p.CloudAccountID, &p.Period, &p.Currency, &p.TotalAmount,
			&pretax, &discount, &p.SyncState, &syncedAt,
			&errText, &p.CreatedAtMs, &p.UpdatedAtMs, &accName,
		); err != nil {
			return nil, err
		}

		if pretax.Valid {
			p.PretaxAmount = &pretax.Float64
		}
		if discount.Valid {
			p.DiscountAmount = &discount.Float64
		}
		if syncedAt.Valid {
			p.SyncedAtMs = &syncedAt.Int64
		}
		if errText.Valid {
			p.ErrorText = &errText.String
		}
		if accName.Valid {
			p.AccountName = accName.String
		}

		res = append(res, &p)
	}

	return res, rows.Err()
}

// ListPeriodsByRange retrieves all bill_periods rows whose period is between startPeriod and endPeriod inclusive.
func (s *Store) ListPeriodsByRange(ctx context.Context, startPeriod, endPeriod string) ([]*BillPeriod, error) {
	if s.db == nil {
		return nil, errors.New("billing: db is nil")
	}

	q := `SELECT id, cloud_account_id, period, currency, total_amount,
		pretax_amount, discount_amount, sync_state, synced_at_ms,
		error_text, created_at_ms, updated_at_ms
	FROM bill_periods
	WHERE period >= ? AND period <= ?
	ORDER BY period ASC, cloud_account_id ASC`

	rows, err := s.db.Query(ctx, q, startPeriod, endPeriod)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*BillPeriod
	for rows.Next() {
		var p BillPeriod
		var pretax, discount sql.NullFloat64
		var syncedAt sql.NullInt64
		var errText sql.NullString

		if err := rows.Scan(
			&p.ID, &p.CloudAccountID, &p.Period, &p.Currency, &p.TotalAmount,
			&pretax, &discount, &p.SyncState, &syncedAt,
			&errText, &p.CreatedAtMs, &p.UpdatedAtMs,
		); err != nil {
			return nil, err
		}

		if pretax.Valid {
			p.PretaxAmount = &pretax.Float64
		}
		if discount.Valid {
			p.DiscountAmount = &discount.Float64
		}
		if syncedAt.Valid {
			p.SyncedAtMs = &syncedAt.Int64
		}
		if errText.Valid {
			p.ErrorText = &errText.String
		}

		res = append(res, &p)
	}

	return res, rows.Err()
}

// ReplaceItems removes all items for (accountID, period) and inserts the new ones.
func (s *Store) ReplaceItems(ctx context.Context, accountID, period string, items []BillItem) error {
	if s.db == nil {
		return errors.New("billing: db is nil")
	}

	return s.db.WithTx(ctx, func(tx *db.Tx) error {
		delQ := "DELETE FROM bill_items WHERE cloud_account_id = ? AND period = ?"
		if _, err := tx.Exec(ctx, delQ, accountID, period); err != nil {
			return fmt.Errorf("delete old bill_items: %w", err)
		}

		nowMs := time.Now().UnixMilli()
		insQ := `INSERT INTO bill_items (
			id, cloud_account_id, period, res_kind, res_ref,
			cloud_resource_id, item_name, product_code, currency,
			amount, usage_text, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

		for _, it := range items {
			itemID := it.ID
			if itemID == "" {
				itemID = ulid.New()
			}
			cAt := it.CreatedAtMs
			if cAt == 0 {
				cAt = nowMs
			}

			if _, err := tx.Exec(ctx, insQ,
				itemID, accountID, period, it.ResKind, it.ResRef,
				it.CloudResourceID, it.ItemName, it.ProductCode, it.Currency,
				it.Amount, it.UsageText, cAt, nowMs,
			); err != nil {
				return fmt.Errorf("insert bill_item: %w", err)
			}
		}

		return nil
	})
}

// ListItems queries items for a period, optionally filtered by cloud_account_id.
func (s *Store) ListItems(ctx context.Context, period string, accountID string) ([]BillItem, error) {
	if s.db == nil {
		return nil, errors.New("billing: db is nil")
	}

	q := `SELECT bi.id, bi.cloud_account_id, bi.period, bi.res_kind, bi.res_ref,
		bi.cloud_resource_id, bi.item_name, bi.product_code, bi.currency,
		bi.amount, bi.usage_text, bi.created_at_ms, bi.updated_at_ms,
		ca.name
	FROM bill_items bi
	LEFT JOIN cloud_accounts ca ON bi.cloud_account_id = ca.id
	WHERE bi.period = ?`
	args := []any{period}

	if accountID != "" {
		q += " AND bi.cloud_account_id = ?"
		args = append(args, accountID)
	}
	q += " ORDER BY bi.amount DESC, bi.id ASC"

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []BillItem
	for rows.Next() {
		var it BillItem
		var resRef, resID, name, pCode, usage, accName sql.NullString

		if err := rows.Scan(
			&it.ID, &it.CloudAccountID, &it.Period, &it.ResKind, &resRef,
			&resID, &name, &pCode, &it.Currency,
			&it.Amount, &usage, &it.CreatedAtMs, &it.UpdatedAtMs,
			&accName,
		); err != nil {
			return nil, err
		}

		if resRef.Valid {
			it.ResRef = &resRef.String
		}
		if resID.Valid {
			it.CloudResourceID = &resID.String
		}
		if name.Valid {
			it.ItemName = &name.String
		}
		if pCode.Valid {
			it.ProductCode = &pCode.String
		}
		if usage.Valid {
			it.UsageText = &usage.String
		}
		if accName.Valid {
			it.AccountName = accName.String
		}

		items = append(items, it)
	}

	return items, rows.Err()
}

// Budget CRUD

// CreateBudget creates a new budget.
func (s *Store) CreateBudget(ctx context.Context, b *BillBudget) error {
	if s.db == nil {
		return errors.New("billing: db is nil")
	}
	nowMs := time.Now().UnixMilli()
	if b.ID == "" {
		b.ID = ulid.New()
	}
	b.CreatedAtMs = nowMs
	b.UpdatedAtMs = nowMs

	if b.PeriodKind == "" {
		b.PeriodKind = "month"
	}
	if b.WarnRatio <= 0 {
		b.WarnRatio = 0.8
	}

	isEnInt := 0
	if b.IsEnabled {
		isEnInt = 1
	}

	q := `INSERT INTO bill_budgets (
		id, scope_kind, scope_ref, period_kind, currency,
		amount, warn_ratio, is_enabled, created_at_ms, updated_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(ctx, q,
		b.ID, b.ScopeKind, b.ScopeRef, b.PeriodKind, b.Currency,
		b.Amount, b.WarnRatio, isEnInt, b.CreatedAtMs, b.UpdatedAtMs,
	)
	return err
}

// UpdateBudget updates an existing budget.
func (s *Store) UpdateBudget(ctx context.Context, b *BillBudget) error {
	if s.db == nil {
		return errors.New("billing: db is nil")
	}
	b.UpdatedAtMs = time.Now().UnixMilli()

	isEnInt := 0
	if b.IsEnabled {
		isEnInt = 1
	}

	q := `UPDATE bill_budgets SET
		scope_kind = ?, scope_ref = ?, period_kind = ?, currency = ?,
		amount = ?, warn_ratio = ?, is_enabled = ?, updated_at_ms = ?
	WHERE id = ?`

	_, err := s.db.Exec(ctx, q,
		b.ScopeKind, b.ScopeRef, b.PeriodKind, b.Currency,
		b.Amount, b.WarnRatio, isEnInt, b.UpdatedAtMs, b.ID,
	)
	return err
}

// DeleteBudget deletes a budget by ID.
func (s *Store) DeleteBudget(ctx context.Context, id string) error {
	if s.db == nil {
		return errors.New("billing: db is nil")
	}
	_, err := s.db.Exec(ctx, "DELETE FROM bill_budgets WHERE id = ?", id)
	return err
}

// GetBudget retrieves a budget by ID.
func (s *Store) GetBudget(ctx context.Context, id string) (*BillBudget, error) {
	if s.db == nil {
		return nil, errors.New("billing: db is nil")
	}

	q := `SELECT id, scope_kind, scope_ref, period_kind, currency,
		amount, warn_ratio, is_enabled, created_at_ms, updated_at_ms
	FROM bill_budgets WHERE id = ?`

	var b BillBudget
	var scopeRef sql.NullString
	var isEnInt int

	err := s.db.QueryRow(ctx, q, id).Scan(
		&b.ID, &b.ScopeKind, &scopeRef, &b.PeriodKind, &b.Currency,
		&b.Amount, &b.WarnRatio, &isEnInt, &b.CreatedAtMs, &b.UpdatedAtMs,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if scopeRef.Valid {
		b.ScopeRef = &scopeRef.String
	}
	b.IsEnabled = (isEnInt == 1)
	return &b, nil
}

// ListBudgets retrieves all budgets.
func (s *Store) ListBudgets(ctx context.Context) ([]*BillBudget, error) {
	if s.db == nil {
		return nil, errors.New("billing: db is nil")
	}

	q := `SELECT id, scope_kind, scope_ref, period_kind, currency,
		amount, warn_ratio, is_enabled, created_at_ms, updated_at_ms
	FROM bill_budgets
	ORDER BY created_at_ms DESC`

	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*BillBudget
	for rows.Next() {
		var b BillBudget
		var scopeRef sql.NullString
		var isEnInt int

		if err := rows.Scan(
			&b.ID, &b.ScopeKind, &scopeRef, &b.PeriodKind, &b.Currency,
			&b.Amount, &b.WarnRatio, &isEnInt, &b.CreatedAtMs, &b.UpdatedAtMs,
		); err != nil {
			return nil, err
		}

		if scopeRef.Valid {
			b.ScopeRef = &scopeRef.String
		}
		b.IsEnabled = (isEnInt == 1)
		list = append(list, &b)
	}

	return list, rows.Err()
}
