package notify

// Channel represents a notification target (e.g. Telegram chat, Webhook URL).
type Channel struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"channel_kind"` // "telegram" / "webhook"
	CredentialID string `json:"credential_id,omitempty"`
	ConfigJSON   string `json:"config_json"`
	IsEnabled    bool   `json:"is_enabled"`
	CreatedAtMs  int64  `json:"created_at_ms"`
	UpdatedAtMs  int64  `json:"updated_at_ms"`
	MaskedSecret string `json:"masked_secret,omitempty"` // For API responses
}

// Rule defines how events are matched and routed to channels.
type Rule struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	IsEnabled       bool   `json:"is_enabled"`
	EventPattern    string `json:"event_pattern"`  // e.g. "*", "node.*", "node.offline"
	MinSeverity     string `json:"min_severity"`   // "info" / "warning" / "critical"
	NotifyChannelID string `json:"notify_channel_id"`
	TemplateName    string `json:"template_name,omitempty"`
	ThrottleS       int    `json:"throttle_s"`
	QuietStartMin   *int   `json:"quiet_start_min,omitempty"` // 0-1439 minute of day
	QuietEndMin     *int   `json:"quiet_end_min,omitempty"`   // 0-1439 minute of day
	DisplayOrder    int    `json:"display_order"`
	CreatedAtMs     int64  `json:"created_at_ms"`
	UpdatedAtMs     int64  `json:"updated_at_ms"`
}

// Delivery records the outcome of a notification dispatch.
type Delivery struct {
	ID           string `json:"id"`
	EventID      string `json:"event_id"`
	ChannelID    string `json:"channel_id"`
	RuleID       string `json:"rule_id,omitempty"`
	State        string `json:"state"` // pending / sent / failed / throttled / quiet_held
	Attempt      int    `json:"attempt"`
	LastError    string `json:"last_error,omitempty"`
	RenderedText string `json:"rendered_text,omitempty"`
	SentAtMs     *int64 `json:"sent_at_ms,omitempty"`
	CreatedAtMs  int64  `json:"created_at_ms"`
	UpdatedAtMs  int64  `json:"updated_at_ms"`
}

// DeliveryFilter defines parameters for querying delivery records.
type DeliveryFilter struct {
	EventID   string
	ChannelID string
	State     string
	Limit     int
	Offset    int
}
