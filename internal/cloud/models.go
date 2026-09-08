package cloud

// ProviderInfo represents a registered cloud provider in providers table.
type ProviderInfo struct {
	ProviderCode string `json:"provider_code"`
	DisplayName  string `json:"display_name"`
	ExecPath     string `json:"exec_path"`
	IsEnabled    bool   `json:"is_enabled"`
	ConfigJSON   string `json:"config_json,omitempty"`
	CreatedAtMs  int64  `json:"created_at_ms"`
	UpdatedAtMs  int64  `json:"updated_at_ms"`
}

// CloudAccount represents a tenant/account in cloud_accounts table.
type CloudAccount struct {
	ID            string `json:"id"`
	ProviderCode  string `json:"provider_code"`
	Name          string `json:"name"`
	CredentialID  string `json:"credential_id"`
	DefaultRegion string `json:"default_region"`
	AccountSite   string `json:"account_site"` // "china" | "international"
	ConfigJSON    string `json:"config_json,omitempty"`
	IsEnabled     bool   `json:"is_enabled"`
	LastSyncAtMs  int64  `json:"last_sync_at_ms,omitempty"`
	CreatedAtMs   int64  `json:"created_at_ms"`
	UpdatedAtMs   int64  `json:"updated_at_ms"`
}

// CloudResource represents a discovered/synced asset in cloud_resources table.
type CloudResource struct {
	ID             string `json:"id"`
	CloudAccountID string `json:"cloud_account_id"`
	ProviderCode   string `json:"provider_code"`
	ResKind        string `json:"res_kind"`
	ResRef         string `json:"res_ref"`
	Name           string `json:"name"`
	Region         string `json:"region"`
	Status         string `json:"status"`
	PublicIPs      string `json:"public_ips"`
	PrivateIPs     string `json:"private_ips"`
	SpecsJSON      string `json:"specs_json,omitempty"`
	BillingJSON    string `json:"billing_json,omitempty"`
	AttrsJSON      string `json:"attrs_json,omitempty"`
	NodeID         string `json:"node_id,omitempty"`
	SyncedAtMs     int64  `json:"synced_at_ms"`
	IsDeleted      bool   `json:"is_deleted"`
	CreatedAtMs    int64  `json:"created_at_ms"`
	UpdatedAtMs    int64  `json:"updated_at_ms"`
}

// SyncJobStatus tracks asynchronous sync operations.
type SyncJobStatus struct {
	JobID        string `json:"job_id"`
	AccountID    string `json:"account_id"`
	State        string `json:"state"` // "pending" | "running" | "succeeded" | "failed"
	Message      string `json:"message,omitempty"`
	SyncedCount  int    `json:"synced_count"`
	TrafficBytes int64  `json:"traffic_bytes"`
}
