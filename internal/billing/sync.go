package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dash/internal/alert"
	"dash/internal/credentials"
	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/logx"
	"dash/internal/provider"
)

// Service coordinates bill synchronization, aggregations, and budget evaluations.
type Service struct {
	db          *db.DB
	store       *Store
	credStore   *credentials.Store
	pm          *provider.Manager
	alertEngine *alert.Engine
}

// NewService creates a new billing Service.
func NewService(database *db.DB, st *Store, creds *credentials.Store, pm *provider.Manager, ae *alert.Engine) *Service {
	return &Service{
		db:          database,
		store:       st,
		credStore:   creds,
		pm:          pm,
		alertEngine: ae,
	}
}

// Store returns the underlying billing Store.
func (s *Service) Store() *Store {
	return s.store
}

// SyncAccount synchronizes bill data for a single cloud account over the past N months.
func (s *Service) SyncAccount(ctx context.Context, accountID string, backfillMonths int) error {
	if s.db == nil {
		return errors.New("billing: db is nil")
	}

	// 1. Load cloud account
	var accID, providerCode, accName, credID string
	var accSite sql.NullString
	var isEnabled int

	q := "SELECT id, provider_code, name, credential_id, account_site, is_enabled FROM cloud_accounts WHERE id = ?"
	err := s.db.QueryRow(ctx, q, accountID).Scan(&accID, &providerCode, &accName, &credID, &accSite, &isEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("billing: cloud account %s not found", accountID)
	}
	if err != nil {
		return err
	}
	if isEnabled != 1 {
		return fmt.Errorf("billing: cloud account %s is disabled", accName)
	}

	accountSite := "china"
	if accSite.Valid && accSite.String != "" {
		accountSite = accSite.String
	}

	// 2. Decrypt credential
	if s.credStore == nil {
		return errors.New("billing: credential store is nil")
	}
	_, decrypted, err := s.credStore.GetDecrypted(ctx, credID)
	if err != nil {
		return fmt.Errorf("billing: decrypt credential for %s failed: %w", accName, err)
	}
	var credData map[string]string
	if err := json.Unmarshal(decrypted, &credData); err != nil {
		return fmt.Errorf("billing: unmarshal credential for %s failed: %w", accName, err)
	}

	// 3. Get provider client
	if s.pm == nil {
		return errors.New("billing: provider manager is nil")
	}
	client, err := s.pm.GetClient(ctx, providerCode)
	if err != nil {
		return fmt.Errorf("billing: get client for provider %s failed: %w", providerCode, err)
	}

	if backfillMonths <= 0 {
		backfillMonths = 12
	}

	now := time.Now().UTC()
	currentPeriod := now.Format("2006-01")

	// Generate months from oldest to current
	periods := make([]string, 0, backfillMonths)
	for i := backfillMonths - 1; i >= 0; i-- {
		m := now.AddDate(0, -i, 0).Format("2006-01")
		periods = append(periods, m)
	}

	// Pre-load cloud_resources to map res_ref -> cloud_resource_id
	resMap := make(map[string]string)
	resRows, err := s.db.Query(ctx, "SELECT id, res_ref FROM cloud_resources WHERE cloud_account_id = ? AND is_deleted = 0", accID)
	if err == nil {
		defer resRows.Close()
		for resRows.Next() {
			var crID, crRef string
			if err := resRows.Scan(&crID, &crRef); err == nil && crRef != "" {
				resMap[crRef] = crID
			}
		}
	}

	for _, period := range periods {
		// ★ 已关账的历史月份只拉一次：sync_state='ok' 且 period < 当月即跳过
		if period < currentPeriod {
			existing, err := s.store.GetPeriod(ctx, accID, period)
			if err == nil && existing != nil && existing.SyncState == "ok" {
				logx.Info(fmt.Sprintf("billing: period %s for %s is already closed and synced, skipping", period, accName))
				continue
			}
		}

		_ = s.store.UpsertPeriod(ctx, &BillPeriod{
			CloudAccountID: accID,
			Period:         period,
			Currency:       "CNY",
			SyncState:      "syncing",
		})

		// Throttle API calls
		time.Sleep(100 * time.Millisecond)

		res, err := client.ListBills(ctx, credData, period, accountSite)
		nowMs := time.Now().UnixMilli()

		if err != nil {
			redactedMsg := logx.Redact(err.Error())
			_ = s.store.UpsertPeriod(ctx, &BillPeriod{
				CloudAccountID: accID,
				Period:         period,
				Currency:       "CNY",
				SyncState:      "failed",
				ErrorText:      &redactedMsg,
				UpdatedAtMs:    nowMs,
			})

			// Emit billing.sync_failed event
			events.Emit(ctx, events.Event{
				Type:       "billing.sync_failed",
				Source:     "billing",
				TargetKind: "cloud_account",
				TargetID:   accID,
				Title:      fmt.Sprintf("云账号 %s 账单同步失败", accName),
				Payload: map[string]any{
					"AccountID":   accID,
					"AccountName": accName,
					"Error":       redactedMsg,
					"Period":      period,
				},
				DedupKey:   fmt.Sprintf("billing:sync_failed:%s:%s", accID, period),
				OccurredAt: nowMs,
			})

			logx.Warn(fmt.Sprintf("billing: sync period %s failed for %s: %s", period, accName, redactedMsg))
			// Return error for this period but allow caller to isolate per account
			return fmt.Errorf("sync period %s failed: %w", period, err)
		}

		// Success: map and insert items
		var items []BillItem
		for _, rawItem := range res.Items {
			it := BillItem{
				CloudAccountID: accID,
				Period:         period,
				ResKind:        rawItem.ResKind,
				Currency:       res.Currency,
				Amount:         rawItem.Amount,
			}
			if rawItem.ResRef != "" {
				ref := rawItem.ResRef
				it.ResRef = &ref
				if crID, ok := resMap[ref]; ok {
					it.CloudResourceID = &crID
				}
			}
			if rawItem.ItemName != "" {
				name := rawItem.ItemName
				it.ItemName = &name
			}
			if rawItem.ProductCode != "" {
				pCode := rawItem.ProductCode
				it.ProductCode = &pCode
			}
			if rawItem.UsageText != "" {
				uText := rawItem.UsageText
				it.UsageText = &uText
			}
			items = append(items, it)
		}

		if err := s.store.ReplaceItems(ctx, accID, period, items); err != nil {
			logx.Error(fmt.Sprintf("billing: replace items failed for %s %s: %v", accName, period, err))
		}

		pRecord := &BillPeriod{
			CloudAccountID: accID,
			Period:         period,
			Currency:       res.Currency,
			TotalAmount:    res.TotalAmount,
			PretaxAmount:   &res.PretaxAmount,
			DiscountAmount: &res.DiscountAmount,
			SyncState:      "ok",
			SyncedAtMs:     &nowMs,
			ErrorText:      nil,
		}
		if err := s.store.UpsertPeriod(ctx, pRecord); err != nil {
			logx.Error(fmt.Sprintf("billing: save period %s failed: %v", period, err))
		}
	}

	// Update cloud_accounts.last_sync_at_ms
	nowMs := time.Now().UnixMilli()
	_, _ = s.db.Exec(ctx, "UPDATE cloud_accounts SET last_sync_at_ms = ?, updated_at_ms = ? WHERE id = ?", nowMs, nowMs, accID)

	// Trigger budget evaluation
	if s.alertEngine != nil {
		_ = s.alertEngine.EvaluateKind(ctx, alert.RuleKindBudget)
	}

	return nil
}

// SyncAll syncs all enabled cloud accounts. Errors in one account do not block others.
func (s *Service) SyncAll(ctx context.Context, backfillMonths int) error {
	if s.db == nil {
		return errors.New("billing: db is nil")
	}

	rows, err := s.db.Query(ctx, "SELECT id, name FROM cloud_accounts WHERE is_enabled = 1")
	if err != nil {
		return err
	}
	defer rows.Close()

	type accEntry struct {
		id   string
		name string
	}
	var accounts []accEntry
	for rows.Next() {
		var a accEntry
		if err := rows.Scan(&a.id, &a.name); err == nil {
			accounts = append(accounts, a)
		}
	}

	var errs []string
	for _, a := range accounts {
		if err := s.SyncAccount(ctx, a.id, backfillMonths); err != nil {
			logx.Warn(fmt.Sprintf("billing: sync account %s failed: %v", a.name, err))
			errs = append(errs, fmt.Sprintf("%s: %v", a.name, err))
		}
	}

	if len(errs) > 0 && len(errs) == len(accounts) {
		return fmt.Errorf("all accounts failed to sync: %s", strings.Join(errs, "; "))
	}
	return nil
}

// GetOverview aggregates summary metrics and monthly trends per currency.
func (s *Service) GetOverview(ctx context.Context, period string) (*BillingOverview, error) {
	if period == "" {
		period = time.Now().UTC().Format("2006-01")
	}

	periods, err := s.store.ListPeriods(ctx, period)
	if err != nil {
		return nil, err
	}

	// Determine sync state and latest synced timestamp
	overallState := "ok"
	var latestSyncedMs *int64
	for _, p := range periods {
		if p.SyncedAtMs != nil {
			if latestSyncedMs == nil || *p.SyncedAtMs > *latestSyncedMs {
				latestSyncedMs = p.SyncedAtMs
			}
		}
		if p.SyncState == "syncing" {
			overallState = "syncing"
		} else if p.SyncState == "failed" && overallState != "syncing" {
			overallState = "failed"
		}
	}

	// Last month period
	t, _ := time.Parse("2006-01", period)
	lastPeriod := t.AddDate(0, -1, 0).Format("2006-01")
	lastPeriods, _ := s.store.ListPeriods(ctx, lastPeriod)
	lastTotalsByCurrency := make(map[string]float64)
	for _, lp := range lastPeriods {
		lastTotalsByCurrency[lp.Currency] += lp.TotalAmount
	}

	// Budgets by currency
	budgets, _ := s.store.ListBudgets(ctx)
	budgetsByCurrency := make(map[string]float64)
	for _, b := range budgets {
		if b.IsEnabled && b.ScopeKind == "all" {
			budgetsByCurrency[b.Currency] += b.Amount
		}
	}

	// Current month totals by currency
	type currAccum struct {
		totalAmount    float64
		pretaxAmount   float64
		discountAmount float64
	}
	currMap := make(map[string]*currAccum)
	for _, p := range periods {
		c, ok := currMap[p.Currency]
		if !ok {
			c = &currAccum{}
			currMap[p.Currency] = c
		}
		c.totalAmount += p.TotalAmount
		if p.PretaxAmount != nil {
			c.pretaxAmount += *p.PretaxAmount
		}
		if p.DiscountAmount != nil {
			c.discountAmount += *p.DiscountAmount
		}
	}

	// Ensure currencies from budgets or periods are represented
	for curr := range budgetsByCurrency {
		if _, ok := currMap[curr]; !ok {
			currMap[curr] = &currAccum{}
		}
	}
	if len(currMap) == 0 {
		currMap["CNY"] = &currAccum{}
	}

	var totals []CurrencySummary
	for curr, accum := range currMap {
		lastTotal := lastTotalsByCurrency[curr]
		var momRatio *float64
		if lastTotal > 0 {
			r := (accum.totalAmount - lastTotal) / lastTotal
			momRatio = &r
		}

		budgetAmt := budgetsByCurrency[curr]
		var progress float64
		if budgetAmt > 0 {
			progress = accum.totalAmount / budgetAmt
		}

		totals = append(totals, CurrencySummary{
			Currency:       curr,
			TotalAmount:    accum.totalAmount,
			PretaxAmount:   accum.pretaxAmount,
			DiscountAmount: accum.discountAmount,
			LastMonthTotal: lastTotal,
			MoMRatio:       momRatio,
			BudgetAmount:   budgetAmt,
			BudgetProgress: progress,
		})
	}

	// 12-month trends per currency
	startPeriod := t.AddDate(0, -11, 0).Format("2006-01")
	historyPeriods, _ := s.store.ListPeriodsByRange(ctx, startPeriod, period)

	// Build 12 months array
	monthList := make([]string, 0, 12)
	for i := 11; i >= 0; i-- {
		monthList = append(monthList, t.AddDate(0, -i, 0).Format("2006-01"))
	}

	histMap := make(map[string]map[string]float64) // currency -> period -> amount
	for _, hp := range historyPeriods {
		if _, ok := histMap[hp.Currency]; !ok {
			histMap[hp.Currency] = make(map[string]float64)
		}
		histMap[hp.Currency][hp.Period] += hp.TotalAmount
	}

	var trends []CurrencyTrend
	for curr := range currMap {
		points := make([]MonthPoint, 0, len(monthList))
		for _, m := range monthList {
			amt := 0.0
			if histMap[curr] != nil {
				amt = histMap[curr][m]
			}
			points = append(points, MonthPoint{
				Period: m,
				Amount: amt,
			})
		}
		trends = append(trends, CurrencyTrend{
			Currency: curr,
			Months:   points,
		})
	}

	return &BillingOverview{
		Period:     period,
		Totals:     totals,
		Trends:     trends,
		SyncedAtMs: latestSyncedMs,
		SyncState:  overallState,
	}, nil
}

// GetItems returns line items and aggregated groups for a given period and groupBy dimension.
func (s *Service) GetItems(ctx context.Context, period, groupBy, accountID string) (*ItemsResponse, error) {
	if period == "" {
		period = time.Now().UTC().Format("2006-01")
	}

	items, err := s.store.ListItems(ctx, period, accountID)
	if err != nil {
		return nil, err
	}

	// Enrich items with node tags
	tagMap := make(map[string][]string) // cloud_resource_id -> []tag
	qTags := `SELECT cr.id, t.name
FROM cloud_resources cr
JOIN nodes n ON cr.node_id = n.id
JOIN node_tags nt ON nt.node_id = n.id
JOIN tags t ON nt.tag_id = t.id`
	rows, err := s.db.Query(ctx, qTags)
	if err != nil {
		logx.Error(fmt.Sprintf("billing: query tags failed: %v", err))
	} else {
		defer rows.Close()
		for rows.Next() {
			var crID, tName string
			if err := rows.Scan(&crID, &tName); err == nil {
				tagMap[crID] = append(tagMap[crID], tName)
			}
		}
	}

	for i := range items {
		if items[i].CloudResourceID != nil {
			if tags, ok := tagMap[*items[i].CloudResourceID]; ok {
				items[i].Tags = tags
			}
		}
	}

	// Grouping logic
	groupMap := make(map[string]*AggregatedGroup)
	var unlinkedGroup *AggregatedGroup

	for _, it := range items {
		isUnlinked := (it.CloudResourceID == nil || it.ResKind == "other" || (it.ResRef != nil && *it.ResRef == ""))

		if groupBy == "tag" {
			if isUnlinked || len(it.Tags) == 0 {
				if unlinkedGroup == nil {
					unlinkedGroup = &AggregatedGroup{
						Key:         "unlinked",
						DisplayName: "未关联资源",
						Currency:    it.Currency,
						IsUnlinked:  true,
					}
				}
				unlinkedGroup.TotalAmount += it.Amount
				unlinkedGroup.ItemCount++
				unlinkedGroup.Items = append(unlinkedGroup.Items, it)
			} else {
				for _, tag := range it.Tags {
					g, ok := groupMap[tag]
					if !ok {
						g = &AggregatedGroup{
							Key:         tag,
							DisplayName: "标签: " + tag,
							Currency:    it.Currency,
						}
						groupMap[tag] = g
					}
					g.TotalAmount += it.Amount
					g.ItemCount++
					g.Items = append(g.Items, it)
				}
			}
		} else if groupBy == "resource" {
			if isUnlinked {
				if unlinkedGroup == nil {
					unlinkedGroup = &AggregatedGroup{
						Key:         "unlinked",
						DisplayName: "未关联资源",
						Currency:    it.Currency,
						IsUnlinked:  true,
					}
				}
				unlinkedGroup.TotalAmount += it.Amount
				unlinkedGroup.ItemCount++
				unlinkedGroup.Items = append(unlinkedGroup.Items, it)
			} else {
				key := it.ResKind + ":" + it.ID
				name := "资源"
				if it.ItemName != nil && *it.ItemName != "" {
					name = *it.ItemName
				} else if it.ResRef != nil && *it.ResRef != "" {
					name = *it.ResRef
				}
				g, ok := groupMap[key]
				if !ok {
					g = &AggregatedGroup{
						Key:         key,
						DisplayName: name,
						Currency:    it.Currency,
					}
					groupMap[key] = g
				}
				g.TotalAmount += it.Amount
				g.ItemCount++
				g.Items = append(g.Items, it)
			}
		} else { // default: groupBy == "kind"
			if isUnlinked {
				if unlinkedGroup == nil {
					unlinkedGroup = &AggregatedGroup{
						Key:         "unlinked",
						DisplayName: "未关联资源",
						Currency:    it.Currency,
						IsUnlinked:  true,
					}
				}
				unlinkedGroup.TotalAmount += it.Amount
				unlinkedGroup.ItemCount++
				unlinkedGroup.Items = append(unlinkedGroup.Items, it)
			} else {
				kindLabels := map[string]string{
					"instance":  "ECS计算实例",
					"disk":      "云盘存储",
					"ip":        "弹性公网IP",
					"bandwidth": "公网带宽/CDT",
					"other":     "其他费用",
				}
				label, ok := kindLabels[it.ResKind]
				if !ok {
					label = it.ResKind
				}
				g, ok := groupMap[it.ResKind]
				if !ok {
					g = &AggregatedGroup{
						Key:         it.ResKind,
						DisplayName: label,
						Currency:    it.Currency,
					}
					groupMap[it.ResKind] = g
				}
				g.TotalAmount += it.Amount
				g.ItemCount++
				g.Items = append(g.Items, it)
			}
		}
	}

	var groups []AggregatedGroup
	for _, g := range groupMap {
		groups = append(groups, *g)
	}
	if unlinkedGroup != nil {
		groups = append(groups, *unlinkedGroup)
	}

	return &ItemsResponse{
		Period: period,
		Groups: groups,
		Items:  items,
	}, nil
}
