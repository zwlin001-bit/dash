package billing

// BillPeriod represents a monthly bill overview row in bill_periods.
type BillPeriod struct {
	ID             string   `json:"id"`
	CloudAccountID string   `json:"cloud_account_id"`
	Period         string   `json:"period"` // e.g. "2026-09"
	Currency       string   `json:"currency"`
	TotalAmount    float64  `json:"total_amount"`
	PretaxAmount   *float64 `json:"pretax_amount,omitempty"`
	DiscountAmount *float64 `json:"discount_amount,omitempty"`
	SyncState      string   `json:"sync_state"` // pending / syncing / ok / failed
	SyncedAtMs     *int64   `json:"synced_at_ms,omitempty"`
	ErrorText      *string  `json:"error_text,omitempty"`
	CreatedAtMs    int64    `json:"created_at_ms"`
	UpdatedAtMs    int64    `json:"updated_at_ms"`

	// Enriched fields
	AccountName string `json:"account_name,omitempty"`
}

// BillItem represents a granular line item in bill_items.
type BillItem struct {
	ID              string   `json:"id"`
	CloudAccountID  string   `json:"cloud_account_id"`
	Period          string   `json:"period"` // e.g. "2026-09"
	ResKind         string   `json:"res_kind"` // instance / disk / ip / bandwidth / other
	ResRef          *string  `json:"res_ref,omitempty"`
	CloudResourceID *string  `json:"cloud_resource_id,omitempty"`
	ItemName        *string  `json:"item_name,omitempty"`
	ProductCode     *string  `json:"product_code,omitempty"`
	Currency        string   `json:"currency"`
	Amount          float64  `json:"amount"`
	UsageText       *string  `json:"usage_text,omitempty"`
	CreatedAtMs     int64    `json:"created_at_ms"`
	UpdatedAtMs     int64    `json:"updated_at_ms"`

	// Enriched fields
	AccountName string   `json:"account_name,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// BillBudget represents a budget rule in bill_budgets.
type BillBudget struct {
	ID          string  `json:"id"`
	ScopeKind   string  `json:"scope_kind"` // all / account / tag
	ScopeRef    *string `json:"scope_ref,omitempty"`
	PeriodKind  string  `json:"period_kind"` // month
	Currency    string  `json:"currency"`
	Amount      float64 `json:"amount"`
	WarnRatio   float64 `json:"warn_ratio"` // e.g. 0.8
	IsEnabled   bool    `json:"is_enabled"`
	CreatedAtMs int64   `json:"created_at_ms"`
	UpdatedAtMs int64   `json:"updated_at_ms"`

	// Enriched fields
	CurrentSpent float64 `json:"current_spent"`
	Progress     float64 `json:"progress"`
	ScopeName    string  `json:"scope_name,omitempty"`
}

// CurrencySummary holds aggregated metrics for a single currency in the given period.
type CurrencySummary struct {
	Currency       string   `json:"currency"`
	TotalAmount    float64  `json:"total_amount"`
	PretaxAmount   float64  `json:"pretax_amount"`
	DiscountAmount float64  `json:"discount_amount"`
	LastMonthTotal float64  `json:"last_month_total"`
	MoMRatio       *float64 `json:"mom_ratio,omitempty"`
	BudgetAmount   float64  `json:"budget_amount"`
	BudgetProgress float64  `json:"budget_progress"`
}

// MonthPoint is a data point for monthly trend chart.
type MonthPoint struct {
	Period string  `json:"period"`
	Amount float64 `json:"amount"`
}

// CurrencyTrend holds 12-month historical data for a specific currency.
type CurrencyTrend struct {
	Currency string       `json:"currency"`
	Months   []MonthPoint `json:"months"`
}

// BillingOverview is the response structure for the overview tab.
type BillingOverview struct {
	Period     string            `json:"period"`
	Totals     []CurrencySummary `json:"totals"`
	Trends     []CurrencyTrend   `json:"trends"`
	SyncedAtMs *int64            `json:"synced_at_ms,omitempty"`
	SyncState  string            `json:"sync_state"`
}

// AggregatedGroup represents items grouped by kind, resource, or tag.
type AggregatedGroup struct {
	Key         string     `json:"key"`
	DisplayName string     `json:"display_name"`
	TotalAmount float64    `json:"total_amount"`
	Currency    string     `json:"currency"`
	ItemCount   int        `json:"item_count"`
	IsUnlinked  bool       `json:"is_unlinked"`
	Items       []BillItem `json:"items,omitempty"`
}

// ItemsResponse is the response structure for items list & breakdown.
type ItemsResponse struct {
	Period string            `json:"period"`
	Groups []AggregatedGroup `json:"groups"`
	Items  []BillItem        `json:"items"`
}
