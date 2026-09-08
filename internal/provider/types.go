package provider

import "encoding/json"

// JSON-RPC 2.0 wire models
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return e.Message
}

// ProviderDescription represents provider metadata
type ProviderDescription struct {
	ProviderCode     string   `json:"provider_code"`
	DisplayName      string   `json:"display_name"`
	SupportedKinds   []string `json:"supported_kinds"`
	CredentialFields []string `json:"credential_fields"`
	Capabilities     []string `json:"capabilities"`
}

// HealthcheckParams parameters for provider.healthcheck
type HealthcheckParams struct {
	Credential map[string]string `json:"credential"`
	Region     string            `json:"region,omitempty"`
}

type HealthcheckResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// ResourceSpecs holds compute/memory/disk specs
type ResourceSpecs struct {
	VCPU   int `json:"vcpu"`
	MemMB  int `json:"mem_mb"`
	DiskGB int `json:"disk_gb"`
}

// ResourceBilling holds billing metadata
type ResourceBilling struct {
	Currency    string  `json:"currency,omitempty"`
	Price       float64 `json:"price,omitempty"`
	CycleDays   int     `json:"cycle_days,omitempty"`
	ExpiresAtMs int64   `json:"expires_at_ms,omitempty"`
}

// NormalizedResource represents a normalized cloud resource across providers
type NormalizedResource struct {
	ProviderCode string          `json:"provider_code"`
	Kind         string          `json:"kind"` // "instance"
	Ref          string          `json:"ref"`  // Cloud instance id, e.g. "i-xxxx"
	Name         string          `json:"name"`
	Region       string          `json:"region"`
	Status       string          `json:"status"` // "running", "stopped", "starting", "stopping", etc.
	PublicIPs    []string        `json:"public_ips"`
	PrivateIPs   []string        `json:"private_ips"`
	Specs        ResourceSpecs   `json:"specs"`
	Billing      ResourceBilling `json:"billing"`
	Attrs        map[string]any  `json:"attrs"`
	BillError    string          `json:"bill_error,omitempty"`
}

// ListResourcesParams parameters for resource.list
type ListResourcesParams struct {
	Credential  map[string]string `json:"credential"`
	Region      string            `json:"region,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	AccountSite string            `json:"account_site,omitempty"` // "china" | "international"
}

// GetResourceParams parameters for resource.get
type GetResourceParams struct {
	Credential map[string]string `json:"credential"`
	Region     string            `json:"region"`
	Kind       string            `json:"kind"`
	Ref        string            `json:"ref"`
}

// DiscoverParams parameters for provider.discover
type DiscoverParams struct {
	Credential  map[string]string `json:"credential"`
	Regions     []string          `json:"regions,omitempty"`
	AccountSite string            `json:"account_site,omitempty"`
}

// TrafficParams parameters for traffic.get
type TrafficParams struct {
	Credential map[string]string `json:"credential"`
}

// CDTTrafficResult holds account-level CDT traffic
type CDTTrafficResult struct {
	TrafficBytes int64              `json:"traffic_bytes"`
	Details      []CDTTrafficDetail `json:"details,omitempty"`
}

type CDTTrafficDetail struct {
	Traffic     int64  `json:"traffic"`
	ProductType string `json:"product_type,omitempty"`
}

// ActionParams parameters for resource.action
type ActionParams struct {
	Credential map[string]string `json:"credential"`
	Region     string            `json:"region"`
	Kind       string            `json:"kind"`
	Ref        string            `json:"ref"`
	Action     string            `json:"action"` // "start" | "stop"
}

type ActionResponse struct {
	JobHandle string `json:"job_handle"`
	Status    string `json:"status"` // "running" | "succeeded" | "failed"
	Message   string `json:"message,omitempty"`
}

// PollJobParams parameters for job.poll
type PollJobParams struct {
	JobHandle string `json:"job_handle"`
}

type PollJobResponse struct {
	JobHandle string `json:"job_handle"`
	Status    string `json:"status"` // "pending" | "running" | "succeeded" | "failed"
	Message   string `json:"message,omitempty"`
}

// MetricPoint represents a single metric data point
type MetricPoint struct {
	TsMs  int64   `json:"ts_ms"`
	Value float64 `json:"value"`
}

// MetricListParams parameters for metric.list
type MetricListParams struct {
	Credential  map[string]string `json:"credential"`
	Region      string            `json:"region,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	ResRef      string            `json:"res_ref"`
	MetricCode  string            `json:"metric_code"`
	StartTimeMs int64             `json:"start_time_ms"`
	EndTimeMs   int64             `json:"end_time_ms"`
	PeriodSec   int               `json:"period_sec,omitempty"`
}

// MetricListResult holds metric series returned from provider
type MetricListResult struct {
	ResRef     string        `json:"res_ref"`
	MetricCode string        `json:"metric_code"`
	Points     []MetricPoint `json:"points"`
}

// BillListParams parameters for bill.list
type BillListParams struct {
	Credential  map[string]string `json:"credential"`
	Period      string            `json:"period"` // "2026-09"
	AccountSite string            `json:"account_site,omitempty"`
}

// BillItem normalized line item for cloud bill
type BillItem struct {
	ResKind     string  `json:"res_kind"`
	ResRef      string  `json:"res_ref,omitempty"`
	ItemName    string  `json:"item_name,omitempty"`
	ProductCode string  `json:"product_code,omitempty"`
	Amount      float64 `json:"amount"`
	UsageText   string  `json:"usage_text,omitempty"`
}

// BillListResult returns normalized bill details for period
type BillListResult struct {
	Period         string     `json:"period"`
	Currency       string     `json:"currency"`
	TotalAmount    float64    `json:"total_amount"`
	PretaxAmount   float64    `json:"pretax_amount,omitempty"`
	DiscountAmount float64    `json:"discount_amount,omitempty"`
	Items          []BillItem `json:"items"`
}
