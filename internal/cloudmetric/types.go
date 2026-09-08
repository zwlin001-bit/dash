package cloudmetric

// MetricQueryResponse represents columnar metric response for UI charts (docs/08-field-map.md §4).
type MetricQueryResponse struct {
	CloudAccountID string                `json:"cloud_account_id"`
	ResRef         string                `json:"res_ref"`
	Source         string                `json:"source"` // "cloud_samples"
	FromMs         int64                 `json:"from_ms"`
	ToMs           int64                 `json:"to_ms"`
	StepMs         int64                 `json:"step_ms"` // 300000 (5 min)
	TsMs           []int64               `json:"ts_ms"`
	Series         map[string][]*float64 `json:"series"`
}

// GuardedInstance holds metadata for pulling instance metrics
type GuardedInstance struct {
	ResourceID     string
	CloudAccountID string
	ResRef         string
	Region         string
	CredentialID   string
}

// AccountSyncTarget holds metadata for pulling account-level metrics
type AccountSyncTarget struct {
	CloudAccountID string
	CredentialID   string
	DefaultRegion  string
}

// SyncReport summarizes a sync run
type SyncReport struct {
	SyncedAccounts  int   `json:"synced_accounts"`
	SyncedInstances int   `json:"synced_instances"`
	PointsSaved     int   `json:"points_saved"`
	ErrorsCount     int   `json:"errors_count"`
	DurationMs      int64 `json:"duration_ms"`
}
